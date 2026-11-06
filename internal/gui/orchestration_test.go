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

func TestZCodeLocalModelRoutesAndBrowserGuards(t *testing.T) {
	s, err := orchestration.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return s, nil }, false)
	route := "/api/orchestration/agents/zcode/local-model"
	code, body := orchestrationRequest(t, mux, "GET", route, nil)
	if code != http.StatusOK {
		t.Fatalf("get empty local model %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", route, map[string]any{"baseUrl": "https://api.example.com/v1", "modelId": "secret"})
	if code != http.StatusBadRequest {
		t.Fatalf("remote endpoint accepted: %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", route, map[string]any{"baseUrl": "http://127.0.0.1:1234/v1", "modelId": "local-qwen"})
	if code != http.StatusOK {
		t.Fatalf("local model save failed %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", route, nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte("local-qwen")) {
		t.Fatalf("model not persisted %d %s", code, body)
	}
	webOnly := http.NewServeMux()
	mountOrchestrationRoutes(webOnly, func() (*orchestration.Store, error) { return s, nil }, true)
	for _, method := range []string{"GET", "POST"} {
		code, body = orchestrationRequest(t, webOnly, method, route, map[string]any{"baseUrl": "http://127.0.0.1:1234/v1", "modelId": "x"})
		if code != http.StatusForbidden {
			t.Fatalf("Web mode accessed model config %s %d %s", method, code, body)
		}
	}
	req := httptest.NewRequest("POST", route, bytes.NewBufferString(`{"baseUrl":"http://127.0.0.1:1234/v1","modelId":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://untrusted.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-site model config accepted %d", recorder.Code)
	}
}

func TestZCodeReadinessDesktopOnlyAndConfirmation(t *testing.T) {
	s, err := orchestration.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	route := "/api/orchestration/agents/zcode/local-model/check"
	api := http.NewServeMux()
	mountOrchestrationRoutes(api, func() (*orchestration.Store, error) { return s, nil }, false)
	status, body := orchestrationRequest(t, api, "POST", route, map[string]any{"confirm": false})
	if status != http.StatusBadRequest {
		t.Fatalf("unconfirmed local model probe %d %s", status, body)
	}
	status, body = orchestrationRequest(t, api, "POST", route, map[string]any{"confirm": true})
	if status != http.StatusOK || !bytes.Contains(body, []byte("unconfigured")) {
		t.Fatalf("no model probe %+v %s", status, body)
	}
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected model route %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen3-local"}]}`))
	}))
	defer modelServer.Close()
	if _, err = s.SetZCodeLocalModel(modelServer.URL+"/v1", "qwen3-local"); err != nil {
		t.Fatal(err)
	}
	status, body = orchestrationRequest(t, api, "POST", route, map[string]any{"confirm": true})
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"modelFound":true`)) {
		t.Fatalf("local model probe status %d %s", status, body)
	}
	webOnly := http.NewServeMux()
	mountOrchestrationRoutes(webOnly, func() (*orchestration.Store, error) { return s, nil }, true)
	status, body = orchestrationRequest(t, webOnly, "POST", route, map[string]any{"confirm": true})
	if status != http.StatusForbidden {
		t.Fatalf("browser mode triggered network probe %d %s", status, body)
	}
	req := httptest.NewRequest("POST", route, bytes.NewReader([]byte(`{"confirm":true}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://untrusted.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rw := httptest.NewRecorder()
	api.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("cross-site triggered network probe %d", rw.Code)
	}
}

