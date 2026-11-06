package orchestration

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Run is a durable record of one explicitly started coding-agent process.
// A recorded state never implies that the agent's work passed validation.
type Run struct {
	ID           string `json:"id"`
	TaskID       string `json:"taskId"`
	Adapter      string `json:"adapter"`
	SessionID    string `json:"sessionId,omitempty"`
	ResumeOf     string `json:"resumeOf,omitempty"`
	State        string `json:"state"`
	PID          int    `json:"pid"`
	WorktreePath string `json:"worktreePath"`
	StartedAt    string `json:"startedAt"`
	FinishedAt   string `json:"finishedAt,omitempty"`
	ExitCode     *int   `json:"exitCode,omitempty"`
	Failure      string `json:"failure,omitempty"`
}

const adapterDSH = "deepseek-harness"

func (s *Store) ensureRunSchema() error {
	for _, q := range []string{
		"CREATE TABLE IF NOT EXISTS agent_runs (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT, adapter TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', resume_of TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, pid INTEGER NOT NULL DEFAULT 0, worktree_path TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '', exit_code INTEGER, failure TEXT NOT NULL DEFAULT '')",
		"CREATE INDEX IF NOT EXISTS agent_runs_task ON agent_runs(task_id,started_at)",
		"CREATE UNIQUE INDEX IF NOT EXISTS agent_runs_active ON agent_runs(task_id) WHERE state IN ('starting','running','stopping','interrupted')",
	} {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("initialize agent runs: %w", err)
		}
	}
	if err := s.ensureSessionColumn(); err != nil {
		return err
	}
	return s.ensureResumeColumn()
}

// An agent belonging to the previous process may still exist after a crash.
// Its lease stays occupied until someone verifies its process and resolves it.
// Do not silently assume the process died or launch a second writer.
func (s *Store) markPreviousRunsUncertain() error {
	_, err := s.db.Exec("UPDATE agent_runs SET state='interrupted',failure='Supervisor restarted; inspect previous process and worktree before retrying' WHERE state IN ('starting','running','stopping')")
	return err
}

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var code sql.NullInt64
	err := row.Scan(&r.ID, &r.TaskID, &r.Adapter, &r.SessionID, &r.ResumeOf, &r.State, &r.PID, &r.WorktreePath, &r.StartedAt, &r.FinishedAt, &code, &r.Failure)
	if err != nil {
		return Run{}, err
	}
	if code.Valid {
		n := int(code.Int64)
		r.ExitCode = &n
	}
	return r, nil
}

