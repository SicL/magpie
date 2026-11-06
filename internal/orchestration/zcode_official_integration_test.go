package orchestration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// Optional integration with the actual ZCode CLI: fixed local fake model.
// It is not evidence of real LLM inference or remote provider credentials.
func TestOfficialZCodeLocalProviderThroughMagpie(t *testing.T) {
	binary := os.Getenv("MAGPIE_ZCODE_CLI_INTEGRATION")
	if binary == "" {
		t.Skip("standalone CLI integration not requested")
	}
	if _, err := validateZCodeBinary(binary); err != nil {
		t.Fatalf("invalid CLI fixture: %v", err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"object":"list","data":[{"id":"magpie-smoke-model","object":"model","created":0,"owned_by":"local-test"}]}`)
		case "/v1/chat/completions":
			calls.Add(1)
			var in struct {
				Stream bool `json:"stream"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&in); err != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			if in.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				fmt.Fprintln(w, "data: "+`{"id":"chatcmpl-test","object":"chat.completion.chunk","created":1790000000,"model":"magpie-smoke-model","choices":[{"index":0,"delta":{"role":"assistant","content":"MAGPIE_ZCODE_PLAN_OK"},"finish_reason":null}]}`)
				fmt.Fprintln(w)
				fmt.Fprintln(w, "data: "+`{"id":"chatcmpl-test","object":"chat.completion.chunk","created":1790000000,"model":"magpie-smoke-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":5,"total_tokens":13}}`)
				fmt.Fprintln(w)
				fmt.Fprintln(w, "data: [DONE]")
				fmt.Fprintln(w)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id":"chatcmpl-test","object":"chat.completion","created":1790000000,"model":"magpie-smoke-model","choices":[{"index":0,"message":{"role":"assistant","content":"MAGPIE_ZCODE_PLAN_OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":5,"total_tokens":13}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	s, runner, parent := setupRunner(t, "complete")
	if _, err := s.SetZCodeCLI(binary); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetZCodeLocalModel(server.URL+"/v1", "magpie-smoke-model"); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(Task{ProjectID: parent.ProjectID, Title: "Local model integration test", Agent: "ZCode", Details: "Only request analysis from a local fake API"})
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
	verified, err := s.VerifyZCodeModel(task.ID, task.Revision)
	if err != nil || !verified.Verified {
		t.Fatalf("official CLI canary failed %+v %v", verified, err)
	}
	run, err := runner.StartZCodePlan(task.ID, task.Revision)
	if err != nil {
		t.Fatal(err)
	}
	runner.wait.Wait()
	result, err := s.Run(run.ID)
	if err != nil || result.State != "completed" {
		t.Fatalf("official CLI Run %+v %v", result, err)
	}
	log, err := s.RunLog(run.ID)
	if err != nil || !strings.Contains(log, "MAGPIE_ZCODE_PLAN_OK") {
		t.Fatalf("fake model reply missing: %v %s", err, log)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected two local model requests, got %d", calls.Load())
	}
}
