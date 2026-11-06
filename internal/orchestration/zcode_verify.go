package orchestration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// ZCodeVerification records only a successful explicit model canary.
// No prompt, API key, model response or provider credential is persisted.
type ZCodeVerification struct {
	TaskID    string `json:"taskId"`
	Verified  bool   `json:"verified"`
	CheckedAt string `json:"checkedAt,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// boundedOutput prevents untrusted CLI streams from consuming unlimited RAM.
type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	const max = 256 * 1024
	if len(b.data) < max {
		remaining := max - len(b.data)
		b.data = append(b.data, p[:min(remaining, len(p))]...)
	}
	return n, nil
}

var _ io.Writer = (*boundedOutput)(nil)

func executableDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err = io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func (s *Store) ZCodeVerificationStatus(taskID string) (ZCodeVerification, error) {
	task, err := s.taskByID(taskID)
	if err != nil {
		return ZCodeVerification{}, err
	}
	cfg, err := s.GetZCodeCLI()
	if err != nil {
		return ZCodeVerification{}, err
	}
	result := ZCodeVerification{TaskID: task.ID}
	if !cfg.Configured {
		result.Reason = "No standalone ZCode CLI configured"
		return result, nil
	}
	if err = s.requireZCodeVerified(task.ID, cfg.Executable); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	if err = s.db.QueryRow("SELECT checked_at FROM zcode_plan_verifications WHERE task_id=?", task.ID).Scan(&result.CheckedAt); err != nil {
		return ZCodeVerification{}, err
	}
	result.Verified = true
	return result, nil
}

func (s *Store) requireZCodeVerified(taskID, path string) error {
	if path == "" {
		return fmt.Errorf("%w: ZCode CLI path is not configured", ErrConflict)
	}
	valid, err := validateZCodeBinary(path)
	if err != nil {
		return err
	}
	var storedHash, checkedAt string
	err = s.db.QueryRow("SELECT cli_hash,checked_at FROM zcode_plan_verifications WHERE task_id=?", taskID).Scan(&storedHash, &checkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: ZCode plan model verification required", ErrConflict)
	}
	if err != nil {
		// Database no-rows always means unverified, never permit a launch.
		return fmt.Errorf("%w: no successful model canary for this task", ErrConflict)
	}
	when, parseErr := time.Parse(time.RFC3339Nano, checkedAt)
	if parseErr != nil || time.Since(when) < 0 || time.Since(when) > 15*time.Minute {
		return fmt.Errorf("%w: ZCode plan model verification expired; repeat canary", ErrConflict)
	}
	actual, err := executableDigest(valid)
	if err != nil {
		return fmt.Errorf("%w: cannot hash configured ZCode CLI: %v", ErrConflict, err)
	}
	if actual != storedHash {
		return fmt.Errorf("%w: ZCode executable changed since model verification", ErrConflict)
	}
	return nil
}

// VerifyZCodeModel is an explicit, bounded live provider check. It uses the
// *same* per-task isolated HOME as subsequent plan runs; failing or malformed
// output cannot authorize a plan launch.
func (s *Store) VerifyZCodeModel(taskID string, revision int64) (ZCodeVerification, error) {
	t, err := s.taskByID(taskID)
	if err != nil {
		return ZCodeVerification{}, err
	}
	if t.Revision != revision || t.Status != "ready" {
		return ZCodeVerification{}, ErrConflict
	}
	assigned := strings.ToLower(strings.TrimSpace(t.Agent))
	if assigned != "zcode" && assigned != "zcode cli" && assigned != "zcode-cli" {
		return ZCodeVerification{}, fmt.Errorf("%w: task must be assigned to ZCode", ErrInvalid)
	}
	cwd, err := s.worktreeForTask(t)
	if err != nil {
		return ZCodeVerification{}, err
	}
	if err = s.CheckDependenciesForLaunch(t, cwd); err != nil {
		return ZCodeVerification{}, err
	}
	readiness, e := s.TaskReadiness(t.ID)
	if e != nil {
		return ZCodeVerification{}, e
	}
	if !readiness.Ready {
		return ZCodeVerification{}, fmt.Errorf("%w: %s", ErrConflict, readiness.Reason)
	}
	cap, err := s.ProbeZCodeCLI()
	if err != nil {
		return ZCodeVerification{}, err
	}
	if !cap.CLIFound {
		return ZCodeVerification{}, fmt.Errorf("%w: %s", ErrConflict, cap.Reason)
	}
	cfg, err := s.GetZCodeCLI()
	if err != nil {
		return ZCodeVerification{}, err
	}
	path, err := validateZCodeBinary(cfg.Executable)
	if err != nil {
		return ZCodeVerification{}, err
	}
	home := filepath.Join(s.dir, "isolated-zcode", "task-"+t.ID)
	for _, folder := range []string{home, filepath.Join(home, "AppData", "Roaming"), filepath.Join(home, "AppData", "Local"), filepath.Join(home, "temp")} {
		if err = os.MkdirAll(folder, 0700); err != nil {
			return ZCodeVerification{}, err
		}
	}
	// Canary is a model call: only the user-triggered verification API may
	// invoke this code. The prompt is fixed and contains no user task details.
	prompt := "Reply exactly MAGPIE_ZCODE_PLAN_OK. Do not use tools, edit files or run commands."
	argv := []string{"--prompt", prompt, "--cwd", cwd, "--mode", "plan", "--workflow-mode", "disabled", "--disallowed-tools", "Bash,Write,Edit,MultiEdit,ApplyPatch,NotebookEdit", "--output-format", "stream-json", "--no-color"}
	exe, args, err := zcodeInvocation(path, argv...)
	if err != nil {
		return ZCodeVerification{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := proc.Command(exe, args...)
	cmd.Dir = cwd
	cmd.Env = zcodeSafeEnvironment(home)
	var stdout, stderr boundedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	tree, startErr := proc.StartTree(cmd)
	if startErr != nil {
		return ZCodeVerification{}, fmt.Errorf("%w: cannot start ZCode provider canary", ErrConflict)
	}
	finished := make(chan error, 1)
	go func() { finished <- tree.Wait() }()
	select {
	case err = <-finished:
	case <-ctx.Done():
		<-tree.Stop(2 * time.Second)
		<-finished
		return ZCodeVerification{}, fmt.Errorf("%w: ZCode provider canary timed out", ErrConflict)
	}
	if err != nil {
		return ZCodeVerification{}, fmt.Errorf("%w: ZCode provider canary failed (check isolated model configuration)", ErrConflict)
	}
	found := false
	scan := bufio.NewScanner(bytes.NewReader(stdout.data))
	scan.Buffer(make([]byte, 4096), 256*1024)
	for scan.Scan() {
		var v struct {
			Type     string `json:"type"`
			Response string `json:"response"`
			IsError  bool   `json:"is_error"`
		}
		if json.Unmarshal(scan.Bytes(), &v) != nil {
			continue
		}
		if v.Type == "result" && !v.IsError && strings.Contains(v.Response, "MAGPIE_ZCODE_PLAN_OK") {
			found = true
		}
	}
	if err = scan.Err(); err != nil {
		return ZCodeVerification{}, fmt.Errorf("%w: malformed canary output", ErrConflict)
	}
	if !found {
		return ZCodeVerification{}, fmt.Errorf("%w: no positive ZCode model result event", ErrConflict)
	}
	sha, err := executableDigest(path)
	if err != nil {
		return ZCodeVerification{}, err
	}
	stamp := now()
	res, err := s.db.Exec("INSERT INTO zcode_plan_verifications(task_id,cli_hash,checked_at) SELECT ?,?,? WHERE EXISTS(SELECT 1 FROM tasks WHERE id=? AND revision=? AND status='ready') ON CONFLICT(task_id) DO UPDATE SET cli_hash=excluded.cli_hash,checked_at=excluded.checked_at", t.ID, sha, stamp, t.ID, revision)
	if err != nil {
		return ZCodeVerification{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return ZCodeVerification{}, err
	}
	if n != 1 {
		return ZCodeVerification{}, ErrConflict
	}
	return ZCodeVerification{TaskID: t.ID, Verified: true, CheckedAt: stamp}, nil
}
