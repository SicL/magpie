// Package orchestration maintains project and task state separately from
// Magpie's model gateway and the coding agents' own configuration.
package orchestration

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
	_ "github.com/yetone/magpie/internal/sqlitedb"
)

var (
	ErrInvalid  = errors.New("invalid input")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflicting change")
)

type Project struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RepoPath     string `json:"repoPath"`
	BaseBranch   string `json:"baseBranch"`
	WorktreeRoot string `json:"worktreeRoot"`
	CreatedAt    string `json:"createdAt"`
}

type Task struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Title     string `json:"title"`
	Details   string `json:"details"`
	Agent     string `json:"agent"`
	Status    string `json:"status"`
	Revision  int64  `json:"revision"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type Store struct {
	db  *sql.DB
	dir string
}

func Open(file string) (*Store, error) {
	if file == "" {
		return nil, fmt.Errorf("%w: empty DB path", ErrInvalid)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"CREATE TABLE IF NOT EXISTS projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, repo_path TEXT NOT NULL UNIQUE, base_branch TEXT NOT NULL, worktree_root TEXT NOT NULL, created_at TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT, title TEXT NOT NULL, details TEXT NOT NULL, agent TEXT NOT NULL, status TEXT NOT NULL, revision INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)",
		"CREATE INDEX IF NOT EXISTS tasks_by_project ON tasks(project_id,created_at)",
	} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize orchestration: %w", err)
		}
	}
	s := &Store{db: db, dir: filepath.Dir(file)}
	if err := s.ensureRunSchema(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.ensureRecoverySchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize recoveries: %w", err)
	}
	if err := s.ensureDependencySchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize dependency graph: %w", err)
	}
	if err := s.ensureCheckpointSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize checkpoints: %w", err)
	}
	if err := s.markPreviousRunsUncertain(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func now() string             { return time.Now().UTC().Format(time.RFC3339Nano) }
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// validateGitRoot never writes to the repository. All commands use args,
// never a shell, and reject a path inside someone else's repository.
func validateGitRoot(p, branch string) (string, string, error) {
	if strings.TrimSpace(p) == "" {
		return "", "", fmt.Errorf("%w: missing Git repository", ErrInvalid)
	}
	root, err := filepath.Abs(p)
	if err != nil {
		return "", "", fmt.Errorf("%w: bad repository path", ErrInvalid)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("%w: repository is inaccessible", ErrInvalid)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("%w: missing repository directory", ErrInvalid)
	}
	out, err := proc.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", fmt.Errorf("%w: not a Git repository", ErrInvalid)
	}
	top := filepath.Clean(strings.TrimSpace(string(out)))
	if !strings.EqualFold(top, filepath.Clean(root)) {
		return "", "", fmt.Errorf("%w: select the Git repository root", ErrInvalid)
	}
	if branch == "" {
		out, err = proc.Command("git", "-C", root, "symbolic-ref", "--short", "HEAD").Output()
		if err != nil {
			return "", "", fmt.Errorf("%w: repository has no current branch", ErrInvalid)
		}
		branch = strings.TrimSpace(string(out))
	}
	if proc.Command("git", "check-ref-format", "--branch", branch).Run() != nil ||
		proc.Command("git", "-C", root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run() != nil {
		return "", "", fmt.Errorf("%w: base branch is invalid or absent", ErrInvalid)
	}
	return root, branch, nil
}

func (s *Store) CreateProject(input Project) (Project, error) {
	name := strings.TrimSpace(input.Name)
	if len(name) == 0 || len(name) > 120 {
		return Project{}, fmt.Errorf("%w: name length", ErrInvalid)
	}
	repo, branch, err := validateGitRoot(input.RepoPath, strings.TrimSpace(input.BaseBranch))
	if err != nil {
		return Project{}, err
	}
	worktree := strings.TrimSpace(input.WorktreeRoot)
	if worktree == "" {
		worktree = filepath.Join(filepath.Dir(repo), ".agent-worktrees", filepath.Base(repo))
	}
	worktree, err = filepath.Abs(worktree)
	if err != nil {
		return Project{}, fmt.Errorf("%w: worktree directory", ErrInvalid)
	}
	rel, err := filepath.Rel(repo, worktree)
	if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return Project{}, fmt.Errorf("%w: worktree root is inside repository", ErrInvalid)
	}
	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	p := Project{ID: id, Name: name, RepoPath: repo, BaseBranch: branch, WorktreeRoot: worktree, CreatedAt: now()}
	_, err = s.db.Exec("INSERT INTO projects VALUES(?,?,?,?,?,?)", p.ID, p.Name, p.RepoPath, p.BaseBranch, p.WorktreeRoot, p.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Project{}, fmt.Errorf("%w: duplicate repository", ErrConflict)
		}
		return Project{}, err
	}
	return p, nil
}

func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query("SELECT id,name,repo_path,base_branch,worktree_root,created_at FROM projects ORDER BY created_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		if err = rows.Scan(&p.ID, &p.Name, &p.RepoPath, &p.BaseBranch, &p.WorktreeRoot, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProject(id string) error {
	res, err := s.db.Exec("DELETE FROM projects WHERE id=? AND NOT EXISTS (SELECT 1 FROM tasks WHERE project_id=?)", id, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: project not found or has tasks", ErrConflict)
	}
	return nil
}

func (s *Store) CreateTask(input Task) (Task, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Agent = strings.TrimSpace(input.Agent)
	if input.Title == "" || len(input.Title) > 200 || len(input.Details) > 20000 || len(input.Agent) > 100 || input.ProjectID == "" {
		return Task{}, fmt.Errorf("%w: invalid task fields", ErrInvalid)
	}
	id, err := newID()
	if err != nil {
		return Task{}, err
	}
	t := Task{ID: id, ProjectID: input.ProjectID, Title: input.Title, Details: input.Details, Agent: input.Agent, Status: "pending", Revision: 1, CreatedAt: now(), UpdatedAt: now()}
	res, err := s.db.Exec("INSERT INTO tasks SELECT ?,?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM projects WHERE id=?)",
		t.ID, t.ProjectID, t.Title, t.Details, t.Agent, t.Status, t.Revision, t.CreatedAt, t.UpdatedAt, t.ProjectID)
	if err != nil {
		return Task{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Task{}, err
	}
	if n != 1 {
		return Task{}, ErrNotFound
	}
	return t, nil
}

func (s *Store) Tasks(projectID string) ([]Task, error) {
	rows, err := s.db.Query("SELECT id,project_id,title,details,agent,status,revision,created_at,updated_at FROM tasks WHERE project_id=? ORDER BY created_at,id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		if err = rows.Scan(&t.ID, &t.ProjectID, &t.Title, &t.Details, &t.Agent, &t.Status, &t.Revision, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

var transitions = map[string]map[string]bool{
	"pending": {"ready": true, "blocked": true},
	"ready":   {"running": true, "blocked": true},
	"running": {"review": true, "failed": true, "blocked": true},
	"blocked": {"ready": true},
	"failed":  {"ready": true},
	"review":  {"done": true, "ready": true},
	"done":    {},
}

// A compare-and-set prevents two coordinators from silently overwriting a
// task's status. "running" is manual until worker leases ship in P2.
func (s *Store) SetTaskStatus(id string, revision int64, status string) (Task, error) {
	var t Task
	err := s.db.QueryRow("SELECT id,project_id,title,details,agent,status,revision,created_at,updated_at FROM tasks WHERE id=?", id).
		Scan(&t.ID, &t.ProjectID, &t.Title, &t.Details, &t.Agent, &t.Status, &t.Revision, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	if !transitions[t.Status][status] {
		return Task{}, fmt.Errorf("%w: transition %s to %s", ErrInvalid, t.Status, status)
	}
	stamp := now()
	res, err := s.db.Exec("UPDATE tasks SET status=?,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND status=?", status, stamp, id, revision, t.Status)
	if err != nil {
		return Task{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Task{}, err
	}
	if n != 1 {
		return Task{}, ErrConflict
	}
	t.Status, t.Revision, t.UpdatedAt = status, revision+1, stamp
	return t, nil
}

func (s *Store) DeleteTask(id string) error {
	t, err := s.taskByID(id)
	if err != nil {
		return err
	}
	if len(t.ID) < 16 {
		return fmt.Errorf("%w: task identifier", ErrInvalid)
	}
	worktrees, err := s.Worktrees(t.ProjectID)
	if err != nil {
		return err
	}
	for _, wt := range worktrees {
		if filepath.Base(wt.Path) == "task-"+t.ID[:16] {
			return fmt.Errorf("%w: task owns a Git worktree; it must be reviewed before removing metadata", ErrConflict)
		}
	}
	var deps int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM task_dependencies WHERE task_id=? OR prerequisite_id=?", id, id).Scan(&deps); err != nil {
		return err
	}
	if deps > 0 {
		return fmt.Errorf("%w: task is linked to prerequisite graph", ErrConflict)
	}
	var linked int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_runs WHERE task_id=?", id).Scan(&linked); err != nil {
		return err
	}
	if linked > 0 {
		return fmt.Errorf("%w: task has run history", ErrConflict)
	}
	var checkpoints int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_checkpoints WHERE task_id=?", id).Scan(&checkpoints); err != nil {
		return err
	}
	if checkpoints > 0 {
		return fmt.Errorf("%w: task has checkpoints", ErrConflict)
	}
	res, err := s.db.Exec("DELETE FROM tasks WHERE id=? AND status IN ('pending','blocked','failed','done')", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: task missing or active", ErrConflict)
	}
	return nil
}
