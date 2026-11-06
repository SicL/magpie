package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// CodeBuddyModelCheck is a transient, fixed-prompt diagnostic. It never
// asserts where the provider credentials came from, grants permissions,
// records a session, or allows any project tool operation.
type CodeBuddyModelCheck struct {
	Configured   bool   `json:"configured"`
	CLIReady     bool   `json:"cliReady"`
	ModelReplied bool   `json:"modelReplied"`
	Status       string `json:"status"`
}

const codeBuddyModelCanary = "MAGPIE_CODEBUDDY_READ_ONLY_CANARY_OK"

// No calls are made against the user's repository. Even plan mode is not
// an operating system sandbox: keep this to a fixed prompt and empty cwd.
func (s *Store) ProbeCodeBuddyModel() (CodeBuddyModelCheck, error) {
	status := CodeBuddyModelCheck{Status: "unconfigured"}
	cfg, err := s.GetCodeBuddyCLI()
	if err != nil {
		return status, err
	}
	if !cfg.Configured {
		return status, nil
	}
	status.Configured = true
	cap, err := s.ProbeCodeBuddyCLI()
	if err != nil {
		return status, err
	}
	if !cap.CLIFound {
		status.Status = "cli_unavailable"
		return status, nil
	}
	status.CLIReady = true
	// Ignore any --help contract which lacks a required no-tool sandboxing
	// parameter. This is intentionally a stricter check than P9's help.
	base := filepath.Join(s.dir, "isolated-codebuddy")
	if err = os.MkdirAll(base, 0700); err != nil {
		return status, err
	}
	probeHome, err := os.MkdirTemp(base, "readonly-model-*")
	if err != nil {
		return status, err
	}
	defer os.RemoveAll(probeHome)
	for _, dir := range []string{
		filepath.Join(probeHome, "AppData", "Roaming"),
		filepath.Join(probeHome, "AppData", "Local"),
		filepath.Join(probeHome, "temp"),
	} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return status, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	help, err := codeBuddyHelp(ctx, cfg.Executable, probeHome, probeHome, "--help")
	if err != nil {
		status.Status = "cli_unavailable"
		return status, nil
	}
	for _, flag := range []string{"--permission-mode", "--tools", "--strict-mcp-config", "--mcp-config", "--no-session-persistence", "--max-turns", "--print", "--output-format"} {
		if !strings.Contains(help, flag) {
			status.Status = "unsafe_cli_contract"
			return status, nil
		}
	}
	emptyMCP := filepath.Join(probeHome, "empty-mcp.json")
	if err = os.WriteFile(emptyMCP, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		return status, err
	}
	program, args, err := codeBuddyInvocation(cfg.Executable,
		"--print",
		"--permission-mode", "plan",
		"--tools", "",
		"--strict-mcp-config",
		"--mcp-config", emptyMCP,
		"--no-session-persistence",
		"--max-turns", "1",
		"--output-format", "json",
		"Reply with exactly "+codeBuddyModelCanary+" and nothing else. Do not call any tools.",
	)
	if err != nil {
		return status, err
	}
	cmd := proc.Command(program, args...)
	cmd.Dir = probeHome
	cmd.Env = codeBuddyRestrictedEnvironment(probeHome)
	var stdout, stderr boundedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	tree, err := proc.StartTree(cmd)
	if err != nil {
		status.Status = "unverified"
		return status, nil
	}
	done := make(chan error, 1)
	go func() { done <- tree.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		<-tree.Stop(2 * time.Second)
		<-done
		status.Status = "timed_out"
		return status, nil
	}
	if err != nil {
		status.Status = "unverified"
		return status, nil
	}
	var reply struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.data))
	if err = decoder.Decode(&reply); err != nil {
		status.Status = "unverified"
		return status, nil
	}
	// A valid result event alone is not proof of model reasoning, and an
	// arbitrary CLI binary can forge it. Do not use this as task clearance.
	if reply.Type == "result" && reply.Subtype == "success" && !reply.IsError && strings.TrimSpace(reply.Result) == codeBuddyModelCanary {
		status.ModelReplied = true
		status.Status = "model_replied"
	} else {
		status.Status = "unverified"
	}
	return status, nil
}

func codeBuddyRestrictedEnvironment(home string) []string {
	// zcodeSafeEnvironment already filters inherited keys, but do not pass
	// ZCode-specific variables to a separate provider's process.
	from := zcodeSafeEnvironment(home)
	result := make([]string, 0, len(from))
	for _, value := range from {
		if strings.HasPrefix(value, "ZCODE_") {
			continue
		}
		result = append(result, value)
	}
	return result
}
