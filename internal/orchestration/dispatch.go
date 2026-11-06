package orchestration

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type DispatchSkip struct {
	TaskID string `json:"taskId"`
	Reason string `json:"reason"`
}

type DispatchReport struct {
	ProjectID string         `json:"projectId"`
	Started   []Run          `json:"started"`
	Skipped   []DispatchSkip `json:"skipped"`
}

func isDSHAssignment(s string) bool {
	s = strings.TrimSpace(s)
	return strings.EqualFold(s, "dsh") || strings.EqualFold(s, "DeepSeek Harness")
}

// DispatchReady is deliberately an explicit user-triggered *batch* action,
// never an unattended loop. Only proven DSH executions can be started;
// WorkBuddy and ZCode are still human handoffs. Every Start() rechecks
// the task revision, Git integration evidence and active-run lease.
func (r *RunService) DispatchReady(projectID string, limit int) (DispatchReport, error) {
	if strings.TrimSpace(projectID) == "" || limit < 1 || limit > 3 {
		return DispatchReport{}, fmt.Errorf("%w: project and max 1-3 runs required", ErrInvalid)
	}
	var exists int
	err := r.store.db.QueryRow("SELECT 1 FROM projects WHERE id=?", projectID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return DispatchReport{}, ErrNotFound
	}
	if err != nil {
		return DispatchReport{}, err
	}
	tasks, err := r.store.Tasks(projectID)
	if err != nil {
		return DispatchReport{}, err
	}
	report := DispatchReport{ProjectID: projectID, Started: []Run{}, Skipped: []DispatchSkip{}}
	for _, t := range tasks {
		if t.Status != "ready" {
			continue
		}
		if len(report.Started) >= limit {
			report.Skipped = append(report.Skipped, DispatchSkip{TaskID: t.ID, Reason: "Dispatch limit reached; task left ready for later"})
			continue
		}
		if !isDSHAssignment(t.Agent) {
			report.Skipped = append(report.Skipped, DispatchSkip{TaskID: t.ID, Reason: "No verified headless executor for assigned Agent; use manual handoff"})
			continue
		}
		evidence, err := r.store.TaskReadiness(t.ID)
		if err != nil {
			return report, err
		}
		if !evidence.Ready {
			report.Skipped = append(report.Skipped, DispatchSkip{TaskID: t.ID, Reason: evidence.Reason})
			continue
		}
		started, err := r.Start(t.ID, t.Revision)
		if err != nil {
			report.Skipped = append(report.Skipped, DispatchSkip{TaskID: t.ID, Reason: err.Error()})
			continue
		}
		report.Started = append(report.Started, started)
	}
	return report, nil
}
