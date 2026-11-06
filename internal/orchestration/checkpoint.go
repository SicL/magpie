package orchestration

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/yetone/magpie/internal/proc"
)

// Checkpoint is a durable, human-authored handoff marker. It does not
// claim that the Agent or its tests were successful unless a human records it.
type Checkpoint struct {
	ID         string `json:"id"`
	TaskID     string `json:"taskId"`
	RunID      string `json:"runId,omitempty"`
	Summary    string `json:"summary"`
	TestStatus string `json:"testStatus"`
	GitHead    string `json:"gitHead"`
	Dirty      bool   `json:"dirty"`
	CreatedAt  string `json:"createdAt"`
}

type CheckpointInput struct {
	TaskID     string `json:"taskId"`
	Revision   int64  `json:"revision"`
	RunID      string `json:"runId,omitempty"`
	Summary    string `json:"summary"`
	TestStatus string `json:"testStatus"`
}

func (s *Store) ensureCheckpointSchema() error {
	_, err := s.db.Exec("CREATE TABLE IF NOT EXISTS agent_checkpoints (id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,run_id TEXT NOT NULL DEFAULT '',summary TEXT NOT NULL,test_status TEXT NOT NULL,git_head TEXT NOT NULL,dirty INTEGER NOT NULL,created_at TEXT NOT NULL)")
	if err != nil {
		return err
	}
	_, err = s.db.Exec("CREATE INDEX IF NOT EXISTS checkpoints_by_task ON agent_checkpoints(task_id,created_at)")
	return err
}

func (s *Store) CreateCheckpoint(in CheckpointInput) (Checkpoint, error) {
	in.Summary = strings.TrimSpace(in.Summary)
	if len(in.Summary) == 0 || len(in.Summary) > 4000 || in.Revision <= 0 {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint summary and revision are required", ErrInvalid)
	}
	if in.TestStatus != "not_run" && in.TestStatus != "passed" && in.TestStatus != "failed" {
		return Checkpoint{}, fmt.Errorf("%w: invalid test result", ErrInvalid)
	}
	task, err := s.taskByID(in.TaskID)
	if err != nil {
		return Checkpoint{}, err
	}
	if task.Revision != in.Revision {
		return Checkpoint{}, ErrConflict
	}
	worktree, err := s.worktreeForTask(task)
	if err != nil {
		return Checkpoint{}, err
	}
	if in.RunID != "" {
		run, err := s.Run(in.RunID)
		if err != nil {
			return Checkpoint{}, err
		}
		if run.TaskID != task.ID || run.WorktreePath != worktree {
			return Checkpoint{}, fmt.Errorf("%w: checkpoint run belongs to another task/worktree", ErrInvalid)
		}
	}
	head, err := proc.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read worktree HEAD: %w", err)
	}
	status, err := proc.Command("git", "-C", worktree, "status", "--porcelain=v1", "--untracked-files=normal").Output()
	if err != nil {
		return Checkpoint{}, fmt.Errorf("inspect worktree state: %w", err)
	}
	id, err := newID()
	if err != nil {
		return Checkpoint{}, err
	}
	c := Checkpoint{ID: id, TaskID: task.ID, RunID: in.RunID, Summary: in.Summary, TestStatus: in.TestStatus, GitHead: strings.TrimSpace(string(head)), Dirty: len(status) > 0, CreatedAt: now()}
	// A revision change during the Git inspection must not turn a stale
	// snapshot into a later task revision's checkpoint.
	res, err := s.db.Exec("INSERT INTO agent_checkpoints (id,task_id,run_id,summary,test_status,git_head,dirty,created_at) SELECT ?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM tasks WHERE id=? AND revision=?)", c.ID, c.TaskID, c.RunID, c.Summary, c.TestStatus, c.GitHead, c.Dirty, c.CreatedAt, c.TaskID, in.Revision)
	if err != nil {
		return Checkpoint{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Checkpoint{}, err
	}
	if n != 1 {
		return Checkpoint{}, ErrConflict
	}
	return c, nil
}

func (s *Store) Checkpoints(taskID string) ([]Checkpoint, error) {
	if taskID == "" {
		return nil, ErrInvalid
	}
	rows, err := s.db.Query("SELECT id,task_id,run_id,summary,test_status,git_head,dirty,created_at FROM agent_checkpoints WHERE task_id=? ORDER BY created_at DESC,id DESC", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Checkpoint{}
	for rows.Next() {
		var c Checkpoint
		if err := rows.Scan(&c.ID, &c.TaskID, &c.RunID, &c.Summary, &c.TestStatus, &c.GitHead, &c.Dirty, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// A nonexistent task is not an empty list.
	var exists int
	err = s.db.QueryRow("SELECT 1 FROM tasks WHERE id=?", taskID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}
