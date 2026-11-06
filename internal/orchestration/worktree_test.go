package orchestration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/proc"
)

func TestCreateWorktreeWithoutChangingMainCheckout(t *testing.T) {
	repo := testRepo(t)
	store, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	worktreeRoot := filepath.Join(t.TempDir(), "isolated")
	project, err := store.CreateProject(Project{Name: "Multi agent", RepoPath: repo, WorktreeRoot: worktreeRoot})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(Task{ProjectID: project.ID, Title: "Change one module", Agent: "DeepSeek Harness"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := proc.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	wt, err := store.CreateWorktree(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Path == repo || !strings.HasPrefix(wt.Path, worktreeRoot) {
		t.Fatalf("not isolated: %+v", wt)
	}
	if !strings.HasPrefix(wt.Branch, "agent/task-") {
		t.Fatalf("branch %+v", wt)
	}
	if _, err = os.Stat(filepath.Join(wt.Path, "README.md")); err != nil {
		t.Fatal(err)
	}
	after, err := proc.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) || wt.Head != strings.TrimSpace(string(original)) {
		t.Fatalf("head changed: %s %s", original, after)
	}
	list, err := store.Worktrees(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Path != wt.Path || list[0].Branch != wt.Branch {
		t.Fatalf("worktrees %+v", list)
	}
	git(t, "-C", wt.Path, "checkout", "--detach", "HEAD")
	detached, err := store.Worktrees(project.ID)
	if err != nil || len(detached) != 1 {
		t.Fatalf("detached checkout lost from listing: %+v %v", detached, err)
	}
	if err = store.DeleteTask(task.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete task with worktree: %v", err)
	}
	if _, err = store.CreateWorktree(task.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate worktree: %v", err)
	}
}
