//go:build windows

package orchestration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResumeUsesOwnedLatestSession(t *testing.T) {
	store, runner, task := setupRunner(t, "complete")
	first, err := runner.Start(task.ID, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	runner.wait.Wait()
	original, err := store.Run(first.ID)
	if err != nil || original.SessionID != "session-fixture-001" {
		t.Fatalf("fixture session %+v %v", original, err)
	}
	current, err := store.taskByID(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := store.CreateCheckpoint(CheckpointInput{TaskID: task.ID, Revision: current.Revision, Summary: "Review completed; resume with regression checks", TestStatus: "passed", RunID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.RunID != first.ID {
		t.Fatal("wrong checkpoint owner")
	}
	ready, err := store.SetTaskStatus(task.ID, current.Revision, "ready")
	if err != nil {
		t.Fatal(err)
	}
	// RunService.Resume must translate the recorded session into the exact
	// argument sequence supported by the real DSH CLI, not a shell string.
	dir := t.TempDir()
	script := filepath.Join(dir, "dsh-resume-fixture.cmd")
	capturedArgs := filepath.Join(dir, "argv.txt")
	capturedPrompt := filepath.Join(dir, "prompt.txt")
	wrapper := "@echo off\r\necho %* > \"" + capturedArgs + "\"\r\n\"" + strings.ReplaceAll(os.Args[0], "\"", "\"\"") + "\" -test.run=^TestRunnerHelperProcess$\r\n"
	if err = os.WriteFile(script, []byte(wrapper), 0600); err != nil {
		t.Fatal(err)
	}
	runner.command = func() (string, []string, error) { return script, []string{"--profile", "headless", "--json", "-"}, nil }
	runner.env = []string{"MAGPIE_RUNNER_HELPER=complete", "MAGPIE_TEST_CAPTURE_STDIN=" + capturedPrompt}
	resumed, err := runner.Resume(task.ID, ready.Revision, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ResumeOf != first.ID {
		t.Fatalf("wrong parent run %+v", resumed)
	}
	runner.wait.Wait()
	args, err := os.ReadFile(capturedArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--json --session-id session-fixture-001 -") {
		t.Fatalf("bad DSH resume argv: %s", args)
	}
	prompt, err := os.ReadFile(capturedPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "Latest checkpoint") || !strings.Contains(string(prompt), checkpoint.Summary) {
		t.Fatalf("checkpoint not in resumed stdin: %s", prompt)
	}
	result, err := store.Run(resumed.ID)
	if err != nil || result.State != "completed" || result.ResumeOf != first.ID {
		t.Fatalf("resumed result %+v %v", result, err)
	}
	current, err = store.taskByID(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	ready, err = store.SetTaskStatus(task.ID, current.Revision, "ready")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Resume(task.ID, ready.Revision, first.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("old non-latest source accepted: %v", err)
	}
	if _, err = runner.Resume(task.ID, ready.Revision, "external-user-session"); err == nil {
		t.Fatal("arbitrary session string accepted")
	}
}
