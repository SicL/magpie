package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// Capability reports only the DSH command-line interface actually probed.
// No model or credentials are tested by the version command.
type Capability struct {
	Adapter   string `json:"adapter"`
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func dshCommand() (string, []string, error) {
	name := "dsh"
	if runtime.GOOS == "windows" {
		name = "dsh.cmd"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", nil, fmt.Errorf("%w: DeepSeek Harness CLI not found in PATH", ErrInvalid)
	}
	return path, []string{"--profile", "headless", "--json", "-"}, nil
}

func DetectDSH() Capability {
	out := Capability{Adapter: adapterDSH}
	path, _, err := dshCommand()
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := proc.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		out.Reason = "dsh --version failed"
		return out
	}
	help, helpErr := proc.CommandContext(ctx, path, "--profile", "headless", "--help").CombinedOutput()
	if helpErr != nil || !strings.Contains(string(help), "--json") || !strings.Contains(string(help), "--session-id") {
		out.Reason = "installed DSH lacks the supported headless JSON interface"
		return out
	}
	out.Available = true
	out.Version = strings.TrimSpace(string(result))
	return out
}

type RunService struct {
	store       *Store
	command     func() (string, []string, error)
	env         []string
	processGone func(int) (bool, error)
	mu          sync.Mutex
	active      map[string]*liveRun
	wait        sync.WaitGroup
}

type liveRun struct {
	tree       *proc.Tree
	cancelling bool
}

func NewRunService(store *Store) *RunService {
	return &RunService{store: store, command: dshCommand, processGone: previousProcessGone, active: make(map[string]*liveRun)}
}

const maxOutputBytes = 2 * 1024 * 1024
const maxLineBytes = 8192

var sessionIDPattern = regexp.MustCompile("^[A-Za-z0-9_.:-]{1,128}$")

var secretParts = []*regexp.Regexp{
	regexp.MustCompile("sk-[A-Za-z0-9_-]{12,}"),
	regexp.MustCompile("(?i)(Bearer[[:space:]]+)[A-Za-z0-9_./+=:-]{12,}"),
	regexp.MustCompile("(?i)((?:api[_-]?key|access[_-]?token)[[:space:]]*[:=][[:space:]]*[\"']?)[A-Za-z0-9_./+=:-]{12,}"),
}

// lineLog keeps separate stdout/stderr buffers, so tool diagnostics or an
// arbitrary stderr line cannot invent the native DSH session for resumption.
type lineLog struct {
	mu            sync.Mutex
	file          *os.File
	stdoutPending []byte
	stderrPending []byte
	written       int
	truncated     bool
	onSession     func(string)
}

type agentStderr struct{ log *lineLog }

func (s agentStderr) Write(p []byte) (int, error) { return s.log.writeStream(p, false) }

func sanitizeLog(line string) string {
	for i, re := range secretParts {
		if i == 0 {
			line = re.ReplaceAllString(line, "[REDACTED]")
		}
		if i > 0 {
			line = re.ReplaceAllString(line, "1[REDACTED]")
		}
	}
	return line
}

func (w *lineLog) flushLine(line []byte, fromStdout bool) error {
	if w.truncated {
		return nil
	}
	if fromStdout && w.onSession != nil {
		var evt map[string]string
		if json.Unmarshal(line, &evt) == nil && evt["type"] == "session" && sessionIDPattern.MatchString(evt["sessionId"]) {
			w.onSession(evt["sessionId"])
		}
	}
	clean := []byte(sanitizeLog(string(line)))
	if len(clean)+w.written > maxOutputBytes {
		remaining := maxOutputBytes - w.written
		if remaining > 0 {
			if _, err := w.file.Write(clean[:min(remaining, len(clean))]); err != nil {
				return err
			}
		}
		w.truncated = true
		_, err := w.file.Write([]byte("\n[output truncated]\n"))
		return err
	}
	n, err := w.file.Write(clean)
	w.written += n
	return err
}

func (w *lineLog) Write(p []byte) (int, error) { return w.writeStream(p, true) }

func (w *lineLog) writeStream(p []byte, fromStdout bool) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(p)
	pending := &w.stdoutPending
	if !fromStdout {
		pending = &w.stderrPending
	}
	for len(p) > 0 {
		size := maxLineBytes - len(*pending)
		if size <= 0 {
			if err := w.flushLine(*pending, fromStdout); err != nil {
				return 0, err
			}
			*pending = nil
			size = maxLineBytes
		}
		i := strings.IndexByte(string(p[:min(size, len(p))]), '\n')
		if i >= 0 {
			*pending = append(*pending, p[:i+1]...)
			p = p[i+1:]
			if err := w.flushLine(*pending, fromStdout); err != nil {
				return 0, err
			}
			*pending = nil
		} else {
			n := min(size, len(p))
			*pending = append(*pending, p[:n]...)
			p = p[n:]
		}
	}
	return original, nil
}

