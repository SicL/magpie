package gui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

func mountOrchestrationRoutes(mux *http.ServeMux, store func() (*orchestration.Store, error), readOnly bool) {
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
