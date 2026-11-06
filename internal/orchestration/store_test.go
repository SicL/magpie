package orchestration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/proc"
)

func git(t *testing.T, args ...string) {
	t.Helper()
	cmd := proc.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func testRepo(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repository")
	git(t, "init", "-b", "main", path)
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test repository\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, "-C", path, "add", "README.md")
	git(t, "-C", path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial")
	return path
}

func TestProjectsPersistAndProtectRepository(t *testing.T) {
	repo := testRepo(t)
	db := filepath.Join(t.TempDir(), "tasks.db")
	store, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(Project{Name: "Test", RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if project.BaseBranch != "main" {
		t.Fatalf("branch = %q", project.BaseBranch)
	}
	if _, err = store.CreateProject(Project{Name: "Duplicate", RepoPath: repo}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err = store.CreateProject(Project{Name: "Nested", RepoPath: filepath.Join(repo, ".git")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nested: %v", err)
	}
	if _, err = store.CreateProject(Project{Name: "Unsafe", RepoPath: repo, WorktreeRoot: filepath.Join(repo, "wt")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("worktree inside repo: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	items, err := store.Projects()
	if err != nil || len(items) != 1 || items[0].ID != project.ID {
		t.Fatalf("reopened projects: %+v %v", items, err)
	}
}

func TestTaskStatusCompareAndSet(t *testing.T) {
	repo := testRepo(t)
	store, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(Project{Name: "Work", RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateTask(Task{ProjectID: "missing", Title: "bad"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing parent: %v", err)
	}
	task, err := store.CreateTask(Task{ProjectID: project.ID, Title: "Plan", Agent: "WorkBuddy"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "pending" || task.Revision != 1 {
		t.Fatalf("created: %+v", task)
	}
	if _, err = store.SetTaskStatus(task.ID, task.Revision, "done"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("illegal status: %v", err)
	}
	ready, err := store.SetTaskStatus(task.ID, task.Revision, "ready")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetTaskStatus(task.ID, task.Revision, "running"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	if err = store.DeleteTask(task.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete ready: %v", err)
	}
	running, err := store.SetTaskStatus(task.ID, ready.Revision, "running")
	if err != nil {
		t.Fatal(err)
	}
	review, err := store.SetTaskStatus(task.ID, running.Revision, "review")
	if err != nil {
		t.Fatal(err)
	}
	done, err := store.SetTaskStatus(task.ID, review.Revision, "done")
	if err != nil {
		t.Fatal(err)
	}
	if done.Revision != 5 {
		t.Fatalf("revision %d", done.Revision)
	}
	if err = store.DeleteProject(project.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete project with task: %v", err)
	}
	if err = store.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.Tasks(project.ID)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("remaining tasks %+v %v", tasks, err)
	}
}

func TestProjectWorktreeRootSymlinkAliasesAreCanonicalAndSafe(t *testing.T) {
	repo := testRepo(t)
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	alias := filepath.Join(t.TempDir(), "link-to-repo-parent")
	if err := os.Symlink(filepath.Dir(repo), alias); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	dangerous := filepath.Join(alias, filepath.Base(repo), "nested-worktrees")
	if _, err = db.CreateProject(Project{Name: "symlink escape", RepoPath: repo, WorktreeRoot: dangerous}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("indirect repository Worktree root allowed: %v", err)
	}
	safeAlias := filepath.Join(alias, "sibling-worktrees")
	expected, err := prospectivePath(safeAlias)
	if err != nil {
		t.Fatal(err)
	}
	created, err := db.CreateProject(Project{Name: "canonical sibling", RepoPath: repo, WorktreeRoot: safeAlias})
	if err != nil {
		t.Fatalf("safe symlink sibling rejected: %v", err)
	}
	if created.WorktreeRoot != expected {
		t.Fatalf("worktree registration is not canonical: got %q want %q", created.WorktreeRoot, expected)
	}
	if _, err = os.Stat(safeAlias); !os.IsNotExist(err) {
		t.Fatalf("project creation unexpectedly wrote a Worktree root: %v", err)
	}
}