func (s *Store) Runs(taskID string) ([]Run, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("%w: task required", ErrInvalid)
	}
	rows, err := s.db.Query("SELECT id,task_id,adapter,session_id,resume_of,state,pid,worktree_path,started_at,finished_at,exit_code,failure FROM agent_runs WHERE task_id=? ORDER BY started_at DESC,id DESC", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Run(id string) (Run, error) {
	r, err := scanRun(s.db.QueryRow("SELECT id,task_id,adapter,session_id,resume_of,state,pid,worktree_path,started_at,finished_at,exit_code,failure FROM agent_runs WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

// reserveRun is a transaction: a task cannot become running without its
// durable run record, and two concurrent requests cannot claim one task.
func (s *Store) reserveRun(t Task, revision int64, worktree string, source ...string) (Run, error) {
	resumeOf := ""
	if len(source) > 0 {
		resumeOf = source[0]
	}
	return s.reserveRunAdapter(t, revision, worktree, adapterDSH, resumeOf)
}

func (s *Store) reserveRunAdapter(t Task, revision int64, worktree, adapter, resumeOf string) (Run, error) {
	if adapter != adapterDSH && adapter != "zcode-cli-plan" {
		return Run{}, ErrInvalid
	}
	id, err := newID()
	if err != nil {
		return Run{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec("UPDATE tasks SET status='running',revision=revision+1,updated_at=? WHERE id=? AND status='ready' AND revision=?", now(), t.ID, revision)
	if err != nil {
		return Run{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Run{}, err
	}
	if n != 1 {
		return Run{}, ErrConflict
	}
	r := Run{ID: id, TaskID: t.ID, Adapter: adapter, ResumeOf: resumeOf, State: "starting", WorktreePath: worktree, StartedAt: now()}
	_, err = tx.Exec("INSERT INTO agent_runs (id,task_id,adapter,resume_of,state,pid,worktree_path,started_at) VALUES(?,?,?,?,?,?,?,?)", r.ID, r.TaskID, r.Adapter, r.ResumeOf, r.State, 0, r.WorktreePath, r.StartedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Run{}, ErrConflict
		}
		return Run{}, err
	}
	if err = tx.Commit(); err != nil {
		return Run{}, err
	}
	return r, nil
}

func (s *Store) runStarted(id string, pid int) error {
	res, err := s.db.Exec("UPDATE agent_runs SET state='running',pid=? WHERE id=? AND state='starting'", pid, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) runStopping(id string) error {
	res, err := s.db.Exec("UPDATE agent_runs SET state='stopping' WHERE id=? AND state IN ('starting','running')", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) finishRun(id, kind string, exit int, failure string) error {
	if kind != "completed" && kind != "failed" && kind != "cancelled" {
		return ErrInvalid
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var taskID string
	if err = tx.QueryRow("SELECT task_id FROM agent_runs WHERE id=?", id).Scan(&taskID); err != nil {
		return err
	}
	stamp := now()
	res, err := tx.Exec("UPDATE agent_runs SET state=?,finished_at=?,exit_code=?,failure=? WHERE id=? AND state IN ('starting','running','stopping')", kind, stamp, exit, failure, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	next := "review"
	if kind == "failed" {
		next = "failed"
	} else if kind == "cancelled" {
		next = "blocked"
	}
	_, err = tx.Exec("UPDATE tasks SET status=?,revision=revision+1,updated_at=? WHERE id=? AND status='running'", next, stamp, taskID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ProjectRuns returns durable run metadata, never the underlying log content.
func (s *Store) ProjectRuns(projectID string) ([]Run, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project required", ErrInvalid)
	}
	rows, err := s.db.Query("SELECT r.id,r.task_id,r.adapter,r.session_id,r.resume_of,r.state,r.pid,r.worktree_path,r.started_at,r.finished_at,r.exit_code,r.failure FROM agent_runs r JOIN tasks t ON r.task_id=t.id WHERE t.project_id=? ORDER BY r.started_at DESC,r.id DESC", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		x, e := scanRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) setRunSession(id, session string) error {
	if session == "" {
		return ErrInvalid
	}
	_, err := s.db.Exec("UPDATE agent_runs SET session_id=? WHERE id=? AND session_id='' AND state IN ('starting','running','stopping')", session, id)
	return err
}

// Older local P3 development databases may predate sessionId capture.
// Extend them without replacing tasks, logs or existing Run records.
func (s *Store) ensureSessionColumn() error {
	rows, err := s.db.Query("PRAGMA table_info(agent_runs)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var position, notNull, pk int
		var name, kind string
		var def sql.NullString
		if err := rows.Scan(&position, &name, &kind, &notNull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "session_id" {
			found = true
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.db.Exec("ALTER TABLE agent_runs ADD COLUMN session_id TEXT NOT NULL DEFAULT ''")
	return err
}

// Migration is additive. P3 databases have session_id but not resume_of.
func (s *Store) ensureResumeColumn() error {
	rows, err := s.db.Query("PRAGMA table_info(agent_runs)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var position, required, pk int
		var name, kind string
		var def sql.NullString
		if err := rows.Scan(&position, &name, &kind, &required, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "resume_of" {
			found = true
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.db.Exec("ALTER TABLE agent_runs ADD COLUMN resume_of TEXT NOT NULL DEFAULT ''")
	return err
}

// resumeSource must be the latest terminal DSH run for this same task and
// exact Worktree. The caller supplies only a recorded Run ID, never an
// arbitrary session ID or foreign path as a CLI argument.
func (s *Store) resumeSource(taskID, sourceID, worktree string) (Run, error) {
	if sourceID == "" {
		return Run{}, fmt.Errorf("%w: resume run ID required", ErrInvalid)
	}
	src, err := s.Run(sourceID)
	if err != nil {
		return Run{}, err
	}
	if src.TaskID != taskID || src.Adapter != adapterDSH || src.WorktreePath != worktree ||
		(src.State != "completed" && src.State != "failed" && src.State != "cancelled") ||
		!sessionIDPattern.MatchString(src.SessionID) {
		return Run{}, fmt.Errorf("%w: source run cannot be resumed in this task", ErrConflict)
	}
	var latest string
	if err = s.db.QueryRow("SELECT id FROM agent_runs WHERE task_id=? ORDER BY started_at DESC,id DESC LIMIT 1", taskID).Scan(&latest); err != nil {
		return Run{}, err
	}
	if latest != src.ID {
		return Run{}, fmt.Errorf("%w: only the latest run can be resumed", ErrConflict)
	}
	return src, nil
}
