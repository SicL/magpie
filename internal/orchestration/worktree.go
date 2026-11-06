package orchestration

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yetone/magpie/internal/proc"
)

// Worktree is a Git-managed checkout, not an agent process. The platform
// never removes it automatically and never issues --force or reset --hard.
type Worktree struct {
	TaskID string `json:"taskId"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
}

func (s *Store) projectByID(id string) (Project, error) {
	var p Project
	err := s.db.QueryRow("SELECT id,name,repo_path,base_branch,worktree_root,created_at FROM projects WHERE id=?", id).
		Scan(&p.ID, &p.Name, &p.RepoPath, &p.BaseBranch, &p.WorktreeRoot, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

func (s *Store) taskByID(id string) (Task, error) {
	var t Task
	err := s.db.QueryRow("SELECT id,project_id,title,details,agent,status,revision,created_at,updated_at FROM tasks WHERE id=?", id).
		Scan(&t.ID, &t.ProjectID, &t.Title, &t.Details, &t.Agent, &t.Status, &t.Revision, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, err
}

// prospectivePath resolves the nearest existing ancestor without creating
// anything. A symlink outside the repository can otherwise redirect a
// future worktree directory into the repository before safety checks run.
func prospectivePath(path string) (string, error) {
	current, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	missing := []string{}
	for {
		_, statErr := os.Lstat(current)
		if statErr == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			// os.Lstat reports a directory symlink as ModeSymlink.
			// Check its resolved target, not the link itself, while
			// rejecting regular-file ancestors and broken links.
			if len(missing) > 0 {
				resolvedInfo, err := os.Stat(real)
				if err != nil || !resolvedInfo.IsDir() {
					return "", fmt.Errorf("%w: non-directory worktree ancestor", ErrInvalid)
				}
			}
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return filepath.Abs(real)
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("%w: cannot resolve worktree root", ErrInvalid)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func outsideRepo(repo, path string) bool {
	relative, err := filepath.Rel(repo, path)
	if err != nil {
		return false
	}
	return relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func checkedWorktreeRoot(p Project) (string, error) {
	repo, branch, err := validateGitRoot(p.RepoPath, p.BaseBranch)
	if err != nil {
		return "", err
	}
	if branch != p.BaseBranch || !strings.EqualFold(repo, p.RepoPath) {
		return "", fmt.Errorf("%w: Git repository changed after registration", ErrConflict)
	}
	target, err := prospectivePath(p.WorktreeRoot)
	if err != nil {
		return "", err
	}
	if !outsideRepo(repo, target) {
		return "", fmt.Errorf("%w: worktree root resolves inside repository", ErrInvalid)
	}
	if err = os.MkdirAll(p.WorktreeRoot, 0700); err != nil {
		return "", err
	}
	actual, err := filepath.EvalSymlinks(p.WorktreeRoot)
	if err != nil {
		return "", err
	}
	if !outsideRepo(repo, actual) {
		return "", fmt.Errorf("%w: worktree root changed during creation", ErrInvalid)
	}
	return actual, nil
}

func (s *Store) CreateWorktree(taskID string) (Worktree, error) {
	t, err := s.taskByID(taskID)
	if err != nil {
		return Worktree{}, err
	}
	if t.Status == "done" {
		return Worktree{}, fmt.Errorf("%w: completed task", ErrInvalid)
	}
	p, err := s.projectByID(t.ProjectID)
	if err != nil {
		return Worktree{}, err
	}
	root, err := checkedWorktreeRoot(p)
	if err != nil {
		return Worktree{}, err
	}
	// Task IDs are crypto-random, hex encoded and controlled by this database.
	if len(t.ID) != 32 {
		return Worktree{}, fmt.Errorf("%w: task id", ErrInvalid)
	}
	short := t.ID[:16]
	branch := "agent/task-" + short
	target := filepath.Join(root, "task-"+short)
	if _, err := os.Lstat(target); err == nil {
		return Worktree{}, fmt.Errorf("%w: worktree target already exists; inspect it before retrying", ErrConflict)
	} else if !os.IsNotExist(err) {
		return Worktree{}, err
	}
	// Git itself guards worktree branch/checkout collisions. No --force and
	// no cleanup on failure: a partially created checkout is left for review.
	out, err := proc.Command("git", "-C", p.RepoPath, "worktree", "add", "-b", branch, target, p.BaseBranch).CombinedOutput()
	if err != nil {
		return Worktree{}, fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	head, err := proc.Command("git", "-C", target, "rev-parse", "HEAD").Output()
	if err != nil {
		return Worktree{}, fmt.Errorf("worktree created but HEAD cannot be read: %w", err)
	}
	return Worktree{TaskID: t.ID, Path: target, Branch: branch, Head: strings.TrimSpace(string(head))}, nil
}

// Worktrees reads Git's own registry; it never trusts the database to mean
// a checkout was created. Only paths inside this project's dedicated root
// with this tool's branch prefix are shown.
func (s *Store) Worktrees(projectID string) ([]Worktree, error) {
	p, err := s.projectByID(projectID)
	if err != nil {
		return nil, err
	}
	// Viewing worktrees is read-only and must not create worktree directories.
	info, err := os.Stat(p.WorktreeRoot)
	if os.IsNotExist(err) {
		return []Worktree{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: worktree root is not directory", ErrInvalid)
	}
	root, err := filepath.EvalSymlinks(p.WorktreeRoot)
	if err != nil {
		return nil, err
	}
	data, err := proc.Command("git", "-C", p.RepoPath, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, err
	}
	out := []Worktree{}
	var path, branch, head string
	flush := func() {
		if path != "" && strings.HasPrefix(filepath.Base(path), "task-") {
			rel, e := filepath.Rel(root, path)
			if e == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
				out = append(out, Worktree{Path: path, Branch: strings.TrimPrefix(branch, "refs/heads/"), Head: head})
			}
		}
		path, branch, head = "", "", ""
	}
	for _, line := range strings.Split(string(data), string(rune(10))) {
		line = strings.TrimSuffix(line, string(rune(13)))
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			path = filepath.Clean(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			branch = strings.TrimPrefix(line, "branch ")
		case strings.HasPrefix(line, "HEAD "):
			head = strings.TrimPrefix(line, "HEAD ")
		}
	}
	flush()
	return out, nil
}
