package orchestration

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// AgentCapability distinguishes an installed desktop application from an
// independently authenticated, tested headless CLI. Presence alone is not
// permission to execute a user's source code.
type AgentCapability struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CLIFound     bool   `json:"cliFound"`
	AutoRunnable bool   `json:"autoRunnable"`
	Version      string `json:"version,omitempty"`
	Reason       string `json:"reason"`
}

func findHeadlessCommand(names ...string) (string, bool) {
	for _, name := range names {
		candidates := []string{name}
		if runtime.GOOS == "windows" {
			candidates = []string{name + ".cmd", name + ".bat"}
		}
		// On Windows only probe known CLI shims; a desktop ZCode.exe
		// can otherwise launch a GUI in response to --help.
		for _, n := range candidates {
			if p, e := exec.LookPath(n); e == nil {
				return p, true
			}
		}
	}
	return "", false
}

func probeHeadless(name, label string, commands []string, flags []string) AgentCapability {
	cap := AgentCapability{ID: name, Name: label, AutoRunnable: false}
	path, ok := findHeadlessCommand(commands...)
	if !ok {
		cap.Reason = "Independent headless CLI is not installed on PATH; desktop app is not an execution adapter"
		return cap
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	help, err := proc.CommandContext(ctx, path, "--help").CombinedOutput()
	if err != nil {
		cap.Reason = "CLI found but its help contract could not be checked"
		return cap
	}
	for _, flag := range flags {
		if !strings.Contains(string(help), flag) {
			cap.Reason = "CLI found but required non-interactive flags are missing"
			return cap
		}
	}
	cap.CLIFound = true
	version, err := proc.CommandContext(ctx, path, "--version").CombinedOutput()
	if err == nil {
		cap.Version = strings.TrimSpace(string(version))
	}
	cap.Reason = "Headless CLI detected; credentials, permissions and isolated runtime have not been validated. Execution is disabled."
	return cap
}

func AgentCapabilities() []AgentCapability {
	dsh := DetectDSH()
	caps := []AgentCapability{{
		ID: adapterDSH, Name: "DeepSeek Harness", CLIFound: dsh.Available,
		AutoRunnable: dsh.Available, Version: dsh.Version, Reason: dsh.Reason,
	}}
	if dsh.Available {
		caps[0].Reason = "Verified headless JSON CLI; runs only after explicit desktop action"
	}
	caps = append(caps,
		AgentCapability{ID: "workbuddy-desktop", Name: "WorkBuddy Desktop", Reason: "Manual handoff only; a supported desktop headless launcher is not verified"},
		AgentCapability{ID: "zcode-desktop", Name: "ZCode Desktop", Reason: "Manual handoff only; desktop installation is not the independent ZCode CLI"},
		AgentCapability{ID: adapterCodeBuddyCLI, Name: "CodeBuddy Code CLI", Reason: "Configure the independent official npm CLI in Projects and verify it explicitly; manual handoff only"},
		probeHeadless("zcode-cli", "ZCode Agent CLI", []string{"zcode"}, []string{"--prompt", "--mode", "--json"}),
	)
	return caps
}
