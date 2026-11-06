package orchestration

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createFakeZCode(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for ZCode bundle fixture")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "zcode.cjs")
	script := `const fs=require("fs");
const args=process.argv.slice(2);
if(args.includes("--help")){console.log("--prompt --mode --cwd --output-format --disallowed-tools --workflow-mode");process.exit(0);}
if(args.includes("--version")){console.log("0.17.1-test");process.exit(0);}
if(!args.includes("--prompt")||!args.includes("plan")||!args.includes("stream-json")||!args.includes("disabled")){process.exit(21);}
const get=(flag)=>args[args.indexOf(flag)+1];
if(get("--mode")!=="plan"||!get("--disallowed-tools").includes("Bash")||get("--cwd")!==process.cwd()){process.exit(22);}
const env={home:process.env.HOME,userprofile:process.env.USERPROFILE,profile:process.env.ZCODE_DATA_BASE_DIR,aws:process.env.AWS_SECRET_ACCESS_KEY||"",tokens:process.env.OPENAI_API_KEY||""};
fs.writeFileSync("plan-fixture.json",JSON.stringify(env));
console.log(JSON.stringify({type:"result",response:get("--prompt").includes("MAGPIE_ZCODE_PLAN_OK")?"MAGPIE_ZCODE_PLAN_OK":"PLAN_READY"}));`
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestZCodeCLIConfigRejectsUnknownExecutables(t *testing.T) {
	store, _, task := setupRunner(t, "complete")
	_ = task
	if _, err := store.SetZCodeCLI("zcode.cjs"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative path accepted: %v", err)
	}
	other := filepath.Join(t.TempDir(), "node_modules.cjs")
	if err := os.WriteFile(other, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetZCodeCLI(other); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unrecognized file accepted: %v", err)
	}
	if _, err := store.GetZCodeCLI(); err != nil {
		t.Fatal(err)
	}
}

func TestZCodePlanHasDedicatedRunAndCredentialFreeEnvironment(t *testing.T) {
	store, runner, parent := setupRunner(t, "complete")
	zcode := createFakeZCode(t)
	conf, err := store.SetZCodeCLI(zcode)
	if err != nil {
		t.Fatal(err)
	}
	if !conf.Configured || conf.Executable != zcode {
		t.Fatalf("CLI config %+v", conf)
	}
	cap, err := store.ProbeZCodeCLI()
	if err != nil || !cap.CLIFound || cap.AutoRunnable || cap.Version != "0.17.1-test" {
		t.Fatalf("CLI contract probe %+v %v", cap, err)
	}
	task, err := store.CreateTask(Task{ProjectID: parent.ProjectID, Title: "Safe ZCode analysis", Agent: "ZCode", Details: "No external data"})
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
	t.Setenv("AWS_SECRET_ACCESS_KEY", "should-not-reach-plan")
	t.Setenv("OPENAI_API_KEY", "should-not-reach-plan")
	if _, err := runner.StartZCodePlan(task.ID, task.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("unverified ZCode plan was allowed: %v", err)
	}
	verified, err := store.VerifyZCodeModel(task.ID, task.Revision)
	if err != nil || !verified.Verified {
		t.Fatalf("model canary verification failed %+v %v", verified, err)
	}
	run, err := runner.StartZCodePlan(task.ID, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	runner.wait.Wait()
	result, err := store.Run(run.ID)
	if err != nil || result.State != "completed" || result.Adapter != adapterZCodePlan || result.SessionID != "" {
		t.Fatalf("ZCode run lifecycle %+v %v", result, err)
	}
	tail, err := store.RunLog(run.ID)
	if err != nil || !strings.Contains(tail, "PLAN_READY") {
		t.Fatalf("ZCode output %q %v", tail, err)
	}
	worktree, err := store.worktreeForTask(task)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(worktree, "plan-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var details struct{ Home, Userprofile, Profile, AWS, Tokens string }
	if err = json.Unmarshal(b, &details); err != nil {
		t.Fatal(err)
	}
	if details.AWS != "" || details.Tokens != "" {
		t.Fatal("Process inherited credentials")
	}
	if details.Home == "" || details.Profile != details.Home || details.Userprofile != details.Home {
		t.Fatalf("ZCode plan not isolated: %+v", details)
	}
	if _, err = runner.StartZCodePlan(parent.ID, parent.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DSH task assigned as ZCode was accepted: %v", err)
	}
}

func TestZCodeCLIPathPersistence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	exe := createFakeZCode(t)
	if _, err = s.SetZCodeCLI(exe); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg, err := s.GetZCodeCLI()
	if err != nil || cfg.Executable != exe || !cfg.Configured || cfg.Mode != "plan" {
		t.Fatalf("profile not persisted: %+v %v", cfg, err)
	}
}

func TestZCodeCanaryIsBoundToTaskAndBinaryDigest(t *testing.T) {
	store, _, parent := setupRunner(t, "complete")
	executable := createFakeZCode(t)
	if _, err := store.SetZCodeCLI(executable); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(Task{ProjectID: parent.ProjectID, Title: "Digest-bound plan", Agent: "ZCode"})
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
	if _, err = store.VerifyZCodeModel(task.ID, task.Revision); err != nil {
		t.Fatal(err)
	}
	status, err := store.ZCodeVerificationStatus(task.ID)
	if err != nil || !status.Verified || status.CheckedAt == "" {
		t.Fatalf("verified state missing %+v %v", status, err)
	}
	if _, err = store.db.Exec("UPDATE zcode_plan_verifications SET checked_at=? WHERE task_id=?", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), task.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := store.ZCodeVerificationStatus(task.ID)
	if err != nil || expired.Verified {
		t.Fatalf("expired model canary was accepted %+v %v", expired, err)
	}
	if _, err = store.VerifyZCodeModel(task.ID, task.Revision); err != nil {
		t.Fatalf("reverification after expiry: %v", err)
	}
	other, err := store.CreateTask(Task{ProjectID: parent.ProjectID, Title: "Another unverified plan", Agent: "ZCode"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateWorktree(other.ID); err != nil {
		t.Fatal(err)
	}
	other, err = store.SetTaskStatus(other.ID, other.Revision, "ready")
	if err != nil {
		t.Fatal(err)
	}
	noStatus, err := store.ZCodeVerificationStatus(other.ID)
	if err != nil || noStatus.Verified {
		t.Fatalf("verification crossed tasks %+v %v", noStatus, err)
	}
	// A binary change invalidates prior model verification, even if its
	// version/help output still appears valid. This also blocks launch.
	f, err := os.OpenFile(executable, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("\n// changed after canary\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	status, err = store.ZCodeVerificationStatus(task.ID)
	if err != nil || status.Verified {
		t.Fatalf("modified executable treated as verified %+v %v", status, err)
	}
	if err = store.requireZCodeVerified(task.ID, executable); !errors.Is(err, ErrConflict) {
		t.Fatalf("modified executable accepted: %v", err)
	}
}

func TestZCodeCanaryBlocksUnresolvedRun(t *testing.T) {
	store, _, parent := setupRunner(t, "complete")
	path := createFakeZCode(t)
	if _, err := store.SetZCodeCLI(path); err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateTask(Task{ProjectID: parent.ProjectID, Title: "Task with unresolved process", Agent: "ZCode"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateWorktree(item.ID); err != nil {
		t.Fatal(err)
	}
	item, err = store.SetTaskStatus(item.ID, item.Revision, "ready")
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := store.worktreeForTask(item)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.reserveRunAdapter(item, item.Revision, cwd, adapterZCodePlan, ""); err != nil {
		t.Fatal(err)
	}
	// An active/uncertain lease cannot trigger another canary even if the
	// task status is accidentally changed back to ready.
	current, err := store.taskByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err = store.SetTaskStatus(item.ID, current.Revision, "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetTaskStatus(item.ID, current.Revision, "ready"); err != nil {
		t.Fatal(err)
	}
	current, err = store.taskByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.VerifyZCodeModel(item.ID, current.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("unresolved run canary permitted: %v", err)
	}
}
