package orchestration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/proc"
)

func handoffTask(t *testing.T, s *Store, projectID, name, agent string) Task {
	t.Helper()
	v, err := s.CreateTask(Task{ProjectID: projectID, Title: name, Agent: agent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateWorktree(v.ID); err != nil {
		t.Fatal(err)
	}
	return v
}
func handoffGit(t *testing.T, wt string, args ...string) {
	t.Helper()
	out, err := proc.Command("git", append([]string{"-C", wt}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}
func handoffCommit(t *testing.T, s *Store, task Task, name string) Checkpoint {
	t.Helper()
	wt, err := s.worktreeForTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(wt, name), []byte(name), 0600); err != nil {
		t.Fatal(err)
	}
	handoffGit(t, wt, "add", name)
	handoffGit(t, wt, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", name)
	cp, err := s.CreateCheckpoint(CheckpointInput{TaskID: task.ID, Revision: task.Revision, Summary: "User-reported tests pass", TestStatus: "passed"})
	if err != nil || cp.Dirty {
		t.Fatalf("checkpoint %+v %v", cp, err)
	}
	return cp
}

func TestHandoffBlocksDirtyAndStaleSnapshots(t *testing.T) {
	s, _, a := setupRunner(t, "complete")
	before, err := s.PreviewHandoff(a.ID)
	if err != nil || !before.Ready || !before.Clean || before.GitHead == "" || before.Fingerprint == "" {
		t.Fatalf("clean ready handoff %+v %v", before, err)
	}
	receipt, err := s.AcknowledgeHandoff(a.ID, a.Revision, before.Fingerprint)
	if err != nil || receipt.Recipient != "DeepSeek Harness" {
		t.Fatalf("handoff receipt %+v %v", receipt, err)
	}
	if _, err = s.AcknowledgeHandoff(a.ID, a.Revision, before.Fingerprint); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate receipt accepted: %v", err)
	}
	rows, err := s.HandoffReceipts(a.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("receipt log %+v %v", rows, err)
	}
	wt, err := s.worktreeForTask(a)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(wt, "not-reviewed.txt"), []byte("not reviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	dirty, err := s.PreviewHandoff(a.ID)
	if err != nil || dirty.Ready || dirty.Clean || dirty.Fingerprint == before.Fingerprint {
		t.Fatalf("unreviewed changes did not invalidate handoff %+v %v", dirty, err)
	}
	if _, err = s.AcknowledgeHandoff(a.ID, a.Revision, before.Fingerprint); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Git snapshot accepted: %v", err)
	}
	if err = os.Remove(filepath.Join(wt, "not-reviewed.txt")); err != nil {
		t.Fatal(err)
	}
	handoffGit(t, wt, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "head updated")
	changed, err := s.PreviewHandoff(a.ID)
	if err != nil || !changed.Ready || changed.GitHead == before.GitHead {
		t.Fatalf("head changed %+v %v", changed, err)
	}
	if _, err = s.AcknowledgeHandoff(a.ID, a.Revision, before.Fingerprint); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale old head accepted: %v", err)
	}
	if _, err = s.AcknowledgeHandoff(a.ID, a.Revision, changed.Fingerprint); err != nil {
		t.Fatal(err)
	}
	records, err := s.HandoffReceipts(a.ID)
	if err != nil || len(records) != 2 {
		t.Fatalf("append-only handoff record count %d %v", len(records), err)
	}
	if _, err = s.AcknowledgeHandoff(a.ID, a.Revision, "123"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid hash accepted %v", err)
	}
	current, err := s.taskByID(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err = s.SetTaskStatus(a.ID, current.Revision, "blocked")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := s.projectByID(current.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	handoffGit(t, proj.RepoPath, "worktree", "remove", wt)
	if err = s.DeleteTask(a.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("task with acknowledged receipts was deleted after Git worktree removal: %v", err)
	}
	if _, err = s.PreviewHandoff("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown task accepted %v", err)
	}
}

func TestHandoffThreeAgentGitChainAndTestClaims(t *testing.T) {
	s, _, dsh := setupRunner(t, "complete")
	zcode := handoffTask(t, s, dsh.ProjectID, "ZCode reviews DSH", "ZCode")
	codebuddy := handoffTask(t, s, dsh.ProjectID, "CodeBuddy reviews ZCode", "CodeBuddy Code")
	var err error
	zcode, err = s.ReplaceDependencies(zcode.ID, zcode.Revision, []string{dsh.ID})
	if err != nil {
		t.Fatal(err)
	}
	codebuddy, err = s.ReplaceDependencies(codebuddy.ID, codebuddy.Revision, []string{zcode.ID})
	if err != nil {
		t.Fatal(err)
	}
	zcode = readyGraphTask(t, s, zcode)
	codebuddy = readyGraphTask(t, s, codebuddy)
	invalid, err := s.PreviewHandoff(zcode.ID)
	if err != nil || invalid.Ready {
		t.Fatalf("unapproved prerequisite handoff was accepted: %+v %v", invalid, err)
	}
	if _, err = s.AcknowledgeHandoff(zcode.ID, zcode.Revision, invalid.Fingerprint); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing predecessor checkpoint accepted: %v", err)
	}
	cpDSH := handoffCommit(t, s, dsh, "feature-dsh.txt")
	dsh = finishGraphTask(t, s, dsh)
	zWT, err := s.worktreeForTask(zcode)
	if err != nil {
		t.Fatal(err)
	}
	handoffGit(t, zWT, "merge", "--ff-only", cpDSH.GitHead)
	previewZ, err := s.PreviewHandoff(zcode.ID)
	if err != nil || !previewZ.Ready || len(previewZ.Prerequisites) != 1 || previewZ.Prerequisites[0].TestedHead != cpDSH.GitHead || previewZ.Prerequisites[0].TestsReported != "passed" {
		t.Fatalf("ZCode handoff missing integrated DSH evidence %+v %v", previewZ, err)
	}
	zReceipt, err := s.AcknowledgeHandoff(zcode.ID, zcode.Revision, previewZ.Fingerprint)
	if err != nil || zReceipt.Recipient != "ZCode" {
		t.Fatalf("ZCode acknowledgment %+v %v", zReceipt, err)
	}
	cpZ := handoffCommit(t, s, zcode, "feature-zcode.txt")
	zcode = finishGraphTask(t, s, zcode)
	cWT, err := s.worktreeForTask(codebuddy)
	if err != nil {
		t.Fatal(err)
	}
	handoffGit(t, cWT, "merge", "--ff-only", cpZ.GitHead)
	previewC, err := s.PreviewHandoff(codebuddy.ID)
	if err != nil || !previewC.Ready || len(previewC.Prerequisites) != 1 || previewC.Prerequisites[0].Agent != "ZCode" || previewC.Prerequisites[0].TestedHead != cpZ.GitHead {
		t.Fatalf("CodeBuddy handoff missing integrated ZCode evidence %+v %v", previewC, err)
	}
	if _, err = s.AcknowledgeHandoff(codebuddy.ID, codebuddy.Revision, previewZ.Fingerprint); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-task handoff hash accepted: %v", err)
	}
	cReceipt, err := s.AcknowledgeHandoff(codebuddy.ID, codebuddy.Revision, previewC.Fingerprint)
	if err != nil || cReceipt.Recipient != "CodeBuddy Code" {
		t.Fatalf("CodeBuddy acknowledgment %+v %v", cReceipt, err)
	}
	// An acceptance receipt alone must not change downstream task state or
	// start an Agent: the next step remains explicit human-controlled work.
	current, err := s.taskByID(codebuddy.ID)
	if err != nil || current.Status != "ready" || current.Revision != codebuddy.Revision {
		t.Fatalf("acknowledgment unexpectedly modified target task %+v %v", current, err)
	}
}