func (w *lineLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.stdoutPending) > 0 {
		if err := w.flushLine(w.stdoutPending, true); err != nil {
			return err
		}
	}
	if len(w.stderrPending) > 0 {
		if err := w.flushLine(w.stderrPending, false); err != nil {
			return err
		}
	}
	return w.file.Close()
}

// worktreeForTask refuses an arbitrary path from the request body. It
// verifies the actual Git worktree registry, path, and expected checkout.
func (s *Store) worktreeForTask(t Task) (string, error) {
	wts, err := s.Worktrees(t.ProjectID)
	if err != nil {
		return "", err
	}
	if len(t.ID) < 16 {
		return "", ErrInvalid
	}
	leaf := "task-" + t.ID[:16]
	for _, wt := range wts {
		if filepath.Base(wt.Path) == leaf {
			// The worktree must still be attached to this project's .git registry.
			out, err := proc.Command("git", "-C", wt.Path, "rev-parse", "--show-toplevel").Output()
			if err != nil || !strings.EqualFold(filepath.Clean(strings.TrimSpace(string(out))), filepath.Clean(wt.Path)) {
				return "", fmt.Errorf("%w: worktree no longer matches Git registry", ErrConflict)
			}
			return wt.Path, nil
		}
	}
	return "", fmt.Errorf("%w: create the task's isolated Worktree first", ErrConflict)
}

func (s *Store) logFile(id string) (string, error) {
	if len(id) != 32 {
		return "", ErrInvalid
	}
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", ErrInvalid
		}
	}
	dir := filepath.Join(s.dir, "run-logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".log"), nil
}

func (r *RunService) Start(taskID string, revision int64) (Run, error) {
	return r.start(taskID, revision, "")
}

// Resume makes a separate Run that continues a previously recorded DSH
// session. It never adopts an unknown session ID or runs automatically.
func (r *RunService) Resume(taskID string, revision int64, sourceRunID string) (Run, error) {
	return r.start(taskID, revision, sourceRunID)
}

func (r *RunService) start(taskID string, revision int64, sourceRunID string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.active) >= 3 {
		return Run{}, fmt.Errorf("%w: at most 3 active Agent runs", ErrConflict)
	}
	t, err := r.store.taskByID(taskID)
	if err != nil {
		return Run{}, err
	}
	if t.Status != "ready" || t.Revision != revision {
		return Run{}, ErrConflict
	}
	if !strings.EqualFold(strings.TrimSpace(t.Agent), "DeepSeek Harness") && !strings.EqualFold(strings.TrimSpace(t.Agent), "dsh") {
		return Run{}, fmt.Errorf("%w: task must be assigned to DeepSeek Harness", ErrInvalid)
	}
	cwd, err := r.store.worktreeForTask(t)
	if err != nil {
		return Run{}, err
	}
	var source Run
	if sourceRunID != "" {
		source, err = r.store.resumeSource(taskID, sourceRunID, cwd)
		if err != nil {
			return Run{}, err
		}
	}
	executable, args, err := r.command()
	if err != nil {
		return Run{}, err
	}
	if sourceRunID != "" {
		if len(args) == 0 || args[len(args)-1] != "-" {
			return Run{}, fmt.Errorf("%w: DSH CLI does not accept stdin task", ErrInvalid)
		}
		args = append(append(append([]string{}, args[:len(args)-1]...), "--session-id", source.SessionID), "-")
	}
	if executable == "" {
		return Run{}, fmt.Errorf("%w: CLI path is empty", ErrInvalid)
	}
	prompt := "Task: " + t.Title + "\n" + t.Details + "\nWorking directory: " + cwd + "\nRead project rules before changes. Do not operate outside this task Worktree. Record changes and verification results."
	// The new process sees the original task and any latest manual checkpoint
	// while DSH restores the native session context via --session-id.
	if sourceRunID != "" {
		prompt = "Continue task in the same Git Worktree.\n" + prompt
		checkpoints, e := r.store.Checkpoints(taskID)
		if e != nil {
			return Run{}, e
		}
		if len(checkpoints) > 0 {
			prompt += "\nLatest checkpoint: " + checkpoints[0].Summary
		}
	}

	run, err := r.store.reserveRun(t, revision, cwd, sourceRunID)
	if err != nil {
		return Run{}, err
	}
	logPath, err := r.store.logFile(run.ID)
	if err != nil {
		_ = r.store.finishRun(run.ID, "failed", -1, "cannot prepare run log")
		return Run{}, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = r.store.finishRun(run.ID, "failed", -1, "cannot open run log")
		return Run{}, err
	}
	writer := &lineLog{file: logFile, onSession: func(sid string) { _ = r.store.setRunSession(run.ID, sid) }}
	cmd := proc.Command(executable, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(prompt)
	if len(r.env) > 0 {
		cmd.Env = append(os.Environ(), r.env...)
	}
	cmd.Stdout = writer
	cmd.Stderr = agentStderr{log: writer}
	tree, err := proc.StartTree(cmd)
	if err != nil {
		_ = writer.Close()
		_ = r.store.finishRun(run.ID, "failed", -1, "Agent process could not start")
		return Run{}, fmt.Errorf("start DeepSeek Harness: %w", err)
	}
	active := &liveRun{tree: tree}
	r.active[run.ID] = active
	r.wait.Add(1)
	if err := r.store.runStarted(run.ID, cmd.Process.Pid); err != nil {
		active.cancelling = true
		tree.Kill()
		// Wait owns both writer and task status updates even after startup failure.
		go r.await(run.ID, active, writer)
		return Run{}, fmt.Errorf("persist DeepSeek Harness process start: %w", err)
	}
	go r.await(run.ID, active, writer)
	result, err := r.store.Run(run.ID)
	if err != nil {
		return Run{}, fmt.Errorf("read started agent run %s: %w", run.ID, err)
	}
	return result, nil
}

