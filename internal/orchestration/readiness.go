package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// Readiness is an on-demand snapshot, not a lease. Start() rechecks it
// immediately before reserving the process's durable Run record.
type Readiness struct {
	TaskID        string `json:"taskId"`
	Ready         bool   `json:"ready"`
	Reason        string `json:"reason,omitempty"`
	Prerequisites int    `json:"prerequisites"`
}

func (s *Store) TaskReadiness(taskID string) (Readiness, error) {
	t, err := s.taskByID(taskID)
	if err != nil {
		return Readiness{}, err
	}
	result := Readiness{TaskID: taskID}
	deps, err := s.Dependencies(taskID)
	if err != nil {
		return Readiness{}, err
	}
	result.Prerequisites = len(deps)
	var activeCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_runs WHERE task_id=? AND state IN ('starting','running','stopping','interrupted')", taskID).Scan(&activeCount); err != nil {
		return Readiness{}, err
	}
	if activeCount > 0 {
		result.Reason = "task has an active or unresolved Agent Run"
		return result, nil
	}
	if t.Status != "ready" {
		result.Reason = "task is not in ready state"
		return result, nil
	}
	cwd, err := s.worktreeForTask(t)
	if err != nil {
		result.Reason = "isolated task Worktree is unavailable or invalid"
		return result, nil
	}
	if err := s.checkPrerequisites(t, cwd, deps); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.Ready = true
	return result, nil
}

func (s *Store) checkPrerequisites(t Task, worktree string, deps []Dependency) error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	for _, dep := range deps {
		prior, err := s.taskByID(dep.PrerequisiteID)
		if err != nil {
			return err
		}
		var unfinished int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_runs WHERE task_id=? AND state IN ('starting','running','stopping','interrupted')", prior.ID).Scan(&unfinished); err != nil {
			return err
		}
		if unfinished > 0 {
			return fmt.Errorf("%w: prerequisite %s has an active or unresolved Agent Run", ErrConflict, prior.Title)
		}
		if prior.ProjectID != t.ProjectID || prior.Status != "done" {
			return fmt.Errorf("%w: prerequisite %s must be approved done in the same project", ErrConflict, prior.Title)
		}
		var head, status string
		var dirty int
		err = s.db.QueryRow("SELECT git_head,test_status,dirty FROM agent_checkpoints WHERE task_id=? ORDER BY created_at DESC,id DESC LIMIT 1", prior.ID).Scan(&head, &status, &dirty)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: prerequisite %s has no verification checkpoint", ErrConflict, prior.Title)
		}
		if err != nil {
			return err
		}
		if status != "passed" || dirty != 0 {
			return fmt.Errorf("%w: prerequisite %s needs a passed checkpoint from a clean Worktree", ErrConflict, prior.Title)
		}
		// The last tested commit must still match the prerequisite Worktree
		// to prevent later unreviewed edits from silently entering downstream.
		priorWT, err := s.worktreeForTask(prior)
		if err != nil {
			return err
		}
		current, err := proc.CommandContext(ctx, "git", "-C", priorWT, "rev-parse", "HEAD").Output()
		if err != nil {
			return fmt.Errorf("%w: prerequisite Git HEAD cannot be verified", ErrConflict)
		}
		if strings.TrimSpace(string(current)) != head {
			return fmt.Errorf("%w: prerequisite %s changed since its checkpoint", ErrConflict, prior.Title)
		}
		uncommitted, err := proc.CommandContext(ctx, "git", "-C", priorWT, "status", "--porcelain=v1", "--untracked-files=normal").Output()
		if err != nil {
			return fmt.Errorf("%w: prerequisite Git changes cannot be checked", ErrConflict)
		}
		if len(uncommitted) > 0 {
			return fmt.Errorf("%w: prerequisite %s has uncommitted changes", ErrConflict, prior.Title)
		}
		// Integration gate: downstream Worktree's own HEAD must contain the
		// prerequisite tested commit. This does not merge or cherry-pick code.
		err = proc.CommandContext(ctx, "git", "-C", worktree, "merge-base", "--is-ancestor", head, "HEAD").Run()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				return fmt.Errorf("%w: prerequisite %s commit is not integrated into this task Worktree", ErrConflict, prior.Title)
			}
			return fmt.Errorf("%w: could not verify prerequisite Git integration: %v", ErrConflict, err)
		}
	}
	return nil
}

func (s *Store) CheckDependenciesForLaunch(t Task, worktree string) error {
	deps, err := s.Dependencies(t.ID)
	if err != nil {
		return err
	}
	return s.checkPrerequisites(t, worktree, deps)
}
