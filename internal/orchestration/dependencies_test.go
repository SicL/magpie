package orchestration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/proc"
)

func newGraphTask(t *testing.T, s *Store, p string, title string) Task {
	t.Helper()
	item, err := s.CreateTask(Task{ProjectID: p, Title: title, Agent: "DeepSeek Harness"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateWorktree(item.ID); err != nil {
		t.Fatal(err)
	}
	return item
}

func readyGraphTask(t *testing.T, s *Store, item Task) Task {
	t.Helper()
	out, err := s.SetTaskStatus(item.ID, item.Revision, "ready")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func finishGraphTask(t *testing.T, s *Store, item Task) Task {
	t.Helper()
	var err error
	item, err = s.SetTaskStatus(item.ID, item.Revision, "running")
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.SetTaskStatus(item.ID, item.Revision, "review")
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.SetTaskStatus(item.ID, item.Revision, "done")
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestDependencyGraphRejectsCyclesAndStaleEdits(t *testing.T) {
	store, _, a := setupRunner(t, "complete")
	b := newGraphTask(t, store, a.ProjectID, "B")
	c := newGraphTask(t, store, a.ProjectID, "C")
	changed, err := store.ReplaceDependencies(a.ID, a.Revision, []string{b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Revision != a.Revision+1 {
		t.Fatalf("revision not incremented %+v", changed)
	}
	if _, err = store.ReplaceDependencies(a.ID, a.Revision, []string{c.ID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale graph overwrite %v", err)
	}
	b, err = store.ReplaceDependencies(b.ID, b.Revision, []string{c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceDependencies(c.ID, c.Revision, []string{a.ID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("cycle accepted: %v", err)
	}
	list, err := store.Dependencies(c.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("cycle rolled back? %+v %v", list, err)
	}
	if _, err = store.ReplaceDependencies(c.ID, c.Revision, []string{c.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self-loop accepted %v", err)
	}
	if _, err = store.ReplaceDependencies(c.ID, c.Revision, []string{a.ID, a.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate accepted %v", err)
	}
	if _, err = store.ReplaceDependencies(a.ID, changed.Revision, []string{""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty id accepted %v", err)
	}
	other, err := store.CreateProject(Project{Name: "another", RepoPath: testRepo(t)})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateTask(Task{ProjectID: other.ID, Title: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceDependencies(c.ID, c.Revision, []string{foreign.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-project source accepted %v", err)
	}
	if err = store.DeleteTask(b.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("referenced task deleted %v", err)
	}
	if _, err = store.Dependencies("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing list accepted %v", err)
	}
}

func TestDependencyRequiresTestedIntegratedGitCommit(t *testing.T) {
	store, runner, a := setupRunner(t, "complete")
	b := newGraphTask(t, store, a.ProjectID, "downstream")
	var err error
	b, err = store.ReplaceDependencies(b.ID, b.Revision, []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	b = readyGraphTask(t, store, b)
	if result, err := store.TaskReadiness(b.ID); err != nil || result.Ready {
		t.Fatalf("prereq not done but accepted %+v %v", result, err)
	}
	if _, err = runner.Start(b.ID, b.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("direct Start bypassed dependency %v", err)
	}
	aWT, err := store.worktreeForTask(a)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(aWT, "new-api.txt"), []byte("new API"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", aWT, "add", "new-api.txt"},
		{"-C", aWT, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "feature a"},
	} {
		if out, e := proc.Command("git", args...).CombinedOutput(); e != nil {
			t.Fatalf("git commit %v: %v %s", args, e, out)
		}
	}
	cp, err := store.CreateCheckpoint(CheckpointInput{TaskID: a.ID, Revision: a.Revision, Summary: "Feature A verified", TestStatus: "passed"})
	if err != nil {
		t.Fatal(err)
	}
	if cp.Dirty {
		t.Fatal("checkpoint dirty")
	}
	a = finishGraphTask(t, store, a)
	if result, err := store.TaskReadiness(b.ID); err != nil || result.Ready {
		t.Fatalf("missing ancestor accepted %+v %v", result, err)
	}
	bWT, err := store.worktreeForTask(b)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := proc.Command("git", "-C", bWT, "merge", "--ff-only", cp.GitHead).CombinedOutput(); err != nil {
		t.Fatalf("integrate prerequisite: %v %s", err, out)
	}
	if result, err := store.TaskReadiness(b.ID); err != nil || !result.Ready {
		t.Fatalf("integration not recognized %+v %v", result, err)
	}
	if err = os.WriteFile(filepath.Join(aWT, "unreviewed.txt"), []byte("unreviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := store.TaskReadiness(b.ID); err != nil || result.Ready {
		t.Fatalf("new unreviewed changes accepted %+v %v", result, err)
	}
	if err = os.Remove(filepath.Join(aWT, "unreviewed.txt")); err != nil {
		t.Fatal(err)
	}
	started, err := runner.Start(b.ID, b.Revision)
	if err != nil {
		t.Fatal(err)
	}
	runner.wait.Wait()
	if started.TaskID != b.ID {
		t.Fatalf("incorrect task launched: %+v", started)
	}
}

func TestDependencyRequiresPassingCleanCheckpoint(t *testing.T) {
	store, _, a := setupRunner(t, "complete")
	b := newGraphTask(t, store, a.ProjectID, "dependent")
	var err error
	b, err = store.ReplaceDependencies(b.ID, b.Revision, []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	b = readyGraphTask(t, store, b)
	if _, err = store.CreateCheckpoint(CheckpointInput{TaskID: a.ID, Revision: a.Revision, Summary: "tests not run", TestStatus: "not_run"}); err != nil {
		t.Fatal(err)
	}
	a = finishGraphTask(t, store, a)
	if r, err := store.TaskReadiness(b.ID); err != nil || r.Ready {
		t.Fatalf("untested source accepted %+v %v", r, err)
	}
}

func TestDependencyRejectsApprovedTaskWithUnresolvedRun(t *testing.T) {
	store, _, a := setupRunner(t, "complete")
	b := newGraphTask(t, store, a.ProjectID, "downstream pending review")
	var err error
	b, err = store.ReplaceDependencies(b.ID, b.Revision, []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	b = readyGraphTask(t, store, b)
	if _, err = store.CreateCheckpoint(CheckpointInput{TaskID: a.ID, Revision: a.Revision, Summary: "Human testing done", TestStatus: "passed"}); err != nil {
		t.Fatal(err)
	}
	cwd, err := store.worktreeForTask(a)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.reserveRun(a, a.Revision, cwd)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.taskByID(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err = store.SetTaskStatus(a.ID, current.Revision, "review")
	if err != nil {
		t.Fatal(err)
	}
	current, err = store.SetTaskStatus(a.ID, current.Revision, "done")
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "done" {
		t.Fatal("test setup did not mark done")
	}
	readiness, err := store.TaskReadiness(b.ID)
	if err != nil || readiness.Ready || !strings.Contains(readiness.Reason, "active or unresolved") {
		t.Fatalf("unfinished ancestor run was ignored: %+v %v", readiness, err)
	}
	if reserved.State != "starting" {
		t.Fatalf("wrong fixture: %+v", reserved)
	}
}
