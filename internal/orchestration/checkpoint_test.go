package orchestration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointPersistsGitSnapshot(t *testing.T) {
	store, _, task := setupRunner(t, "complete")
	in := CheckpointInput{TaskID: task.ID, Revision: task.Revision, Summary: "Reviewed API changes; targeted tests passed", TestStatus: "passed"}
	first, err := store.CreateCheckpoint(in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Dirty || first.GitHead == "" {
		t.Fatalf("clean snapshot %+v", first)
	}
	worktree, err := store.worktreeForTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(worktree, "draft.txt"), []byte("new change"), 0600); err != nil {
		t.Fatal(err)
	}
	in.Summary = "Uncommitted work remaining"
	in.TestStatus = "not_run"
	second, err := store.CreateCheckpoint(in)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Dirty || second.GitHead != first.GitHead {
		t.Fatalf("dirty worktree snapshot %+v", second)
	}
	list, err := store.Checkpoints(task.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("checkpoints %+v %v", list, err)
	}
	if _, err = store.CreateCheckpoint(CheckpointInput{TaskID: task.ID, Revision: task.Revision, Summary: "invalid tests", TestStatus: "verified"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid test status %v", err)
	}
	if _, err = store.CreateCheckpoint(CheckpointInput{TaskID: "unknown", Revision: task.Revision, Summary: "none", TestStatus: "not_run"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown task %v", err)
	}
	_, err = store.SetTaskStatus(task.ID, task.Revision, "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateCheckpoint(in); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale task revision %v", err)
	}
	if _, err = store.Checkpoints("unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown list %v", err)
	}
}

func TestManualInterruptedRecoveryKeepsTaskBlocked(t *testing.T) {
	store, runner, task := setupRunner(t, "complete")
	cwd, err := store.worktreeForTask(task)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.reserveRun(task, task.Revision, cwd)
	if err != nil {
		t.Fatal(err)
	}
	const pid = 321432143
	if err = store.runStarted(old.ID, pid); err != nil {
		t.Fatal(err)
	}
	if err = store.markPreviousRunsUncertain(); err != nil {
		t.Fatal(err)
	}
	current, err := store.taskByID(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner.processGone = func(n int) (bool, error) { return n == pid, nil }
	input := RecoveryInput{Revision: current.Revision, Verified: true, Reason: "Confirmed old process and descendants stopped, reviewed Git worktree"}
	if _, err = runner.ResolveInterrupted(old.ID, RecoveryInput{Revision: current.Revision, Reason: input.Reason}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing confirmation %v", err)
	}
	runner.processGone = func(int) (bool, error) { return false, nil }
	if _, err = runner.ResolveInterrupted(old.ID, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("live process wrongly unlocked %v", err)
	}
	runner.processGone = func(n int) (bool, error) { return n == pid, nil }
	if _, err = runner.ResolveInterrupted(old.ID, RecoveryInput{Revision: 1, Verified: true, Reason: input.Reason}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision %v", err)
	}
	result, err := runner.ResolveInterrupted(old.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousPID != pid || result.TaskID != task.ID || result.GitHead == "" {
		t.Fatalf("recovery evidence %+v", result)
	}
	history, err := store.Recoveries(task.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("recovery history %+v %v", history, err)
	}
	run, err := store.Run(old.ID)
	if err != nil || run.State != "reconciled" {
		t.Fatalf("run after recovery %+v %v", run, err)
	}
	current, err = store.taskByID(task.ID)
	if err != nil || current.Status != "blocked" {
		t.Fatalf("workflow after recovery %+v %v", current, err)
	}
	if _, err = runner.ResolveInterrupted(old.ID, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeated recovery %v", err)
	}
	if _, err = store.SetTaskStatus(task.ID, current.Revision, "ready"); err != nil {
		t.Fatal(err)
	}
	// New sessions require another explicit Start; reconciliation itself
	// must not start a new process or reuse a suspect interrupted session.
	if len(runner.active) != 0 {
		t.Fatal("recovery launched process unexpectedly")
	}
}

func TestCheckpointSummaryNotCopiedAcrossTasks(t *testing.T) {
	store, _, task := setupRunner(t, "complete")
	a, err := store.CreateCheckpoint(CheckpointInput{TaskID: task.ID, Revision: task.Revision, Summary: "Private project summary", TestStatus: "not_run"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateTask(Task{ProjectID: task.ProjectID, Title: "Other task", Agent: "DSH"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateCheckpoint(CheckpointInput{TaskID: other.ID, Revision: other.Revision, Summary: a.Summary, RunID: strings.Repeat("a", 32), TestStatus: "not_run"}); err == nil {
		t.Fatal("cross-task checkpoint accepted")
	}
}
