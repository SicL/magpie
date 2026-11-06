package orchestration

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Dependency is a project-local prerequisite. A dependent task cannot
// launch while the prerequisite's tested code is absent from its Worktree.
type Dependency struct {
	TaskID         string `json:"taskId"`
	PrerequisiteID string `json:"prerequisiteId"`
}

func (s *Store) ensureDependencySchema() error {
	for _, q := range []string{
		"CREATE TABLE IF NOT EXISTS task_dependencies (task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT, prerequisite_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT, PRIMARY KEY(task_id,prerequisite_id),CHECK(task_id<>prerequisite_id))",
		"CREATE INDEX IF NOT EXISTS task_dependencies_by_source ON task_dependencies(prerequisite_id)",
	} {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("initialize dependencies: %w", err)
		}
	}
	return nil
}

func (s *Store) Dependencies(taskID string) ([]Dependency, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, ErrInvalid
	}
	var check int
	if err := s.db.QueryRow("SELECT 1 FROM tasks WHERE id=?", taskID).Scan(&check); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT task_id,prerequisite_id FROM task_dependencies WHERE task_id=? ORDER BY prerequisite_id", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Dependency{}
	for rows.Next() {
		var d Dependency
		if err = rows.Scan(&d.TaskID, &d.PrerequisiteID); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *Store) ProjectDependencies(projectID string) ([]Dependency, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, ErrInvalid
	}
	rows, err := s.db.Query("SELECT d.task_id,d.prerequisite_id FROM task_dependencies d JOIN tasks t ON t.id=d.task_id WHERE t.project_id=? ORDER BY d.task_id,d.prerequisite_id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dependency{}
	for rows.Next() {
		var d Dependency
		if err = rows.Scan(&d.TaskID, &d.PrerequisiteID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ReplaceDependencies uses task revision CAS and one transaction. Changes
// are forbidden while active, in review, or already marked done.
// The recursive SQL uses UNION (not UNION ALL) so even corrupt cyclic
// historical data cannot make the check loop indefinitely.
func (s *Store) ReplaceDependencies(taskID string, revision int64, inputs []string) (Task, error) {
	if revision <= 0 || len(inputs) > 32 {
		return Task{}, ErrInvalid
	}
	seen := make(map[string]bool, len(inputs))
	for _, id := range inputs {
		if id == "" || id == taskID || seen[id] {
			return Task{}, fmt.Errorf("%w: duplicate, missing or self dependency", ErrInvalid)
		}
		seen[id] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	var t Task
	err = tx.QueryRow("SELECT id,project_id,title,details,agent,status,revision,created_at,updated_at FROM tasks WHERE id=?", taskID).
		Scan(&t.ID, &t.ProjectID, &t.Title, &t.Details, &t.Agent, &t.Status, &t.Revision, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	if t.Revision != revision {
		return Task{}, ErrConflict
	}
	switch t.Status {
	case "pending", "blocked", "failed", "ready":
	default:
		return Task{}, fmt.Errorf("%w: dependencies cannot change during an active or completed task", ErrConflict)
	}
	var active int
	if err = tx.QueryRow("SELECT COUNT(*) FROM agent_runs WHERE task_id=? AND state IN ('starting','running','stopping','interrupted')", taskID).Scan(&active); err != nil {
		return Task{}, err
	}
	if active > 0 {
		return Task{}, fmt.Errorf("%w: cannot edit dependencies while an Agent Run remains active or unresolved", ErrConflict)
	}
	for _, id := range inputs {
		var targetProject string
		if err = tx.QueryRow("SELECT project_id FROM tasks WHERE id=?", id).Scan(&targetProject); errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		} else if err != nil {
			return Task{}, err
		}
		if targetProject != t.ProjectID {
			return Task{}, fmt.Errorf("%w: prerequisite must belong to the same project", ErrInvalid)
		}
	}
	if _, err = tx.Exec("DELETE FROM task_dependencies WHERE task_id=?", taskID); err != nil {
		return Task{}, err
	}
	for _, id := range inputs {
		if _, err = tx.Exec("INSERT INTO task_dependencies(task_id,prerequisite_id) VALUES(?,?)", taskID, id); err != nil {
			return Task{}, err
		}
	}
	var cycle int
	// Walk task -> prerequisite edges: reaching the same task is a cycle.
	err = tx.QueryRow(`WITH RECURSIVE reach(id) AS (
 SELECT prerequisite_id FROM task_dependencies WHERE task_id=?
 UNION
 SELECT d.prerequisite_id FROM task_dependencies d JOIN reach r ON d.task_id=r.id
 ) SELECT COUNT(*) FROM reach WHERE id=?`, taskID, taskID).Scan(&cycle)
	if err != nil {
		return Task{}, err
	}
	if cycle > 0 {
		return Task{}, fmt.Errorf("%w: prerequisite cycle detected", ErrConflict)
	}
	stamp := now()
	res, err := tx.Exec("UPDATE tasks SET revision=revision+1,updated_at=? WHERE id=? AND revision=?", stamp, taskID, revision)
	if err != nil {
		return Task{}, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Task{}, err
	}
	if changed != 1 {
		return Task{}, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return Task{}, err
	}
	t.Revision++
	t.UpdatedAt = stamp
	return t, nil
}
