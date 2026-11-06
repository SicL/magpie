package orchestration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// HandoffEvidence is the last human-reported test result for an integrated
// predecessor. This is NOT independently executed CI proof.
type HandoffEvidence struct {
	TaskID        string `json:"taskId"`
	Agent         string `json:"agent"`
	CheckpointID  string `json:"checkpointId,omitempty"`
	Summary       string `json:"summary,omitempty"`
	TestedHead    string `json:"testedHead"`
	TestsReported string `json:"testsReported"`
}

type HandoffPreview struct {
	TaskID        string            `json:"taskId"`
	Revision      int64             `json:"revision"`
	Agent         string            `json:"agent"`
	WorktreePath  string            `json:"worktreePath,omitempty"`
	GitHead       string            `json:"gitHead,omitempty"`
	Ready         bool              `json:"ready"`
	Clean         bool              `json:"clean"`
	Reason        string            `json:"reason,omitempty"`
	Prerequisites []HandoffEvidence `json:"prerequisites"`
	Fingerprint   string            `json:"fingerprint"`
}

type HandoffReceipt struct {
	ID          string `json:"id"`
	TaskID      string `json:"taskId"`
	Revision    int64  `json:"revision"`
	Recipient   string `json:"recipient"`
	GitHead     string `json:"gitHead"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"createdAt"`
}

// A receipt means the local operator manually acknowledged this snapshot;
// it is NOT a cryptographic signature or automated Agent acceptance.
func (s *Store) ensureHandoffSchema() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_handoff_receipts(
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
  revision INTEGER NOT NULL,
  recipient TEXT NOT NULL,
  git_head TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(task_id,fingerprint,recipient)
 )`)
	return err
}

func canonicalRecipient(agent string) string {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "dsh", "deepseek harness":
		return "DeepSeek Harness"
	case "zcode", "zcode cli", "zcode-cli":
		return "ZCode"
	case "codebuddy", "codebuddy code", "codebuddy code cli":
		return "CodeBuddy Code"
	case "workbuddy", "workbuddy desktop":
		return "WorkBuddy Desktop"
	default:
		return ""
	}
}

func (s *Store) PreviewHandoff(taskID string) (HandoffPreview, error) {
	t, err := s.taskByID(taskID)
	if err != nil {
		return HandoffPreview{}, err
	}
	result := HandoffPreview{TaskID: t.ID, Revision: t.Revision, Agent: t.Agent, Prerequisites: []HandoffEvidence{}}
	readiness, err := s.TaskReadiness(taskID)
	if err != nil {
		return HandoffPreview{}, err
	}
	result.Ready = readiness.Ready
	result.Reason = readiness.Reason
	if canonicalRecipient(t.Agent) == "" {
		result.Ready = false
		result.Reason = "task has no supported assigned Agent"
	}
	cwd, err := s.worktreeForTask(t)
	if err != nil {
		result.Ready = false
		result.Reason = "isolated task Worktree is unavailable or invalid"
	} else {
		result.WorktreePath = cwd
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		head, e := proc.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "HEAD").Output()
		if e != nil {
			result.Ready = false
			result.Reason = "Git HEAD could not be verified"
		} else {
			result.GitHead = strings.TrimSpace(string(head))
		}
		diff, e := proc.CommandContext(ctx, "git", "-C", cwd, "status", "--porcelain=v1", "--untracked-files=normal").Output()
		if e != nil {
			result.Ready = false
			result.Reason = "Git worktree state could not be verified"
		} else {
			result.Clean = len(diff) == 0
			if !result.Clean {
				result.Ready = false
				result.Reason = "task Worktree has uncommitted changes"
			}
		}
	}
	deps, err := s.Dependencies(t.ID)
	if err != nil {
		return HandoffPreview{}, err
	}
	for _, dep := range deps {
		prior, err := s.taskByID(dep.PrerequisiteID)
		if err != nil {
			return HandoffPreview{}, err
		}
		evidence := HandoffEvidence{TaskID: prior.ID, Agent: prior.Agent, TestsReported: "not_run"}
		var checkpointID, summary, testedHead, testStatus string
		e := s.db.QueryRow("SELECT id,summary,git_head,test_status FROM agent_checkpoints WHERE task_id=? ORDER BY created_at DESC,id DESC LIMIT 1", prior.ID).Scan(&checkpointID, &summary, &testedHead, &testStatus)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return HandoffPreview{}, e
		}
		if e == nil {
			evidence.CheckpointID = checkpointID
			evidence.Summary = summary
			evidence.TestedHead = testedHead
			evidence.TestsReported = testStatus
		}
		result.Prerequisites = append(result.Prerequisites, evidence)
	}
	digestPayload := struct {
		TaskID        string
		Revision      int64
		Agent         string
		WorktreePath  string
		GitHead       string
		Ready         bool
		Clean         bool
		Reason        string
		Prerequisites []HandoffEvidence
	}{t.ID, t.Revision, t.Agent, result.WorktreePath, result.GitHead, result.Ready, result.Clean, result.Reason, result.Prerequisites}
	raw, err := json.Marshal(digestPayload)
	if err != nil {
		return HandoffPreview{}, err
	}
	sum := sha256.Sum256(raw)
	result.Fingerprint = hex.EncodeToString(sum[:])
	return result, nil
}

func (s *Store) AcknowledgeHandoff(taskID string, revision int64, fingerprint string) (HandoffReceipt, error) {
	if revision <= 0 || len(fingerprint) != 64 {
		return HandoffReceipt{}, ErrInvalid
	}
	for _, ch := range fingerprint {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return HandoffReceipt{}, ErrInvalid
		}
	}
	preview, err := s.PreviewHandoff(taskID)
	if err != nil {
		return HandoffReceipt{}, err
	}
	if !preview.Ready || preview.Revision != revision || preview.Fingerprint != fingerprint {
		return HandoffReceipt{}, fmt.Errorf("%w: task handoff snapshot is stale or fails Git prerequisites", ErrConflict)
	}
	recipient := canonicalRecipient(preview.Agent)
	if recipient == "" {
		return HandoffReceipt{}, ErrInvalid
	}
	id, err := newID()
	if err != nil {
		return HandoffReceipt{}, err
	}
	receipt := HandoffReceipt{ID: id, TaskID: taskID, Revision: revision, Recipient: recipient, GitHead: preview.GitHead, Fingerprint: fingerprint, CreatedAt: now()}
	res, err := s.db.Exec(`INSERT INTO agent_handoff_receipts(id,task_id,revision,recipient,git_head,fingerprint,created_at)
  SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM tasks WHERE id=? AND revision=? AND status='ready')`,
		receipt.ID, receipt.TaskID, receipt.Revision, receipt.Recipient, receipt.GitHead, receipt.Fingerprint, receipt.CreatedAt, taskID, revision)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return HandoffReceipt{}, ErrConflict
		}
		return HandoffReceipt{}, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return HandoffReceipt{}, err
	}
	if count != 1 {
		return HandoffReceipt{}, ErrConflict
	}
	return receipt, nil
}

func (s *Store) HandoffReceipts(taskID string) ([]HandoffReceipt, error) {
	if _, err := s.taskByID(taskID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT id,task_id,revision,recipient,git_head,fingerprint,created_at FROM agent_handoff_receipts WHERE task_id=? ORDER BY created_at DESC,id DESC", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	receipts := []HandoffReceipt{}
	for rows.Next() {
		var a HandoffReceipt
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Revision, &a.Recipient, &a.GitHead, &a.Fingerprint, &a.CreatedAt); err != nil {
			return nil, err
		}
		receipts = append(receipts, a)
	}
	return receipts, rows.Err()
}
