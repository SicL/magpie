package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

const adapterZCodePlan = "zcode-cli-plan"

type ZCodeConfig struct {
	Executable string `json:"executable"`
	Configured bool   `json:"configured"`
	Mode       string `json:"mode"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}

func (s *Store) ensureZCodeSchema() error {
	for _, q := range []string{
		"CREATE TABLE IF NOT EXISTS agent_cli_profiles (adapter TEXT PRIMARY KEY, executable TEXT NOT NULL, updated_at TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS zcode_plan_verifications (task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE, cli_hash TEXT NOT NULL, checked_at TEXT NOT NULL)",
	} {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func validateZCodeBinary(path string) (string, error) {
	if len(path) == 0 || len(path) > 1024 || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: ZCode CLI requires an absolute program path", ErrInvalid)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("%w: CLI file is not accessible: %v", ErrInvalid, err)
	}
	fi, err := os.Stat(resolved)
	if err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%w: CLI file must be a regular file", ErrInvalid)
	}
	name := strings.ToLower(filepath.Base(resolved))
	if name == "zcode.cjs" {
		return resolved, nil
	}
	if runtime.GOOS == "windows" && name == "zcode-windows-x64.exe" && runtime.GOARCH == "amd64" {
		return resolved, nil
	}
	return "", fmt.Errorf("%w: expected built zcode.cjs or official ZCode SEA filename", ErrInvalid)
}

func (s *Store) SetZCodeCLI(path string) (ZCodeConfig, error) {
	valid, err := validateZCodeBinary(path)
	if err != nil {
		return ZCodeConfig{}, err
	}
	stamp := now()
	_, err = s.db.Exec("INSERT INTO agent_cli_profiles(adapter,executable,updated_at) VALUES(?,?,?) ON CONFLICT(adapter) DO UPDATE SET executable=excluded.executable,updated_at=excluded.updated_at", adapterZCodePlan, valid, stamp)
	if err != nil {
		return ZCodeConfig{}, err
	}
	return ZCodeConfig{Executable: valid, Configured: true, Mode: "plan", UpdatedAt: stamp}, nil
}

func (s *Store) GetZCodeCLI() (ZCodeConfig, error) {
	var value ZCodeConfig
	err := s.db.QueryRow("SELECT executable,updated_at FROM agent_cli_profiles WHERE adapter=?", adapterZCodePlan).Scan(&value.Executable, &value.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ZCodeConfig{Mode: "plan"}, nil
	}
	if err != nil {
		return ZCodeConfig{}, err
	}
	value.Configured = true
	value.Mode = "plan"
	return value, nil
}

// ZCode CLI is a separately built distribution, not ZCode Desktop.
// A successful help probe establishes a CLI contract, not provider login.
func (s *Store) ProbeZCodeCLI() (AgentCapability, error) {
	cfg, err := s.GetZCodeCLI()
	if err != nil {
		return AgentCapability{}, err
	}
	cap := AgentCapability{ID: adapterZCodePlan, Name: "ZCode CLI (plan mode)"}
	if !cfg.Configured {
		cap.Reason = "ZCode CLI not configured; desktop app is not the CLI"
		return cap, nil
	}
	path, err := validateZCodeBinary(cfg.Executable)
	if err != nil {
		cap.Reason = err.Error()
		return cap, nil
	}
	probeHome := filepath.Join(s.dir, "isolated-zcode", "probe-home")
	if err = os.MkdirAll(filepath.Join(probeHome, "temp"), 0700); err != nil {
		return cap, err
	}
	if err = os.MkdirAll(probeHome, 0700); err != nil {
		return cap, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd, args, err := zcodeInvocation(path, "--help")
	if err != nil {
		return cap, err
	}
	probeCmd := proc.CommandContext(ctx, cmd, args...)
	probeCmd.Dir = s.dir
	probeCmd.Env = zcodeSafeEnvironment(probeHome)
	out, err := probeCmd.CombinedOutput()
	if err != nil {
		cap.Reason = "ZCode CLI help probe failed"
		return cap, nil
	}
	for _, flag := range []string{"--prompt", "--mode", "--cwd", "--disallowed-tools"} {
		if !strings.Contains(string(out), flag) {
			cap.Reason = "ZCode CLI lacks required safe plan-mode argument: " + flag
			return cap, nil
		}
	}
	// Some official ZCode builds accept --output-format stream-json but
	// advertise only --json in --help. Both are supported protocol markers.
	if !strings.Contains(string(out), "--output-format") && !strings.Contains(string(out), "--json") {
		cap.Reason = "ZCode CLI lacks structured JSON headless output"
		return cap, nil
	}
	cap.CLIFound = true
	cmd, args, err = zcodeInvocation(path, "--version")
	if err == nil {
		vctx, vstop := context.WithTimeout(context.Background(), 5*time.Second)
		defer vstop()
		versionCmd := proc.CommandContext(vctx, cmd, args...)
		versionCmd.Dir = s.dir
		versionCmd.Env = zcodeSafeEnvironment(probeHome)
		b, e := versionCmd.CombinedOutput()
		if e == nil {
			cap.Version = strings.TrimSpace(string(b[:min(len(b), 256)]))
		}
	}
	// Plan mode is not a full OS sandbox. Opt-in clicks are required.
	cap.AutoRunnable = false
	cap.Reason = "Plan-mode CLI contract verified. Live model/credential verification must succeed for this task before execution. Not an OS sandbox."
	return cap, nil
}

func zcodeInvocation(path string, args ...string) (string, []string, error) {
	path, err := validateZCodeBinary(path)
	if err != nil {
		return "", nil, err
	}
	if strings.EqualFold(filepath.Ext(path), ".cjs") {
		node, err := exec.LookPath("node")
		if err != nil {
			return "", nil, fmt.Errorf("%w: Node.js 24 required for ZCode bundle", ErrInvalid)
		}
		return node, append([]string{path}, args...), nil
	}
	return path, args, nil
}

func zcodeSafeEnvironment(home string) []string {
	// Only environment keys required for process startup. Never inherit
	// unrelated API keys, user-specific Agent settings or cloud credentials.
	keys := []string{"PATH", "PATHEXT", "SystemRoot", "WINDIR", "ComSpec", "LANG"}
	env := make([]string, 0, len(keys)+5)
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return append(env, "HOME="+home, "USERPROFILE="+home,
		"APPDATA="+filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
		"ZCODE_DATA_BASE_DIR="+home, "TEMP="+filepath.Join(home, "temp"), "TMP="+filepath.Join(home, "temp"))
}

func (r *RunService) StartZCodePlan(taskID string, revision int64) (Run, error) {
	cap, err := r.store.ProbeZCodeCLI()
	if err != nil {
		return Run{}, err
	}
	if !cap.CLIFound {
		return Run{}, fmt.Errorf("%w: %s", ErrConflict, cap.Reason)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.active) >= 3 {
		return Run{}, fmt.Errorf("%w: maximum 3 active Agent Runs", ErrConflict)
	}
	t, err := r.store.taskByID(taskID)
	if err != nil {
		return Run{}, err
	}
	if t.Revision != revision || t.Status != "ready" {
		return Run{}, ErrConflict
	}
	assigned := strings.ToLower(strings.TrimSpace(t.Agent))
	if assigned != "zcode" && assigned != "zcode cli" && assigned != "zcode-cli" {
		return Run{}, fmt.Errorf("%w: task is not assigned to ZCode", ErrInvalid)
	}
	cwd, err := r.store.worktreeForTask(t)
	if err != nil {
		return Run{}, err
	}
	if err = r.store.CheckDependenciesForLaunch(t, cwd); err != nil {
		return Run{}, err
	}

	cfg, err := r.store.GetZCodeCLI()
	if err != nil {
		return Run{}, err
	}
	if err = r.store.requireZCodeVerified(t.ID, cfg.Executable); err != nil {
		return Run{}, err
	}
	home := filepath.Join(r.store.dir, "isolated-zcode", "task-"+t.ID)
	for _, folder := range []string{home, filepath.Join(home, "AppData", "Roaming"), filepath.Join(home, "AppData", "Local"), filepath.Join(home, "temp")} {
		if err = os.MkdirAll(folder, 0700); err != nil {
			return Run{}, err
		}
	}
	prompt := "Analyze the following task and propose a plan only. Do not modify files, run commands, use MCP tools, or access unrelated directories.\nTask: " + t.Title + "\n" + t.Details + "\nTask Worktree: " + cwd
	args := []string{"--prompt", prompt, "--cwd", cwd, "--mode", "plan", "--workflow-mode", "disabled", "--disallowed-tools", "Bash,Write,Edit,MultiEdit,ApplyPatch,NotebookEdit", "--output-format", "stream-json", "--no-color"}
	executable, fullArgs, err := zcodeInvocation(cfg.Executable, args...)
	if err != nil {
		return Run{}, err
	}
	env, e := r.store.zcodeEnvironment(home)
	if e != nil {
		return Run{}, e
	}
	return r.launchRun(t, revision, cwd, adapterZCodePlan, executable, fullArgs, "", "", env)
}
