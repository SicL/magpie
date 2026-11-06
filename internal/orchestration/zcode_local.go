package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ZCodeLocalModel is an explicitly selected, NO-CREDENTIALS, loopback-only
// OpenAI Chat Completions provider. No external endpoint or API key may be
// configured through this workflow.
type ZCodeLocalModel struct {
	BaseURL    string `json:"baseUrl"`
	ModelID    string `json:"modelId"`
	Configured bool   `json:"configured"`
}

type zcodeProviderFile struct {
	SchemaVersion int `json:"schemaVersion"`
	Config        struct {
		ProviderConfigRules struct {
			ProviderRules []struct {
				ProviderID   string `json:"providerId"`
				ProviderName string `json:"providerName"`
				Config       struct {
					Group  string `json:"group"`
					Access struct {
						Type   string `json:"type"`
						APIKey string `json:"apiKey"`
					} `json:"access"`
					API struct {
						Type    string `json:"type"`
						BaseURL string `json:"baseUrl"`
					} `json:"api"`
					PersonalModelIDs []string `json:"personalModelIds"`
				} `json:"config"`
			} `json:"providerRules"`
		} `json:"providerConfigRules"`
		ModelConfigRules struct {
			ProviderModelRules       []any `json:"providerModelRules"`
			ManualProviderModelRules []any `json:"manualProviderModelRules"`
		} `json:"modelConfigRules"`
		DefaultModelSelection struct {
			ProviderID string `json:"providerId"`
			ModelID    string `json:"modelId"`
		} `json:"defaultModelSelection"`
	} `json:"config"`
}

const localZCodeProviderID = "magpie-local-loopback"

func validateZCodeLocalModel(baseURL, modelID string) (ZCodeLocalModel, error) {
	baseURL = strings.TrimSpace(baseURL)
	modelID = strings.TrimSpace(modelID)
	if len(baseURL) == 0 || len(baseURL) > 256 || len(modelID) == 0 || len(modelID) > 128 {
		return ZCodeLocalModel{}, ErrInvalid
	}
	u, err := url.Parse(baseURL)
	if err != nil || u == nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" || u.RawPath != "" || u.ForceQuery {
		return ZCodeLocalModel{}, fmt.Errorf("%w: local model endpoint must use http://127.0.0.1 or http://[::1] without query or credentials", ErrInvalid)
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "::1" {
		return ZCodeLocalModel{}, fmt.Errorf("%w: only explicit loopback IP addresses are permitted", ErrInvalid)
	}
	if u.Port() == "" {
		return ZCodeLocalModel{}, fmt.Errorf("%w: explicit model server port is required", ErrInvalid)
	}
	if !validModelPort(u.Port()) {
		return ZCodeLocalModel{}, ErrInvalid
	}
	if u.Path != "/v1" && u.Path != "/v1/" {
		return ZCodeLocalModel{}, fmt.Errorf("%w: local model endpoint path must be /v1", ErrInvalid)
	}
	for _, c := range modelID {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '/' || c == ':') {
			return ZCodeLocalModel{}, fmt.Errorf("%w: invalid model id", ErrInvalid)
		}
	}
	return ZCodeLocalModel{BaseURL: u.String(), ModelID: modelID, Configured: true}, nil
}

func validModelPort(s string) bool {
	var n int
	if len(s) < 1 || len(s) > 5 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n > 0 && n <= 65535
}

func (s *Store) zcodeLocalFile() string {
	return filepath.Join(s.dir, "isolated-zcode", "magpie-local-provider.json")
}

func (s *Store) GetZCodeLocalModel() (ZCodeLocalModel, error) {
	path := s.zcodeLocalFile()
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ZCodeLocalModel{}, nil
	}
	if err != nil {
		return ZCodeLocalModel{}, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 8192 {
		return ZCodeLocalModel{}, fmt.Errorf("%w: model profile is not a small regular file", ErrConflict)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ZCodeLocalModel{}, err
	}
	var v zcodeProviderFile
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&v); err != nil {
		return ZCodeLocalModel{}, fmt.Errorf("%w: invalid local model profile", ErrConflict)
	}
	var extra any
	if e := decoder.Decode(&extra); e != io.EOF {
		return ZCodeLocalModel{}, fmt.Errorf("%w: additional provider content", ErrConflict)
	}
	rules := v.Config.ProviderConfigRules.ProviderRules
	if v.SchemaVersion != 1 || len(rules) != 1 || rules[0].ProviderID != localZCodeProviderID ||
		rules[0].ProviderName != "Magpie local model" || rules[0].Config.Group != "standard-personal" ||
		len(v.Config.ModelConfigRules.ProviderModelRules) != 0 || len(v.Config.ModelConfigRules.ManualProviderModelRules) != 0 ||
		rules[0].Config.API.Type != "openai-chat-completions" || v.Config.DefaultModelSelection.ProviderID != localZCodeProviderID ||
		rules[0].Config.Access.APIKey != "local-no-key" || rules[0].Config.Access.Type != "api-key" ||
		len(rules[0].Config.PersonalModelIDs) != 1 || rules[0].Config.PersonalModelIDs[0] != v.Config.DefaultModelSelection.ModelID {
		return ZCodeLocalModel{}, fmt.Errorf("%w: model profile has unsupported or altered fields", ErrConflict)
	}
	return validateZCodeLocalModel(rules[0].Config.API.BaseURL, v.Config.DefaultModelSelection.ModelID)
}

