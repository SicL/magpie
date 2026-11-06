package orchestration

import (
	"errors"
	"strings"
	"testing"
)

func TestDispatchReadyStartsOnlyVerifiedDSHInOwnWorktrees(t *testing.T) {
	store, runner, first := setupRunner(t, "complete")
	manual, err := store.CreateTask(Task{ProjectID: first.ProjectID, Title: "Manual WorkBuddy task", Agent: "WorkBuddy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateWorktree(manual.ID); err != nil {
		t.Fatal(err)
	}
	manual = readyGraphTask(t, store, manual)
	other := newGraphTask(t, store, first.ProjectID, "Second DSH task")
	other = readyGraphTask(t, store, other)
	blocked := newGraphTask(t, store, first.ProjectID, "Unintegrated DSH task")
	blocked, err = store.ReplaceDependencies(blocked.ID, blocked.Revision, []string{first.ID})
	if err != nil {
		t.Fatal(err)
	}
	blocked = readyGraphTask(t, store, blocked)
	result, err := runner.DispatchReady(first.ProjectID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Started) != 2 {
		t.Fatalf("want 2 DSH tasks started got %+v", result)
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("want manual and blocked tasks skipped got %+v", result)
	}
	if result.Started[0].TaskID != first.ID || result.Started[1].TaskID != other.ID {
		t.Fatalf("wrong tasks started %+v", result)
	}
	for _, x := range result.Skipped {
		if x.TaskID != manual.ID && x.TaskID != blocked.ID {
			t.Fatalf("unexpected skip %+v", x)
		}
	}
	runner.wait.Wait()
	after, err := store.taskByID(manual.ID)
	if err != nil || after.Status != "ready" {
		t.Fatalf("manual task was changed %+v %v", after, err)
	}
	if _, err = runner.DispatchReady(first.ProjectID, 4); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid dispatch max accepted %v", err)
	}
	if _, err = runner.DispatchReady("nonexistent", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nonexistent project accepted %v", err)
	}
}

func TestCapabilitiesDoNotClaimUnverifiedCLIIsRunnable(t *testing.T) {
	caps := AgentCapabilities()
	if len(caps) < 5 {
		t.Fatalf("missing agent entries: %+v", caps)
	}
	for _, c := range caps {
		if strings.Contains(strings.ToLower(c.ID), "zcode") || strings.Contains(strings.ToLower(c.ID), "workbuddy") || strings.Contains(strings.ToLower(c.ID), "codebuddy") {
			if c.AutoRunnable {
				t.Fatalf("unverified agent marked runnable: %+v", c)
			}
		}
		if c.Name == "" || c.ID == "" {
			t.Fatalf("invalid agent entry: %+v", c)
		}
	}
}

func TestDispatchReadyCapacityLeavesOtherTasksReady(t *testing.T) {
	store, runner, first := setupRunner(t, "complete")
	later := newGraphTask(t, store, first.ProjectID, "Later task")
	later = readyGraphTask(t, store, later)
	result, err := runner.DispatchReady(first.ProjectID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Started) != 1 || len(result.Skipped) != 1 {
		t.Fatalf("capacity accounting incorrect: %+v", result)
	}
	if result.Skipped[0].TaskID != later.ID || !strings.Contains(result.Skipped[0].Reason, "limit") {
		t.Fatalf("deferred task not explained: %+v", result.Skipped)
	}
	runner.wait.Wait()
	current, err := store.taskByID(later.ID)
	if err != nil || current.Status != "ready" {
		t.Fatalf("deferred task state changed: %+v %v", current, err)
	}
}