func (r *RunService) await(id string, job *liveRun, log *lineLog) {
	defer r.wait.Done()
	err := job.tree.Wait()
	_ = log.Close()
	exit := 0
	if err != nil {
		exit = -1
		var e *exec.ExitError
		if errors.As(err, &e) {
			exit = e.ExitCode()
		}
	}
	r.mu.Lock()
	cancelled := job.cancelling
	delete(r.active, id)
	r.mu.Unlock()
	kind, failure := "completed", ""
	if cancelled {
		kind = "cancelled"
	} else if err != nil {
		kind = "failed"
		failure = "Agent command exited unsuccessfully"
	}
	if updateErr := r.store.finishRun(id, kind, exit, failure); updateErr != nil {
		// The next startup preserves an uncertain record rather than retrying blindly.
		fmt.Fprintln(os.Stderr, "magpie orchestration: could not persist run outcome:", updateErr)
	}
}

func (r *RunService) Stop(id string) error {
	r.mu.Lock()
	job, ok := r.active[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: run is not owned by this process", ErrConflict)
	}
	if job.cancelling {
		r.mu.Unlock()
		return nil
	}
	err := r.store.runStopping(id)
	if err == nil {
		job.cancelling = true
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	// proc.Tree.Stop only terminates descendants of this exact Run's
	// process tree, not an arbitrary PID or unrelated application.
	<-job.tree.Stop(2 * time.Second)
	return nil
}

func (r *RunService) Close() {
	r.mu.Lock()
	ids := make([]string, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		_ = r.Stop(id)
	}
	r.wait.Wait()
}

func (s *Store) RunLog(id string) (string, error) {
	if _, err := s.Run(id); err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, "run-logs", id+".log")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: run log is unavailable", ErrNotFound)
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	if stat.Size() > 32*1024 {
		if _, err = f.Seek(-32*1024, io.SeekEnd); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, 32*1024))
	return string(b), err
}

// ResolveInterrupted does not stop or claim an old process. It requires an
// explicit local operator attestation, checks that the old PID is absent,
// audits the Git snapshot and leaves the workflow blocked for review.
func (r *RunService) ResolveInterrupted(id string, in RecoveryInput) (Recovery, error) {
	reason := strings.TrimSpace(in.Reason)
	if !in.Verified || len(reason) < 12 || len(reason) > 500 || in.Revision <= 0 {
		return Recovery{}, fmt.Errorf("%w: attest that old Agent and its children stopped, with a reason (12-500 chars)", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, owned := r.active[id]; owned {
		return Recovery{}, fmt.Errorf("%w: old Run is still owned by current supervisor", ErrConflict)
	}
	old, err := r.store.Run(id)
	if err != nil {
		return Recovery{}, err
	}
	if old.State != "interrupted" {
		return Recovery{}, fmt.Errorf("%w: only interrupted runs can be reconciled", ErrConflict)
	}
	dead, err := r.processGone(old.PID)
	if err != nil {
		return Recovery{}, err
	}
	if !dead {
		return Recovery{}, fmt.Errorf("%w: old process ID is still running; inspect its process tree first", ErrConflict)
	}
	return r.store.recoverRun(old, in.Revision, reason)
}