func TestCodeBuddyCLIConfigProbeAndDesktopGuards(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for CodeBuddy CLI probe")
	}
	s, err := orchestration.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "@tencent-ai", "codebuddy-code")
	binDir := filepath.Join(pkgDir, "bin")
	if err = os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"@tencent-ai/codebuddy-code"}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(binDir, "codebuddy")
	js := `if(process.argv.includes('--help')){console.log('--print --allowedTools --output-format');process.exit(0);}
if(process.argv.includes('--version')){console.log('2.180.0-test');process.exit(0);}
process.exit(7);`
	if err = os.WriteFile(path, []byte(js), 0600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return s, nil }, false)
	route := "/api/orchestration/agents/codebuddy/config"
	code, body := orchestrationRequest(t, mux, "GET", route, nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte("manual-handoff")) {
		t.Fatalf("empty codebuddy profile %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", route, map[string]any{"executable": "workbuddy.exe"})
	if code != http.StatusBadRequest {
		t.Fatalf("desktop executable accepted %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", route, map[string]any{"executable": path})
	if code != http.StatusOK || !bytes.Contains(body, []byte("configured")) {
		t.Fatalf("codebuddy profile failed %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/agents/codebuddy/probe", map[string]any{"confirm": false})
	if code != http.StatusBadRequest {
		t.Fatalf("unconfirmed probe %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", "/api/orchestration/agents/codebuddy/probe", map[string]any{"confirm": true})
	if code != http.StatusOK || !bytes.Contains(body, []byte("2.180.0-test")) || !bytes.Contains(body, []byte(`"autoRunnable":false`)) {
		t.Fatalf("probe improperly enabled CodeBuddy execution %d %s", code, body)
	}
	webOnly := http.NewServeMux()
	mountOrchestrationRoutes(webOnly, func() (*orchestration.Store, error) { return s, nil }, true)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", route, nil},
		{"POST", route, map[string]any{"executable": path}},
		{"POST", "/api/orchestration/agents/codebuddy/probe", map[string]any{"confirm": true}},
	} {
		code, body = orchestrationRequest(t, webOnly, tc.method, tc.path, tc.body)
		if code != http.StatusForbidden {
			t.Fatalf("web mode accessed CodeBuddy %s %s: %d %s", tc.method, tc.path, code, body)
		}
	}
	req := httptest.NewRequest("POST", route, bytes.NewBufferString(`{"executable":"malicious"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://untrusted.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("cross-site changed CodeBuddy config %d", rw.Code)
	}
}

func TestCodeBuddyModelProbeExplicitDesktopOnly(t *testing.T) {
	s, err := orchestration.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := "/api/orchestration/agents/codebuddy/model/check"
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return s, nil }, false)
	code, body := orchestrationRequest(t, mux, "POST", path, map[string]any{"confirm": false})
	if code != http.StatusBadRequest {
		t.Fatalf("unconfirmed model check %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", path, map[string]any{"confirm": true})
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"status":"unconfigured"`)) {
		t.Fatalf("without CLI config should not probe model %d %s", code, body)
	}
	webOnly := http.NewServeMux()
	mountOrchestrationRoutes(webOnly, func() (*orchestration.Store, error) { return s, nil }, true)
	code, body = orchestrationRequest(t, webOnly, "POST", path, map[string]any{"confirm": true})
	if code != http.StatusForbidden {
		t.Fatalf("web accessed model check %d %s", code, body)
	}
	req := httptest.NewRequest("POST", path, bytes.NewBufferString(`{"confirm":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://untrusted.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("cross-site triggered model check %d", rw.Code)
	}
}

func TestGitHandoffPreviewReceiptAndDesktopGuards(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	for _, args := range [][]string{
		{"init", "-b", "main", repo},
		{"-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial"},
	} {
		if out, err := proc.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	s, err := orchestration.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.CreateProject(orchestration.Project{Name: "fixture", RepoPath: repo, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees")})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(orchestration.Task{ProjectID: p.ID, Title: "ZCode reviewer", Agent: "ZCode"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateWorktree(task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = s.SetTaskStatus(task.ID, task.Revision, "ready")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return s, nil }, false)
	path := "/api/orchestration/tasks/" + task.ID + "/handoff"
	code, body := orchestrationRequest(t, mux, "GET", path, nil)
	var snapshot orchestration.HandoffPreview
	if code != http.StatusOK || json.Unmarshal(body, &snapshot) != nil || !snapshot.Ready {
		t.Fatalf("handoff preview %d %+v %s", code, snapshot, body)
	}
	receiptsPath := "/api/orchestration/tasks/" + task.ID + "/handoffs"
	code, body = orchestrationRequest(t, mux, "POST", receiptsPath, map[string]any{"revision": snapshot.Revision, "fingerprint": snapshot.Fingerprint, "confirm": false})
	if code != http.StatusBadRequest {
		t.Fatalf("unconfirmed handoff accepted %d %s", code, body)
	}
	wrongHashPrefix := "a"
	if snapshot.Fingerprint[0] == 'a' {
		wrongHashPrefix = "b"
	}
	code, body = orchestrationRequest(t, mux, "POST", receiptsPath, map[string]any{"revision": snapshot.Revision, "fingerprint": wrongHashPrefix + snapshot.Fingerprint[1:], "confirm": true})
	if code != http.StatusConflict {
		t.Fatalf("stale handoff hash accepted %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", receiptsPath, map[string]any{"revision": snapshot.Revision, "fingerprint": snapshot.Fingerprint, "confirm": true})
	if code != http.StatusCreated {
		t.Fatalf("valid handoff not recorded %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", receiptsPath, nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte(snapshot.Fingerprint)) {
		t.Fatalf("handoff not persisted %d %s", code, body)
	}
	ciCheck := "/api/orchestration/tasks/" + task.ID + "/handoff/ci-check"
	ciProofs := "/api/orchestration/tasks/" + task.ID + "/handoff/ci-proofs"
	code, body = orchestrationRequest(t, mux, "POST", ciCheck, map[string]any{"confirm": false})
	if code != http.StatusBadRequest {
		t.Fatalf("unconfirmed CI check accepted %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", ciCheck, map[string]any{"confirm": true})
	if code != http.StatusOK || !bytes.Contains(body, []byte("unsupported_remote")) {
		t.Fatalf("non-GitHub repo marked verified %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "GET", ciProofs, nil)
	if code != http.StatusOK || string(bytes.TrimSpace(body)) != "[]" {
		t.Fatalf("unexpected CI proofs %d %s", code, body)
	}
	code, body = orchestrationRequest(t, mux, "POST", ciProofs, map[string]any{"confirm": true, "revision": snapshot.Revision, "fingerprint": snapshot.Fingerprint})
	if code != http.StatusConflict {
		t.Fatalf("unsupported repo got CI attestation %d %s", code, body)
	}
	browser := http.NewServeMux()
	mountOrchestrationRoutes(browser, func() (*orchestration.Store, error) { return s, nil }, true)
	for _, tc := range []struct {
		method, path string
		payload      any
	}{
		{"GET", path, nil}, {"GET", receiptsPath, nil}, {"GET", ciProofs, nil},
		{"POST", ciCheck, map[string]any{"confirm": true}},
		{"POST", ciProofs, map[string]any{"confirm": true, "revision": snapshot.Revision, "fingerprint": snapshot.Fingerprint}},
		{"POST", receiptsPath, map[string]any{"revision": snapshot.Revision, "fingerprint": snapshot.Fingerprint, "confirm": true}},
	} {
		code, body = orchestrationRequest(t, browser, tc.method, tc.path, tc.payload)
		if code != http.StatusForbidden {
			t.Fatalf("web accessed handoff %s %d %s", tc.method, code, body)
		}
	}
	req := httptest.NewRequest("POST", receiptsPath, bytes.NewBufferString(`{"confirm":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://untrusted.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("cross-site handoff write allowed %d", rw.Code)
	}
}

func TestGitHubCIRoutesRequireSameOrigin(t *testing.T) {
	s, err := orchestration.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mux := http.NewServeMux()
	mountOrchestrationRoutes(mux, func() (*orchestration.Store, error) { return s, nil }, false)
	for _, route := range []string{
		"/api/orchestration/tasks/dummy/handoff/ci-check",
		"/api/orchestration/tasks/dummy/handoff/ci-proofs",
	} {
		req := httptest.NewRequest("POST", route, bytes.NewBufferString(`{"confirm":true}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://untrusted.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		if rw.Code != http.StatusForbidden {
			t.Fatalf("cross-site CI action accepted %s status=%d", route, rw.Code)
		}
	}
}
