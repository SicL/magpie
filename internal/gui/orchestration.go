package gui

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yetone/magpie/internal/orchestration"
	"github.com/yetone/magpie/internal/settings"
)

// A database is opened on the first request, not when Magpie starts.
// It is intentionally separate from Magpie's gateway/session stores.
func orchestrationRoutes(mux *http.ServeMux, w Windows) {
	var once sync.Once
	var store *orchestration.Store
	var openErr error
	getStore := func() (*orchestration.Store, error) {
		once.Do(func() {
			store, openErr = orchestration.Open(filepath.Join(settings.Dir(), "orchestration", "tasks.db"))
		})
		return store, openErr
	}
	mountOrchestrationRoutes(mux, getStore, isWeb(w))
}

func orchestrationFailure(rw http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, orchestration.ErrInvalid):
		code = http.StatusBadRequest
	case errors.Is(err, orchestration.ErrConflict):
		code = http.StatusConflict
	case errors.Is(err, orchestration.ErrNotFound):
		code = http.StatusNotFound
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(map[string]string{"error": err.Error()})
}

func orchestrationReadJSON(rw http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 32768))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.Join(orchestration.ErrInvalid, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.Join(orchestration.ErrInvalid, errors.New("trailing JSON data"))
	}
	return nil
}

func mountOrchestrationRoutes(mux *http.ServeMux, store func() (*orchestration.Store, error), readOnly bool, builders ...func(*orchestration.Store) *orchestration.RunService) {
	buildRunner := orchestration.NewRunService
	if len(builders) > 0 {
		buildRunner = builders[0]
	}
	db := func(rw http.ResponseWriter) (*orchestration.Store, bool) {
		s, err := store()
		if err != nil {
			orchestrationFailure(rw, err)
			return nil, false
		}
		return s, true
	}
	allowed := func(rw http.ResponseWriter) bool {
		if readOnly {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": "Project changes are only allowed in the desktop application"})
			return false
		}
		return true
	}

	// DSH execution is registered only in the desktop API. Browser access is
	// limited to metadata; task log content and all process mutation are local.
	var runnerOnce sync.Once
	var runner *orchestration.RunService
	runService := func(rw http.ResponseWriter) (*orchestration.RunService, bool) {
		s, ok := db(rw)
		if !ok {
			return nil, false
		}
		runnerOnce.Do(func() { runner = buildRunner(s) })
		return runner, true
	}
	// Agent execution is higher risk than a settings change. Require JSON
	// and reject cross-site browser origins and null origins. Same-origin desktop
	// fetch is allowed; a local command caller has the user's own OS rights.
	executionAllowed := func(rw http.ResponseWriter, r *http.Request) bool {
		if !allowed(rw) {
			return false
		}
		kind, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaErr != nil || kind != "application/json" {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusUnsupportedMediaType)
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": "application/json required"})
			return false
		}
		site := r.Header.Get("Sec-Fetch-Site")
		origin := r.Header.Get("Origin")
		reject := site == "cross-site" || origin == "null"
		if origin != "" && origin != "null" {
			parsed, e := url.Parse(origin)
			if e != nil || !strings.EqualFold(parsed.Host, r.Host) {
				reject = true
			}
		}
		if reject {
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": "cross-origin execution is forbidden"})
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/orchestration/agents/dsh", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, orchestration.DetectDSH())
	})
	mux.HandleFunc("GET /api/orchestration/runs", func(rw http.ResponseWriter, r *http.Request) {
		project := strings.TrimSpace(r.URL.Query().Get("project"))
		if project == "" {
			orchestrationFailure(rw, errors.Join(orchestration.ErrInvalid, errors.New("project is required")))
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		items, err := s.ProjectRuns(project)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, items)
	})
	mux.HandleFunc("GET /api/orchestration/runs/{id}", func(rw http.ResponseWriter, r *http.Request) {
		s, ok := db(rw)
		if !ok {
			return
		}
		item, err := s.Run(r.PathValue("id"))
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, item)
	})
	mux.HandleFunc("GET /api/orchestration/runs/{id}/log", func(rw http.ResponseWriter, r *http.Request) {
		if !allowed(rw) {
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		tail, err := s.RunLog(r.PathValue("id"))
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, map[string]string{"tail": tail})
	})
	mux.HandleFunc("POST /api/orchestration/tasks/{id}/run", func(rw http.ResponseWriter, r *http.Request) {
		if !executionAllowed(rw, r) {
			return
		}
		var input struct {
			Revision int64 `json:"revision"`
		}
		if err := orchestrationReadJSON(rw, r, &input); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		runner, ok := runService(rw)
		if !ok {
			return
		}
		item, err := runner.Start(r.PathValue("id"), input.Revision)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusCreated)
		writeJSON(rw, item)
	})
	mux.HandleFunc("POST /api/orchestration/runs/{id}/stop", func(rw http.ResponseWriter, r *http.Request) {
		if !executionAllowed(rw, r) {
			return
		}
		runner, ok := runService(rw)
		if !ok {
			return
		}
		if err := runner.Stop(r.PathValue("id")); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, map[string]bool{"stopping": true})
	})

	mux.HandleFunc("GET /api/orchestration/tasks/{id}/checkpoints", func(rw http.ResponseWriter, r *http.Request) {
		s, ok := db(rw)
		if !ok {
			return
		}
		rows, err := s.Checkpoints(r.PathValue("id"))
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, rows)
	})
	mux.HandleFunc("POST /api/orchestration/tasks/{id}/checkpoints", func(rw http.ResponseWriter, r *http.Request) {
		if !executionAllowed(rw, r) {
			return
		}
		var input struct {
			Revision   int64  `json:"revision"`
			Summary    string `json:"summary"`
			TestStatus string `json:"testStatus"`
			RunID      string `json:"runId"`
		}
		if err := orchestrationReadJSON(rw, r, &input); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		result, err := s.CreateCheckpoint(orchestration.CheckpointInput{TaskID: r.PathValue("id"), Revision: input.Revision, Summary: input.Summary, TestStatus: input.TestStatus, RunID: input.RunID})
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusCreated)
		writeJSON(rw, result)
	})
	mux.HandleFunc("GET /api/orchestration/tasks/{id}/recoveries", func(rw http.ResponseWriter, r *http.Request) {
		s, ok := db(rw)
		if !ok {
			return
		}
		items, err := s.Recoveries(r.PathValue("id"))
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, items)
	})
	mux.HandleFunc("POST /api/orchestration/tasks/{id}/resume", func(rw http.ResponseWriter, r *http.Request) {
		if !executionAllowed(rw, r) {
			return
		}
		var input struct {
			Revision    int64  `json:"revision"`
			SourceRunID string `json:"sourceRunId"`
		}
		if err := orchestrationReadJSON(rw, r, &input); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		runner, ok := runService(rw)
		if !ok {
			return
		}
		item, err := runner.Resume(r.PathValue("id"), input.Revision, input.SourceRunID)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusCreated)
		writeJSON(rw, item)
	})
	mux.HandleFunc("POST /api/orchestration/runs/{id}/reconcile", func(rw http.ResponseWriter, r *http.Request) {
		if !executionAllowed(rw, r) {
			return
		}
		var in orchestration.RecoveryInput
		if err := orchestrationReadJSON(rw, r, &in); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		runner, ok := runService(rw)
		if !ok {
			return
		}
		item, err := runner.ResolveInterrupted(r.PathValue("id"), in)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, item)
	})

	mux.HandleFunc("GET /api/orchestration/agents/capabilities", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, orchestration.AgentCapabilities())
	})
	mux.HandleFunc("GET /api/orchestration/dependencies", func(rw http.ResponseWriter, r *http.Request) {
		project := strings.TrimSpace(r.URL.Query().Get("project"))
		if project == "" {
			orchestrationFailure(rw, orchestration.ErrInvalid)
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		items, err := s.ProjectDependencies(project)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, items)
	})
	mux.HandleFunc("GET /api/orchestration/tasks/{id}/readiness", func(rw http.ResponseWriter, r *http.Request) {
		s, ok := db(rw)
		if !ok {
			return
		}
		result, err := s.TaskReadiness(r.PathValue("id"))
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, result)
	})
	mux.HandleFunc("POST /api/orchestration/tasks/{id}/dependencies", func(rw http.ResponseWriter, r *http.Request) {
		if !executionAllowed(rw, r) {
			return
		}
		var input struct {
			Revision        int64    `json:"revision"`
			PrerequisiteIDs []string `json:"prerequisiteIds"`
		}
		if err := orchestrationReadJSON(rw, r, &input); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		updated, err := s.ReplaceDependencies(r.PathValue("id"), input.Revision, input.PrerequisiteIDs)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, updated)
	})
	mux.HandleFunc("POST /api/orchestration/projects/{id}/dispatch", func(rw http.ResponseWriter, r *http.Request) {
		if !executionAllowed(rw, r) {
			return
		}
		var input struct {
			MaxRuns int `json:"maxRuns"`
		}
		if err := orchestrationReadJSON(rw, r, &input); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		runner, ok := runService(rw)
		if !ok {
			return
		}
		result, err := runner.DispatchReady(r.PathValue("id"), input.MaxRuns)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, result)
	})

	mux.HandleFunc("GET /api/orchestration/projects", func(rw http.ResponseWriter, r *http.Request) {
		s, ok := db(rw)
		if !ok {
			return
		}
		items, err := s.Projects()
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, items)
	})
	mux.HandleFunc("POST /api/orchestration/projects", func(rw http.ResponseWriter, r *http.Request) {
		if !allowed(rw) {
			return
		}
		var in orchestration.Project
		if err := orchestrationReadJSON(rw, r, &in); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		out, err := s.CreateProject(in)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusCreated)
		writeJSON(rw, out)
	})
	mux.HandleFunc("POST /api/orchestration/projects/{id}/remove", func(rw http.ResponseWriter, r *http.Request) {
		if !allowed(rw) {
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		if err := s.DeleteProject(r.PathValue("id")); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		rw.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/orchestration/tasks", func(rw http.ResponseWriter, r *http.Request) {
		project := strings.TrimSpace(r.URL.Query().Get("project"))
		if project == "" {
			orchestrationFailure(rw, errors.Join(orchestration.ErrInvalid, errors.New("project is required")))
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		out, err := s.Tasks(project)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("POST /api/orchestration/tasks", func(rw http.ResponseWriter, r *http.Request) {
		if !allowed(rw) {
			return
		}
		var in orchestration.Task
		if err := orchestrationReadJSON(rw, r, &in); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		out, err := s.CreateTask(in)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusCreated)
		writeJSON(rw, out)
	})
	mux.HandleFunc("POST /api/orchestration/tasks/{id}/status", func(rw http.ResponseWriter, r *http.Request) {
		if !allowed(rw) {
			return
		}
		var in struct {
			Revision int64  `json:"revision"`
			Status   string `json:"status"`
		}
		if err := orchestrationReadJSON(rw, r, &in); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		out, err := s.SetTaskStatus(r.PathValue("id"), in.Revision, in.Status)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("GET /api/orchestration/worktrees", func(rw http.ResponseWriter, r *http.Request) {
		project := strings.TrimSpace(r.URL.Query().Get("project"))
		if project == "" {
			orchestrationFailure(rw, errors.Join(orchestration.ErrInvalid, errors.New("project is required")))
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		out, err := s.Worktrees(project)
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("POST /api/orchestration/tasks/{id}/worktree", func(rw http.ResponseWriter, r *http.Request) {
		if !allowed(rw) {
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		out, err := s.CreateWorktree(r.PathValue("id"))
		if err != nil {
			orchestrationFailure(rw, err)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusCreated)
		writeJSON(rw, out)
	})
	mux.HandleFunc("POST /api/orchestration/tasks/{id}/remove", func(rw http.ResponseWriter, r *http.Request) {
		if !allowed(rw) {
			return
		}
		s, ok := db(rw)
		if !ok {
			return
		}
		if err := s.DeleteTask(r.PathValue("id")); err != nil {
			orchestrationFailure(rw, err)
			return
		}
		rw.WriteHeader(http.StatusNoContent)
	})
}