// SetZCodeLocalModel changes only Magpie's own loopback-only provider file;
// it never overwrites the user's standalone ZCode profile or desktop login.
// A file that has been manually modified or corrupted is not overwritten.
func (s *Store) SetZCodeLocalModel(baseURL, modelID string) (ZCodeLocalModel, error) {
	input, err := validateZCodeLocalModel(baseURL, modelID)
	if err != nil {
		return ZCodeLocalModel{}, err
	}
	var active int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM agent_runs WHERE adapter=? AND state IN ('starting','running','stopping','interrupted')", adapterZCodePlan).Scan(&active); err != nil {
		return ZCodeLocalModel{}, err
	}
	if active > 0 {
		return ZCodeLocalModel{}, fmt.Errorf("%w: stop or reconcile ZCode Runs before changing shared model", ErrConflict)
	}
	if _, err = s.GetZCodeLocalModel(); err != nil {
		return ZCodeLocalModel{}, err
	}
	var p zcodeProviderFile
	p.SchemaVersion = 1
	p.Config.ProviderConfigRules.ProviderRules = make([]struct {
		ProviderID   string `json:"providerId"`
		ProviderName string `json:"providerName"`
		Config       struct {
			Group  string `json:"group"`
			Access struct {
				Type   string `json:"type"`
				APIKey string `json:"apiKey"`
			} `json:"access"`
			API struct {
				Type    string `json:"type"`
				BaseURL string `json:"baseUrl"`
			} `json:"api"`
			PersonalModelIDs []string `json:"personalModelIds"`
		} `json:"config"`
	}, 1)
	rule := &p.Config.ProviderConfigRules.ProviderRules[0]
	rule.ProviderID = localZCodeProviderID
	rule.ProviderName = "Magpie local model"
	rule.Config.Group = "standard-personal"
	rule.Config.Access.Type = "api-key"
	rule.Config.Access.APIKey = "local-no-key" // Non-secret placeholder; local server need not verify it.
	rule.Config.API.Type = "openai-chat-completions"
	rule.Config.API.BaseURL = input.BaseURL
	rule.Config.PersonalModelIDs = []string{input.ModelID}
	p.Config.ModelConfigRules.ProviderModelRules = []any{}
	p.Config.ModelConfigRules.ManualProviderModelRules = []any{}
	p.Config.DefaultModelSelection.ProviderID = localZCodeProviderID
	p.Config.DefaultModelSelection.ModelID = input.ModelID
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return ZCodeLocalModel{}, err
	}
	dir := filepath.Dir(s.zcodeLocalFile())
	if err = os.MkdirAll(dir, 0700); err != nil {
		return ZCodeLocalModel{}, err
	}
	f, err := os.CreateTemp(dir, ".magpie-zcode-model-*.tmp")
	if err != nil {
		return ZCodeLocalModel{}, err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return ZCodeLocalModel{}, err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return ZCodeLocalModel{}, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return ZCodeLocalModel{}, err
	}
	if err = f.Close(); err != nil {
		return ZCodeLocalModel{}, err
	}
	if err = os.Rename(f.Name(), s.zcodeLocalFile()); err != nil {
		return ZCodeLocalModel{}, err
	}
	// Persisted canaries for any other model selection are no longer valid.
	_, err = s.db.Exec("DELETE FROM zcode_plan_verifications")
	return input, err
}

func (s *Store) zcodeModelFingerprint(path string) (string, error) {
	exe, err := executableDigest(path)
	if err != nil {
		return "", err
	}
	model, err := s.GetZCodeLocalModel()
	if err != nil {
		return "", err
	}
	if !model.Configured {
		return exe, nil
	}
	b, err := os.ReadFile(s.zcodeLocalFile())
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(exe+":"), b...))
	return hex.EncodeToString(sum[:]), nil
}

func (s *Store) zcodeEnvironment(home string) ([]string, error) {
	env := zcodeSafeEnvironment(home)
	model, err := s.GetZCodeLocalModel()
	if err != nil {
		return nil, err
	}
	if model.Configured {
		env = append(env, "ZCODE_PERSONAL_PROVIDER_CONFIG_FILE="+s.zcodeLocalFile())
	}
	return env, nil
}
