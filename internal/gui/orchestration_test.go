package gui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/orchestration"
	"github.com/yetone/magpie/internal/proc"
)

func orchestrationRequest(t *testing.T, mux *http.ServeMux, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

func TestOrchestrationAPIProjectsAndTasks(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repository")
	for _, args := range [][]string{
		{"init", "-b", "main", repo},
		{"-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial"},
	} {
		if out, err := proc.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	store, err := orchestration.Open(filepath.Join(t.TempDir(), "project.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return store, nil }, false)

	code, body := orchestrationRequest(t, mux, "GET", "/api/orchestration/projects", nil)
	if code != 200 || string(bytes.TrimSpace(body)) != "[]" {
		t.Fatalf("empty projects %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/projects", map[string]string{"name": "Test", "repoPath": repo})
	if code != 201 {
		t.Fatalf("create project %d %s", code, body)
	}
	var project orchestration.Project
	if err = json.Unmarshal(body, &project); err != nil {
		t.Fatal(err)
	}
	if project.ID == "" || project.BaseBranch != "main" {
		t.Fatalf("project %+v", project)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks", map[string]string{"projectId": project.ID, "title": "Review", "agent": "ZCode"})
	if code != 201 {
		t.Fatalf("create task %d %s", code, body)
	}
	var task orchestration.Task
	if err = json.Unmarshal(body, &task); err != nil {
		t.Fatal(err)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/"+task.ID+"/worktree", map[string]any{})
	if code != http.StatusCreated {
		t.Fatalf("create worktree %d %s", code, body)
	}
	var wt orchestration.Worktree
	if err = json.Unmarshal(body, &wt); err != nil || wt.Path == "" {
		t.Fatalf("worktree %+v %v", wt, err)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/worktrees?project="+project.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("list worktrees %d %s", code, body)
	}
	var worktrees []orchestration.Worktree
	if err = json.Unmarshal(body, &worktrees); err != nil || len(worktrees) != 1 {
		t.Fatalf("worktree list %+v %v", worktrees, err)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/"+task.ID+"/status", map[string]any{"revision": task.Revision, "status": "ready"})
	if code != 200 {
		t.Fatalf("update task %d %s", code, body)
	}

	// Persist and enumerate a human handoff checkpoint at the current task revision.
	var current orchestration.Task
	if err = json.Unmarshal(body, &current); err != nil {
		t.Fatal(err)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/"+task.ID+"/checkpoints", map[string]any{"revision": current.Revision, "summary": "Reviewed changes; tests still pending", "testStatus": "not_run"})
	if code != http.StatusCreated {
		t.Fatalf("create checkpoint %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/tasks/"+task.ID+"/checkpoints", nil)
	if code != http.StatusOK {
		t.Fatalf("list checkpoints %d %s", code, body)
	}
	var checkpoints []orchestration.Checkpoint
	if err = json.Unmarshal(body, &checkpoints); err != nil || len(checkpoints) != 1 {
		t.Fatalf("checkpoint list %+v %v", checkpoints, err)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/"+task.ID+"/checkpoints", map[string]any{"revision": task.Revision, "summary": "stale revision", "testStatus": "not_run"})
	if code != http.StatusConflict {
		t.Fatalf("stale checkpoint accepted %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/tasks/"+task.ID+"/recoveries", nil)
	if code != http.StatusOK || string(bytes.TrimSpace(body)) != "[]" {
		t.Fatalf("unexpected recovery history %d %s", code, body)
	}

	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/"+task.ID+"/status", map[string]any{"revision": task.Revision, "status": "blocked"})
	if code != 409 {
		t.Fatalf("stale task should conflict: %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/projects/"+project.ID+"/remove", map[string]any{})
	if code != 409 {
		t.Fatalf("cannot remove project with task: %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/runs?project="+project.ID, nil)
	if code != 200 || string(bytes.TrimSpace(body)) != "[]" {
		t.Fatalf("unexpected run list %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/tasks?project="+project.ID, nil)
	if code != 200 {
		t.Fatalf("list task %d %s", code, body)
	}
	var tasks []orchestration.Task
	if err = json.Unmarshal(body, &tasks); err != nil || len(tasks) != 1 || tasks[0].Status != "ready" {
		t.Fatalf("tasks %+v %v", tasks, err)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks", map[string]string{
		"projectId": project.ID, "title": "Legacy prerequisite", "agent": "WorkBuddy",
	})
	if code != http.StatusCreated {
		t.Fatalf("create prerequisite task %d %s", code, body)
	}
	var predecessor orchestration.Task
	if err = json.Unmarshal(body, &predecessor); err != nil {
		t.Fatal(err)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/"+task.ID+"/dependencies", map[string]any{
		"revision": tasks[0].Revision, "prerequisiteIds": []string{predecessor.ID},
	})
	if code != http.StatusOK {
		t.Fatalf("set prerequisites %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/dependencies?project="+project.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("list project dependencies %d %s", code, body)
	}
	var edges []orchestration.Dependency
	if err = json.Unmarshal(body, &edges); err != nil || len(edges) != 1 || edges[0].PrerequisiteID != predecessor.ID {
		t.Fatalf("dependency API returned %+v %v", edges, err)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/tasks/"+task.ID+"/readiness", nil)
	if code != http.StatusOK {
		t.Fatalf("readiness API %d %s", code, body)
	}
	var ready orchestration.Readiness
	if err = json.Unmarshal(body, &ready); err != nil || ready.Ready {
		t.Fatalf("unmet prerequisite allowed %+v %v", ready, err)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/projects/"+project.ID+"/dispatch", map[string]any{"maxRuns": 1})
	if code != http.StatusOK {
		t.Fatalf("dispatch API %d %s", code, body)
	}
	var report orchestration.DispatchReport
	if err = json.Unmarshal(body, &report); err != nil || len(report.Started) != 0 || len(report.Skipped) != 1 {
		t.Fatalf("manual-only dispatch must skip %+v %v", report, err)
	}

}

func TestOrchestrationReadOnlyWebBlocksWrites(t *testing.T) {
	dir := t.TempDir()
	store, err := orchestration.Open(filepath.Join(dir, "project.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return store, nil }, true)
	code, body := orchestrationRequest(t, mux, "POST", "/api/orchestration/projects", map[string]string{"name": "Blocked"})
	if code != http.StatusForbidden {
		t.Fatalf("web write %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/untrusted/run", map[string]any{"revision": 1})
	if code != http.StatusForbidden {
		t.Fatalf("browser must not launch agent: %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/runs/untrusted/stop", map[string]any{})
	if code != http.StatusForbidden {
		t.Fatalf("browser must not stop agent: %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/runs/untrusted/log", nil)
	if code != http.StatusForbidden {
		t.Fatalf("browser must not read agent logs: %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/fake/checkpoints", map[string]any{"revision": 1, "summary": "x", "testStatus": "not_run"})
	if code != http.StatusForbidden {
		t.Fatalf("browser created checkpoint %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/fake/resume", map[string]any{"revision": 1, "sourceRunId": "fake"})
	if code != http.StatusForbidden {
		t.Fatalf("browser resumed agent %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/runs/fake/reconcile", map[string]any{"revision": 1, "verified": true, "reason": "process stopped and reviewed"})
	if code != http.StatusForbidden {
		t.Fatalf("browser reconciled agent %d %s", code, body)
	}

	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/fake/dependencies", map[string]any{"revision": 1, "prerequisiteIds": []string{}})
	if code != http.StatusForbidden {
		t.Fatalf("browser edited prerequisite graph %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/projects/fake/dispatch", map[string]any{"maxRuns": 1})
	if code != http.StatusForbidden {
		t.Fatalf("browser launched batch dispatch %d %s", code, body)
	}

	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/projects", nil)
	if code != http.StatusOK || string(bytes.TrimSpace(body)) != "[]" {
		t.Fatalf("web list %d %s", code, body)
	}
	if _, err = os.Stat(filepath.Join(dir, "project.db")); err != nil {
		t.Fatal(err)
	}
}

func TestOrchestrationCrossOriginCannotExecuteAgents(t *testing.T) {
	store, err := orchestration.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return store, nil }, false)
	for _, tc := range []struct {
		label, origin, contentType, fetchSite string
		want                                  int
	}{
		{"foreign origin", "https://evil.example", "application/json", "cross-site", http.StatusForbidden},
		{"null origin", "null", "application/json", "", http.StatusForbidden},
		{"simple form", "", "text/plain", "", http.StatusUnsupportedMediaType},
	} {
		t.Run(tc.label, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/orchestration/tasks/unknown/run", bytes.NewBufferString("{\"revision\":1}"))
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Content-Type", tc.contentType)
			req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("request permitted: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestOrchestrationDispatchAndDependenciesRejectCrossOrigin(t *testing.T) {
	store, err := orchestration.Open(filepath.Join(t.TempDir(), "project.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return store, nil }, false)
	for _, target := range []string{
		"/api/orchestration/projects/foreign/dispatch",
		"/api/orchestration/agents/zcode/config",
		"/api/orchestration/agents/zcode/probe",
		"/api/orchestration/tasks/foreign/plan-zcode",
		"/api/orchestration/tasks/foreign/zcode/verify",
		"/api/orchestration/tasks/foreign/dependencies",
	} {
		req := httptest.NewRequest("POST", target, bytes.NewBufferString("{\"maxRuns\":1}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://outside.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("cross-site action allowed at %s: %d %s", target, rr.Code, rr.Body.String())
		}
	}
}
func TestZCodeCLIConfigureProbeAndGuard(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is required for isolated ZCode CLI API fixture")
	}
	dir := t.TempDir()
	store, err := orchestration.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path := filepath.Join(dir, "zcode.cjs")
	script := `const args=process.argv.slice(2);
if(args.includes("--help")){console.log("--prompt --mode --cwd --output-format --disallowed-tools");process.exit(0);}
if(args.includes("--version")){console.log("0.17.1-api-test");process.exit(0);}
process.exit(7);`
	if err = os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return store, nil }, false)
	code, body := orchestrationRequest(t, mux, "POST", "/api/orchestration/agents/zcode/config", map[string]any{"executable": path})
	if code != http.StatusOK {
		t.Fatalf("zcode configure: %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/agents/zcode/config", nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte("zcode.cjs")) {
		t.Fatalf("saved profile: %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/agents/zcode/probe", map[string]any{"confirm": false})
	if code != http.StatusBadRequest {
		t.Fatalf("unconfirmed probe %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/agents/zcode/probe", map[string]any{"confirm": true})
	if code != http.StatusOK || !bytes.Contains(body, []byte("0.17.1-api-test")) {
		t.Fatalf("probe failed %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", "/api/orchestration/tasks/nonexistent/zcode/verification", nil)
	if code != http.StatusNotFound {
		t.Fatalf("unrecognized task verification status %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/nonexistent/zcode/verify", map[string]any{"revision": 1, "confirm": false})
	if code != http.StatusBadRequest {
		t.Fatalf("unconfirmed model probe %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/tasks/nonexistent/plan-zcode", map[string]any{"revision": 1})
	if code != http.StatusNotFound {
		t.Fatalf("unexpected unknown task plan result %d %s", code, body)
	}
	readOnly := http.NewServeMux()
	mountOrchestrationRoutes(readOnly, func() (*orchestration.Store, error) { return store, nil }, true)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/orchestration/agents/zcode/config", nil},
		{"POST", "/api/orchestration/agents/zcode/config", map[string]any{"executable": path}},
		{"POST", "/api/orchestration/agents/zcode/probe", map[string]any{"confirm": true}},
		{"POST", "/api/orchestration/tasks/nonexistent/plan-zcode", map[string]any{"revision": 1}},
		{"GET", "/api/orchestration/tasks/nonexistent/zcode/verification", nil},
		{"POST", "/api/orchestration/tasks/nonexistent/zcode/verify", map[string]any{"revision": 1, "confirm": true}},
	} {
		code, body = orchestrationRequest(t, readOnly, tc.method, tc.path, tc.body)
		if code != http.StatusForbidden {
			t.Fatalf("browser ZCode endpoint allowed %s %s: %d %s", tc.method, tc.path, code, body)
		}
	}
}
