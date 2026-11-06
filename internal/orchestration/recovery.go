package orchestration

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/yetone/magpie/internal/proc"
)

type RecoveryInput struct {
	Revision int64  `json:"revision"`
	Verified bool   `json:"verified"`
	Reason   string `json:"reason"`
}

// Recovery is evidence of the explicit operator decision. It is never
// an automatic proof that a process tree or Git changes are safe.
type Recovery struct {
	ID          string `json:"id"`
	RunID       string `json:"runId"`
	TaskID      string `json:"taskId"`
	Reason      string `json:"reason"`
	PreviousPID int    `json:"previousPid"`
	GitHead     string `json:"gitHead"`
	Dirty       bool   `json:"dirty"`
	CreatedAt   string `json:"createdAt"`
}

func (s *Store) ensureRecoverySchema() error {
	_, err := s.db.Exec("CREATE TABLE IF NOT EXISTS agent_recoveries (id TEXT PRIMARY KEY,run_id TEXT NOT NULL UNIQUE REFERENCES agent_runs(id),task_id TEXT NOT NULL REFERENCES tasks(id),reason TEXT NOT NULL,previous_pid INTEGER NOT NULL,git_head TEXT NOT NULL,dirty INTEGER NOT NULL,created_at TEXT NOT NULL)")
	return err
}

func (s *Store) Recoveries(taskID string) ([]Recovery, error) {
	if taskID == "" {
		return nil, ErrInvalid
	}
	rows, err := s.db.Query("SELECT id,run_id,task_id,reason,previous_pid,git_head,dirty,created_at FROM agent_recoveries WHERE task_id=? ORDER BY created_at DESC", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recovery{}
	for rows.Next() {
		var rec Recovery
		if err = rows.Scan(&rec.ID, &rec.RunID, &rec.TaskID, &rec.Reason, &rec.PreviousPID, &rec.GitHead, &rec.Dirty, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var found int
	if err = s.db.QueryRow("SELECT 1 FROM tasks WHERE id=?", taskID).Scan(&found); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return out, err
}

// recoverRun is called only after a process liveness check and an explicit
// human attestation. It unlocks a crashed Run but leaves task BLOCKED,
// requiring a separate manual ready transition before any restart.
func (s *Store) recoverRun(old Run, revision int64, reason string) (Recovery, error) {
	t, err := s.taskByID(old.TaskID)
	if err != nil {
		return Recovery{}, err
	}
	if t.Status != "running" || t.Revision != revision {
		return Recovery{}, ErrConflict
	}
	worktree, err := s.worktreeForTask(t)
	if err != nil {
		return Recovery{}, err
	}
	if worktree != old.WorktreePath {
		return Recovery{}, fmt.Errorf("%w: Worktree differs from previous Run", ErrConflict)
	}
	head, err := proc.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		return Recovery{}, err
	}
	dirty, err := proc.Command("git", "-C", worktree, "status", "--porcelain=v1", "--untracked-files=normal").Output()
	if err != nil {
		return Recovery{}, err
	}
	id, err := newID()
	if err != nil {
		return Recovery{}, err
	}
	item := Recovery{ID: id, RunID: old.ID, TaskID: t.ID, Reason: reason, PreviousPID: old.PID, GitHead: strings.TrimSpace(string(head)), Dirty: len(dirty) > 0, CreatedAt: now()}
	tx, err := s.db.Begin()
	if err != nil {
		return Recovery{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec("UPDATE agent_runs SET state='reconciled',finished_at=?,failure=? WHERE id=? AND state='interrupted' AND pid=?", item.CreatedAt, "Manually reconciled: "+reason, old.ID, old.PID)
	if err != nil {
		return Recovery{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Recovery{}, err
	}
	if n != 1 {
		return Recovery{}, ErrConflict
	}
	res, err = tx.Exec("UPDATE tasks SET status='blocked',revision=revision+1,updated_at=? WHERE id=? AND status='running' AND revision=?", item.CreatedAt, t.ID, revision)
	if err != nil {
		return Recovery{}, err
	}
	n, err = res.RowsAffected()
	if err != nil {
		return Recovery{}, err
	}
	if n != 1 {
		return Recovery{}, ErrConflict
	}
	_, err = tx.Exec("INSERT INTO agent_recoveries VALUES(?,?,?,?,?,?,?,?)", item.ID, item.RunID, item.TaskID, item.Reason, item.PreviousPID, item.GitHead, item.Dirty, item.CreatedAt)
	if err != nil {
		return Recovery{}, err
	}
	if err = tx.Commit(); err != nil {
		return Recovery{}, err
	}
	return item, nil
}
