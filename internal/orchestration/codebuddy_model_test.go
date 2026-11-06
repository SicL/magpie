package orchestration

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func codeBuddyModelFixture(t *testing.T, mode string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js not installed")
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
	script := `const fs=require("fs"),path=require("path");
const args=process.argv.slice(2);
if(args.includes("--help")){console.log("--print --allowedTools --output-format --permission-mode --tools --strict-mcp-config --mcp-config --no-session-persistence --max-turns");process.exit(0)}
if(args.includes("--version")){console.log("2.180.0");process.exit(0)}
if(args.includes("--print")){
  const value=(flag)=>{const idx=args.indexOf(flag);return idx<0?null:args[idx+1]};
  if(value("--tools")!==""||value("--permission-mode")!=="plan"||value("--max-turns")!=="1"||!args.includes("--no-session-persistence")||!args.includes("--strict-mcp-config"))process.exit(9);
  const conf=value("--mcp-config");
  if(!conf||JSON.parse(fs.readFileSync(conf,"utf8")).mcpServers===undefined)process.exit(9);
  if(fs.realpathSync(process.cwd())!==fs.realpathSync(process.env.HOME))process.exit(10);
  if(process.env.CODEBUDDY_API_KEY||process.env.OPENAI_API_KEY||process.env.CODEBUDDY_IS_SANDBOX||process.env.ZCODE_DATA_BASE_DIR)process.exit(11);
  if(!args.some(x=>x.includes("MAGPIE_CODEBUDDY_READ_ONLY_CANARY_OK")))process.exit(12);
  if("MODE"==="fail"){console.error("credentials hidden");process.exit(1)}
  if("MODE"==="error"){console.log(JSON.stringify({type:"result",subtype:"error",is_error:true,result:"MAGPIE_CODEBUDDY_READ_ONLY_CANARY_OK"}));process.exit(0)}
  if("MODE"==="wrong"){console.log(JSON.stringify({type:"result",subtype:"success",is_error:false,result:"not the expected response"}));process.exit(0)}
  console.log(JSON.stringify({type:"result",subtype:"success",is_error:false,result:"MAGPIE_CODEBUDDY_READ_ONLY_CANARY_OK"}));
  process.exit(0);
}
process.exit(7);`
	script = strings.ReplaceAll(script, "MODE", mode)
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodeBuddyReadOnlyModelCanaryCannotStartRun(t *testing.T) {
	s, _, _ := setupRunner(t, "complete")
	unset, err := s.ProbeCodeBuddyModel()
	if err != nil || unset.Status != "unconfigured" || unset.ModelReplied {
		t.Fatalf("unconfigured result: %+v %v", unset, err)
	}
	exe := codeBuddyModelFixture(t, "success")
	if _, err = s.SetCodeBuddyCLI(exe); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEBUDDY_API_KEY", "must-not-leak")
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	t.Setenv("CODEBUDDY_IS_SANDBOX", "1")
	result, err := s.ProbeCodeBuddyModel()
	if err != nil || !result.Configured || !result.CLIReady || !result.ModelReplied || result.Status != "model_replied" {
		t.Fatalf("expected isolated no-tools reply, got %+v %v", result, err)
	}
	var n int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM agent_runs").Scan(&n); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("model canary started a task run %d", n)
	}
	matches, err := filepath.Glob(filepath.Join(s.dir, "isolated-codebuddy", "readonly-model-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("canary temporary home was retained %v %v", matches, err)
	}
}

func TestCodeBuddyReadOnlyModelProbeFailsClosed(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"fail", "unverified"},
		{"error", "unverified"},
		{"wrong", "unverified"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			s, _, _ := setupRunner(t, "complete")
			exe := codeBuddyModelFixture(t, tc.mode)
			if _, err := s.SetCodeBuddyCLI(exe); err != nil {
				t.Fatal(err)
			}
			result, err := s.ProbeCodeBuddyModel()
			if err != nil || result.ModelReplied || result.Status != tc.want {
				t.Fatalf("accepted malformed or failed model result %+v %v", result, err)
			}
		})
	}
}

func TestCodeBuddyReadOnlyRejectsInsufficientCLIHelp(t *testing.T) {
	s, _, _ := setupRunner(t, "complete")
	exe := makeOfficialCodeBuddyFixture(t, true) // P9 help lacks the required no-tools flags.
	if _, err := s.SetCodeBuddyCLI(exe); err != nil {
		t.Fatal(err)
	}
	result, err := s.ProbeCodeBuddyModel()
	if err != nil || result.ModelReplied || result.Status != "unsafe_cli_contract" {
		t.Fatalf("unsafe CLI flags accepted %+v %v", result, err)
	}
}

func TestOfficialCodeBuddyNoToolsModelProbe(t *testing.T) {
	path := os.Getenv("MAGPIE_CODEBUDDY_CLI_INTEGRATION")
	if path == "" {
		t.Skip("official CodeBuddy CLI not requested")
	}
	s, _, _ := setupRunner(t, "complete")
	if _, err := s.SetCodeBuddyCLI(path); err != nil {
		t.Fatal(err)
	}
	result, err := s.ProbeCodeBuddyModel()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Configured || !result.CLIReady {
		t.Fatalf("real isolated CodeBuddy CLI unavailable: %+v", result)
	}
	if result.Status == "" {
		t.Fatal("empty model check status")
	}
	// A real response may depend on separate model authentication. Do not
	// assert model success, load Desktop credentials or authorize tool runs.
	t.Logf("official CLI isolated fixed prompt status: %s; does NOT authorize agent execution", result.Status)
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM agent_runs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("untrusted model check created %d Run(s)", count)
	}
}
