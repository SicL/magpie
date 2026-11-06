package orchestration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestZCodeLocalReadinessNeedsExplicitConfiguration(t *testing.T) {
	s, _, _ := setupRunner(t, "complete")
	result, err := s.ProbeZCodeLocalModel()
	if err != nil || result.Configured || result.Status != "unconfigured" {
		t.Fatalf("unexpected result %+v %v", result, err)
	}
}

func TestZCodeLocalReadinessValidatesExactModel(t *testing.T) {
	tests := []struct {
		name       string
		httpStatus int
		body       string
		want       string
		reachable  bool
		found      bool
	}{
		{"present", 200, `{"object":"list","data":[{"id":"other"},{"id":"qwen3-local"}]}`, "ready", true, true},
		{"missing", 200, `{"data":[{"id":"different"}]}`, "model_missing", true, false},
		{"serverError", 503, `{"error":"busy"}`, "unexpected_response", true, false},
		{"invalidJson", 200, "not-json", "unexpected_response", true, false},
		{"missingData", 200, `{"other":[]}`, "unexpected_response", true, false},
		{"trailingJson", 200, `{"data":[]}{"data":[]}`, "unexpected_response", true, false},
		{"oversize", 200, strings.Repeat("x", 65537), "unexpected_response", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" || r.URL.Path != "/v1/models" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.httpStatus)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			s, _, _ := setupRunner(t, "complete")
			if _, err := s.SetZCodeLocalModel(server.URL+"/v1", "qwen3-local"); err != nil {
				t.Fatal(err)
			}
			result, err := s.ProbeZCodeLocalModel()
			if err != nil || result.Status != tc.want || result.Reachable != tc.reachable || result.ModelFound != tc.found {
				t.Fatalf("response %+v error %v", result, err)
			}
			if requests.Load() != 1 {
				t.Fatalf("requests=%d, expected 1", requests.Load())
			}
		})
	}
}

func TestZCodeLocalReadinessRejectsRedirectsAndIgnoresProxy(t *testing.T) {
	var remote atomic.Int32
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { remote.Add(1); fmt.Fprintln(w, "unexpected") }))
	defer remoteServer.Close()
	var local atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		local.Add(1)
		http.Redirect(w, r, remoteServer.URL+"/outside", http.StatusFound)
	}))
	defer server.Close()
	t.Setenv("HTTP_PROXY", remoteServer.URL)
	t.Setenv("http_proxy", remoteServer.URL)
	s, _, _ := setupRunner(t, "complete")
	if _, err := s.SetZCodeLocalModel(server.URL+"/v1", "qwen3-local"); err != nil {
		t.Fatal(err)
	}
	result, err := s.ProbeZCodeLocalModel()
	if err != nil || !result.Reachable || result.ModelFound || result.Status != "unexpected_response" {
		t.Fatalf("redirected model probe %+v %v", result, err)
	}
	if local.Load() != 1 || remote.Load() != 0 {
		t.Fatalf("redirect or proxy escaped local server local=%d remote=%d", local.Load(), remote.Load())
	}
}

func TestZCodeLocalReadinessIsNotModelVerification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "qwen3-local"}}})
	}))
	defer server.Close()
	s, _, _ := setupRunner(t, "complete")
	if _, err := s.SetZCodeLocalModel(server.URL+"/v1", "qwen3-local"); err != nil {
		t.Fatal(err)
	}
	status, err := s.ProbeZCodeLocalModel()
	if err != nil || !status.ModelFound {
		t.Fatalf("readiness %+v %v", status, err)
	}
	// Listing a model must not create a task-specific canary record.
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM zcode_plan_verifications").Scan(&count); err != nil || count != 0 {
		t.Fatalf("model readiness unexpectedly authorized task execution: count=%d %v", count, err)
	}
}
