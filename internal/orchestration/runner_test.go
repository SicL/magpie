package orchestration

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerHelperProcess(t *testing.T) {
	mode := os.Getenv("MAGPIE_RUNNER_HELPER")
	if mode == "" {
		return
	}
	input, _ := io.ReadAll(os.Stdin)
	if capture := os.Getenv("MAGPIE_TEST_CAPTURE_STDIN"); capture != "" {
		if err := os.WriteFile(capture, input, 0600); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "complete":
		fmt.Println("{\"type\":\"session\",\"sessionId\":\"session-fixture-001\"}")
		fmt.Println("{\"message\":\"sk-0123456789ABCDEF012345 Bearer token123456789abcdef api_key=example123456789012\"}")
	case "fail":
		fmt.Fprintln(os.Stderr, "fixture failed")
		os.Exit(3)
	case "hang":
		fmt.Println("{\"event\":\"ready\"}")
		for {
			time.Sleep(50 * time.Millisecond)
		}
	default:
		os.Exit(7)
	}
}

func setupRunner(t *testing.T, mode string) (*Store, *RunService, Task) {
	t.Helper()
	repo := testRepo(t)
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	p, err := store.CreateProject(Project{Name: "test", RepoPath: repo, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees")})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(Task{ProjectID: p.ID, Title: "Test task", Details: "Verify output", Agent: "DeepSeek Harness"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateWorktree(task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = store.SetTaskStatus(task.ID, task.Revision, "ready")
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunService(store)
	runner.command = func() (string, []string, error) {
		return os.Args[0], []string{"-test.run=^TestRunnerHelperProcess$"}, nil
	}
	runner.env = []string{"MAGPIE_RUNNER_HELPER=" + mode}
	t.Cleanup(runner.Close)
	return store, runner, task
}

func TestRunServiceFinishesAndRedactsOutput(t *testing.T) {
	store, runner, task := setupRunner(t, "complete")
	launched, err := runner.Start(task.ID, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if launched.PID <= 0 {
		t.Fatalf("did not start a process: %+v", launched)
	}
	runner.wait.Wait()
	finished, err := store.Run(launched.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.SessionID != "session-fixture-001" {
		t.Fatalf("missing session id: %+v", finished)
	}
	if finished.State != "completed" || finished.ExitCode == nil || *finished.ExitCode != 0 {
		t.Fatalf("run %+v", finished)
	}
	current, err := store.taskByID(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "review" {
		t.Fatalf("task status %s", current.Status)
	}
	log, err := store.RunLog(launched.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log, "0123456789ABCDEF") || strings.Contains(log, "token123456789abcdef") || strings.Contains(log, "example123456789012") {
		t.Fatalf("secret printed without redaction: %q", log)
	}
	if !strings.Contains(log, "[REDACTED]") {
		t.Fatalf("no scrubbed markers: %q", log)
	}
	if err := os.Remove(filepath.Join(store.dir, "run-logs", launched.ID+".log")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RunLog(launched.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing log should be unknown: %v", err)
	}
	if len(log) > maxOutputBytes {
		t.Fatalf("log too large: %d", len(log))
	}
}

func TestRunServiceStopsOnlyOwnedProcess(t *testing.T) {
	store, runner, task := setupRunner(t, "hang")
	first, err := runner.Start(task.ID, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Start(task.ID, task.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate run: %v", err)
	}
	if err = runner.Stop(first.ID); err != nil {
		t.Fatal(err)
	}
	runner.wait.Wait()
	result, err := store.Run(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "cancelled" {
		t.Fatalf("stopped run = %+v", result)
	}
	current, err := store.taskByID(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "blocked" {
		t.Fatalf("task status after stop: %s", current.Status)
	}
	if err = runner.Stop(first.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancel without ownership: %v", err)
	}
}

func TestRunServiceRecordsAgentFailure(t *testing.T) {
	store, runner, task := setupRunner(t, "fail")
	first, err := runner.Start(task.ID, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	runner.wait.Wait()
	result, err := store.Run(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "failed" || result.ExitCode == nil || *result.ExitCode != 3 {
		t.Fatalf("failure record %+v", result)
	}
	current, err := store.taskByID(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "failed" {
		t.Fatalf("task status %s", current.Status)
	}
}

func TestRunRestartIsNotClaimedAgain(t *testing.T) {
	repo := testRepo(t)
	file := filepath.Join(t.TempDir(), "state.db")
	store, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{Name: "test", RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(Task{ProjectID: p.ID, Title: "Run", Agent: "DeepSeek Harness"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.SetTaskStatus(task.ID, task.Revision, "ready")
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.reserveRun(task, task.Revision, "only-a-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, err := store.Run(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "interrupted" {
		t.Fatalf("restart should not assume previous process died: %+v", state)
	}
	if _, err = store.reserveRun(task, task.Revision, "somewhere"); !errors.Is(err, ErrConflict) {
		t.Fatalf("uncertain run accepted: %v", err)
	}
}

func TestConcurrentClaimsOnlyStartOneAgent(t *testing.T) {
	store, runner, task := setupRunner(t, "hang")
	const tries = 8
	type result struct {
		run Run
		err error
	}
	ch := make(chan result, tries)
	for i := 0; i < tries; i++ {
		go func() { r, e := runner.Start(task.ID, task.Revision); ch <- result{r, e} }()
	}
	successes := 0
	var owned Run
	for i := 0; i < tries; i++ {
		x := <-ch
		if x.err == nil {
			successes++
			owned = x.run
		} else if !errors.Is(x.err, ErrConflict) {
			t.Fatalf("unexpected race error: %v", x.err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one Agent run, got %d", successes)
	}
	active, err := store.Runs(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("expected one durable Run, got %+v", active)
	}
	if err = runner.Stop(owned.ID); err != nil {
		t.Fatal(err)
	}
	runner.wait.Wait()
}
