package orchestration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZCodeLoopbackModelRejectsForeignAndMalformedEndpoints(t *testing.T) {
	bad := []string{
		"https://api.example.com/v1", "http://example.com/v1", "http://127.0.0.2:1234/v1",
		"http://localhost:1234/v1", "http://127.0.0.1/v1", "http://127.0.0.1:65536/v1",
		"http://127.0.0.1:1234/v1?api=1", "http://user:secret@127.0.0.1:1234/v1",
		"file:///tmp/model", "http://127.0.0.1:1234/admin", "http://[::ffff:192.168.0.3]:1234/v1",
	}
	for _, endpoint := range bad {
		if _, err := validateZCodeLocalModel(endpoint, "test-model"); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted disallowed URL %s: %v", endpoint, err)
		}
	}
	if _, err := validateZCodeLocalModel("http://127.0.0.1:1234/v1", "bad model; command"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid model name accepted: %v", err)
	}
	for _, endpoint := range []string{"http://127.0.0.1:1234/v1", "http://[::1]:1234/v1"} {
		v, err := validateZCodeLocalModel(endpoint, "my-local-model")
		if err != nil || !v.Configured {
			t.Fatalf("loopback rejected %q %+v %v", endpoint, v, err)
		}
	}
}

func TestZCodeLocalModelFileRoundtripAndTamperFailClosed(t *testing.T) {
	s, _, _ := setupRunner(t, "complete")
	empty, err := s.GetZCodeLocalModel()
	if err != nil || empty.Configured {
		t.Fatalf("expected no model %+v %v", empty, err)
	}
	model, err := s.SetZCodeLocalModel("http://127.0.0.1:1234/v1", "qwen-local")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetZCodeLocalModel()
	if err != nil || got != model {
		t.Fatalf("roundtrip %+v %+v %v", got, model, err)
	}
	b, err := os.ReadFile(s.zcodeLocalFile())
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		SchemaVersion int `json:"schemaVersion"`
		Config        struct {
			DefaultModelSelection struct{ ProviderID, ModelID string } `json:"defaultModelSelection"`
		} `json:"config"`
	}
	if err = json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	if config.SchemaVersion != 1 || config.Config.DefaultModelSelection.ModelID != "qwen-local" ||
		config.Config.DefaultModelSelection.ProviderID != localZCodeProviderID {
		t.Fatalf("unrecognized ZCode config fields: %+v", config)
	}
	if strings.Contains(string(b), "OPENAI_API_KEY") || strings.Contains(string(b), "sk-") {
		t.Fatal("provider config may not contain secrets")
	}
	env, err := s.zcodeEnvironment(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range env {
		if v == "ZCODE_PERSONAL_PROVIDER_CONFIG_FILE="+s.zcodeLocalFile() {
			found = true
		}
	}
	if !found {
		t.Fatal("model file was not passed explicitly to isolated CLI")
	}
	mutated := strings.Replace(string(b), `"schemaVersion": 1`, `"schemaVersion": 1, "unrecognized":true`, 1)
	if mutated == string(b) {
		t.Fatal("schema header not found")
	}
	if err = os.WriteFile(s.zcodeLocalFile(), []byte(mutated), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetZCodeLocalModel(); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown field silently accepted %v", err)
	}
	if err = os.WriteFile(s.zcodeLocalFile(), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(s.zcodeLocalFile(), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetZCodeLocalModel(); !errors.Is(err, ErrConflict) {
		t.Fatalf("corrupt profile treated empty %v", err)
	}
	if _, err = s.SetZCodeLocalModel("http://127.0.0.1:1234/v1", "new-model"); !errors.Is(err, ErrConflict) {
		t.Fatalf("corrupted profile silently replaced: %v", err)
	}
}

func TestZCodeModelChangeInvalidatesSuccessfulCanary(t *testing.T) {
	s, _, parent := setupRunner(t, "complete")
	cli := createFakeZCode(t)
	if _, err := s.SetZCodeCLI(cli); err != nil {
		t.Fatal(err)
	}
	first, err := s.SetZCodeLocalModel("http://127.0.0.1:1234/v1", "model-a")
	if err != nil || !first.Configured {
		t.Fatal(err)
	}
	task, err := s.CreateTask(Task{ProjectID: parent.ProjectID, Title: "local model", Agent: "ZCode"})
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
	if _, err = s.VerifyZCodeModel(task.ID, task.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.requireZCodeVerified(task.ID, cli); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetZCodeLocalModel("http://127.0.0.1:1234/v1", "model-b"); err != nil {
		t.Fatal(err)
	}
	if err = s.requireZCodeVerified(task.ID, cli); !errors.Is(err, ErrConflict) {
		t.Fatalf("old canary allowed after model change: %v", err)
	}
}

func TestZCodeLocalModelChangesBlockedByUnresolvedRuns(t *testing.T) {
	s, _, parent := setupRunner(t, "complete")
	if _, err := s.SetZCodeLocalModel("http://127.0.0.1:1234/v1", "model-one"); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(Task{ProjectID: parent.ProjectID, Title: "unresolved local model run", Agent: "ZCode"})
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
	cwd, err := s.worktreeForTask(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.reserveRunAdapter(task, task.Revision, cwd, adapterZCodePlan, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetZCodeLocalModel("http://127.0.0.1:1234/v1", "model-two"); !errors.Is(err, ErrConflict) {
		t.Fatalf("active ZCode Run did not lock model configuration: %v", err)
	}
	model, err := s.GetZCodeLocalModel()
	if err != nil || model.ModelID != "model-one" {
		t.Fatalf("model changed after rejected update %+v %v", model, err)
	}
}
