package orchestration

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func makeOfficialCodeBuddyFixture(t *testing.T, good bool) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js required for npm fixture")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"@tencent-ai/codebuddy-code","version":"2.180.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "codebuddy")
	text := `const fs=require('fs');
const argv=process.argv.slice(2);
if(argv.includes('--help')){
 console.log('--print --allowedTools --output-format --permission-mode');
 process.exit(0);
}
if(argv.includes('--version')){
 fs.writeFileSync(process.env.HOME+'/probe-result.json', JSON.stringify({
  home:process.env.HOME,
  apiKey:process.env.OPENAI_API_KEY||'',
  desktop:process.env.CODEBUDDY_IS_SANDBOX||'',
  userConfig:process.env.CODEBUDDY_API_KEY||''
 }));
 console.log('2.180.0');process.exit(0);
}
process.exit(7);`
	if !good {
		text = `console.log('--version');`
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodeBuddyCLIRequiresOfficialNPMDistribution(t *testing.T) {
	s, _, _ := setupRunner(t, "complete")
	valid := makeOfficialCodeBuddyFixture(t, true)
	if _, err := s.SetCodeBuddyCLI(valid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetCodeBuddyCLI(filepath.Join(t.TempDir(), "codebuddy")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nonexistent accepted: %v", err)
	}
	if _, err := s.SetCodeBuddyCLI("bin/codebuddy"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative script accepted: %v", err)
	}
	if _, err := s.SetCodeBuddyCLI("C:\\Program Files\\WorkBuddy\\WorkBuddy.exe"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Desktop executable accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(valid)), "package.json"), []byte(`{"name":"@genie/agent-cli"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetCodeBuddyCLI(valid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("WorkBuddy embedded package accepted: %v", err)
	}
}

func TestCodeBuddyCLIDetectedWithoutCredentialsOrRunPermission(t *testing.T) {
	s, _, _ := setupRunner(t, "complete")
	cfg, err := s.GetCodeBuddyCLI()
	if err != nil || cfg.Configured || cfg.Mode != "manual-handoff" {
		t.Fatalf("empty config %+v %v", cfg, err)
	}
	cap, err := s.ProbeCodeBuddyCLI()
	if err != nil || cap.CLIFound || cap.AutoRunnable {
		t.Fatalf("unconfigured CLI %+v %v", cap, err)
	}
	exe := makeOfficialCodeBuddyFixture(t, true)
	cfg, err = s.SetCodeBuddyCLI(exe)
	if err != nil || !cfg.Configured {
		t.Fatalf("set CodeBuddy %+v %v", cfg, err)
	}
	t.Setenv("CODEBUDDY_API_KEY", "must-not-leak")
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	t.Setenv("CODEBUDDY_IS_SANDBOX", "1")
	cap, err = s.ProbeCodeBuddyCLI()
	if err != nil || !cap.CLIFound || cap.AutoRunnable || cap.Version != "2.180.0" {
		t.Fatalf("CodeBuddy CLI contract %+v %v", cap, err)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "isolated-codebuddy", "probe-home", "probe-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "must-not-leak") || strings.Contains(string(b), `"desktop":"1"`) {
		t.Fatalf("CLI inherited ambient secrets or bypass flag: %s", b)
	}
	if !strings.Contains(cap.Reason, "manual handoff") {
		t.Fatalf("help check granted execution: %+v", cap)
	}
}

func TestCodeBuddyCLIProbeIsFailClosedIfHelpIncomplete(t *testing.T) {
	s, _, _ := setupRunner(t, "complete")
	exe := makeOfficialCodeBuddyFixture(t, false)
	if _, err := s.SetCodeBuddyCLI(exe); err != nil {
		t.Fatal(err)
	}
	cap, err := s.ProbeCodeBuddyCLI()
	if err != nil || cap.CLIFound || cap.AutoRunnable {
		t.Fatalf("incomplete CLI permitted %+v %v", cap, err)
	}
}

func TestOfficialCodeBuddyCLIBinaryContract(t *testing.T) {
	path := os.Getenv("MAGPIE_CODEBUDDY_CLI_INTEGRATION")
	if path == "" {
		t.Skip("official independent CodeBuddy npm installation not available")
	}
	s, _, _ := setupRunner(t, "complete")
	if _, err := s.SetCodeBuddyCLI(path); err != nil {
		t.Fatalf("official CodeBuddy CLI registration: %v", err)
	}
	cap, err := s.ProbeCodeBuddyCLI()
	if err != nil || !cap.CLIFound || cap.AutoRunnable {
		t.Fatalf("official isolated CodeBuddy CLI help/version check %+v %v", cap, err)
	}
}
