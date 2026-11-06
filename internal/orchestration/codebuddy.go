package orchestration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

const adapterCodeBuddyCLI = "codebuddy-code-cli"

// CodeBuddyConfig records a separately installed official npm CLI.
// Desktop WorkBuddy/WorkBuddyAI installations are not CodeBuddy CLI profiles.
type CodeBuddyConfig struct {
	Executable string `json:"executable"`
	Configured bool   `json:"configured"`
	Mode       string `json:"mode"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}

func validateCodeBuddyBinary(path string) (string, error) {
	if len(path) == 0 || len(path) > 1024 || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: CodeBuddy CLI requires an absolute file path", ErrInvalid)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("%w: CodeBuddy CLI file is not accessible", ErrInvalid)
	}
	fi, err := os.Stat(resolved)
	if err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%w: CodeBuddy CLI must be a regular file", ErrInvalid)
	}
	if filepath.Base(resolved) != "codebuddy" || filepath.Base(filepath.Dir(resolved)) != "bin" {
		return "", fmt.Errorf("%w: expected official npm bin/codebuddy entrypoint, not Desktop", ErrInvalid)
	}
	manifest := filepath.Join(filepath.Dir(filepath.Dir(resolved)), "package.json")
	mf, err := os.Open(manifest)
	if err != nil {
		return "", fmt.Errorf("%w: cannot identify independent CodeBuddy npm package", ErrInvalid)
	}
	defer mf.Close()
	stat, err := mf.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 128*1024 {
		return "", fmt.Errorf("%w: invalid CodeBuddy npm manifest", ErrInvalid)
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err = json.NewDecoder(mf).Decode(&pkg); err != nil || pkg.Name != "@tencent-ai/codebuddy-code" {
		return "", fmt.Errorf("%w: only official independent CodeBuddy npm package is accepted", ErrInvalid)
	}
	return resolved, nil
}

func (s *Store) SetCodeBuddyCLI(path string) (CodeBuddyConfig, error) {
	valid, err := validateCodeBuddyBinary(path)
	if err != nil {
		return CodeBuddyConfig{}, err
	}
	stamp := now()
	_, err = s.db.Exec("INSERT INTO agent_cli_profiles(adapter,executable,updated_at) VALUES(?,?,?) ON CONFLICT(adapter) DO UPDATE SET executable=excluded.executable,updated_at=excluded.updated_at", adapterCodeBuddyCLI, valid, stamp)
	if err != nil {
		return CodeBuddyConfig{}, err
	}
	return CodeBuddyConfig{Executable: valid, Configured: true, Mode: "manual-handoff", UpdatedAt: stamp}, nil
}

func (s *Store) GetCodeBuddyCLI() (CodeBuddyConfig, error) {
	var cfg CodeBuddyConfig
	err := s.db.QueryRow("SELECT executable,updated_at FROM agent_cli_profiles WHERE adapter=?", adapterCodeBuddyCLI).Scan(&cfg.Executable, &cfg.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CodeBuddyConfig{Mode: "manual-handoff"}, nil
	}
	if err != nil {
		return CodeBuddyConfig{}, err
	}
	cfg.Configured = true
	cfg.Mode = "manual-handoff"
	return cfg, nil
}

func codeBuddyInvocation(path string, args ...string) (string, []string, error) {
	valid, err := validateCodeBuddyBinary(path)
	if err != nil {
		return "", nil, err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return "", nil, fmt.Errorf("%w: Node.js is required for CodeBuddy npm CLI", ErrInvalid)
	}
	return node, append([]string{valid}, args...), nil
}

// codeBuddyHelp executes only --help or --version under an isolated HOME.
// It never loads the user's CodeBuddy or Desktop credentials, or starts
// agent prompts, interactive sessions, shell tools or file edits.
func codeBuddyHelp(ctx context.Context, path, home, cwd, arg string) (string, error) {
	if arg != "--help" && arg != "--version" {
		return "", ErrInvalid
	}
	executable, args, err := codeBuddyInvocation(path, arg)
	if err != nil {
		return "", err
	}
	cmd := proc.Command(executable, args...)
	cmd.Dir = cwd
	cmd.Env = zcodeSafeEnvironment(home)
	var output, errorOutput boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &errorOutput
	tree, err := proc.StartTree(cmd)
	if err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- tree.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		<-tree.Stop(2 * time.Second)
		<-done
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	if len(output.data) == 0 {
		return string(errorOutput.data), nil
	}
	return string(output.data), nil
}

func (s *Store) ProbeCodeBuddyCLI() (AgentCapability, error) {
	cfg, err := s.GetCodeBuddyCLI()
	if err != nil {
		return AgentCapability{}, err
	}
	cap := AgentCapability{ID: adapterCodeBuddyCLI, Name: "CodeBuddy Code CLI"}
	if !cfg.Configured {
		cap.Reason = "Independent CodeBuddy Code CLI is not configured; WorkBuddy Desktop is not the CLI"
		return cap, nil
	}
	path, err := validateCodeBuddyBinary(cfg.Executable)
	if err != nil {
		cap.Reason = err.Error()
		return cap, nil
	}
	home := filepath.Join(s.dir, "isolated-codebuddy", "probe-home")
	for _, dir := range []string{home, filepath.Join(home, "AppData", "Roaming"), filepath.Join(home, "AppData", "Local"), filepath.Join(home, "temp")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return cap, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	help, err := codeBuddyHelp(ctx, path, home, s.dir, "--help")
	if err != nil {
		cap.Reason = "Standalone CodeBuddy CLI help contract failed in isolated profile"
		return cap, nil
	}
	for _, flag := range []string{"--print", "--allowedTools", "--output-format"} {
		if !strings.Contains(help, flag) {
			cap.Reason = "Standalone CodeBuddy CLI lacks expected headless flag: " + flag
			return cap, nil
		}
	}
	cap.CLIFound = true
	versionCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	v, e := codeBuddyHelp(versionCtx, path, home, s.dir, "--version")
	if e == nil {
		cap.Version = strings.TrimSpace(v[:min(len(v), 128)])
	}
	cap.AutoRunnable = false
	cap.Reason = "Independent CLI verified. Authentication and tool permissions are not verified; manual handoff only. No -y or sandbox bypass."
	return cap, nil
}
