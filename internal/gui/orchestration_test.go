package gui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
