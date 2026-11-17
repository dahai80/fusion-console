package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dahai80/fusion-console/internal/config"
)

func TestWriteAtlasProviderMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATLAS_CONFIG_DIR", dir)
	// Pre-existing user settings must survive the merge.
	existing := map[string]any{
		"defaultRole": "small",
		"providers": map[string]any{
			"mine": map[string]any{"baseURL": "http://keep.example/v1", "apiKey": "k", "api": "openai-completions", "models": []any{map[string]any{"id": "keep-1"}}},
		},
		"modelRoles": map[string]any{
			"premium": map[string]any{"provider": "mine", "models": []any{"mine/keep-1"}},
		},
	}
	data, _ := json.Marshal(existing)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	prov := &config.Provider{ID: "iff-local", Type: config.ProviderOpenAICompat, BaseURL: "http://127.0.0.1:8999/v1/", Model: "fabric-small", Models: []string{"fabric-small", "fabric-fast"}, APIKeyID: "iff-key", Enabled: true}
	t.Setenv("FUSION_CONSOLE_APIKEY_IFF_KEY", "sk-test-iff")

	ref, err := writeAtlasProvider(prov, "fabric-small")
	if err != nil {
		t.Fatalf("writeAtlasProvider: %v", err)
	}
	if ref != "iff-local/fabric-small" {
		t.Fatalf("model ref wrong: %q", ref)
	}

	var out map[string]any
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("settings missing: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("settings unparsable: %v", err)
	}
	providers := out["providers"].(map[string]any)
	if _, ok := providers["mine"]; !ok {
		t.Error("user provider entry was dropped")
	}
	mine := providers["iff-local"].(map[string]any)
	if mine["baseURL"] != "http://127.0.0.1:8999/v1" {
		t.Errorf("baseURL trailing slash not trimmed: %v", mine["baseURL"])
	}
	if mine["apiKey"] != "sk-test-iff" {
		t.Errorf("apiKey not resolved from keyring env: %v", mine["apiKey"])
	}
	roles := out["modelRoles"].(map[string]any)
	premium := roles["premium"].(map[string]any)
	if premium["provider"] != "iff-local" {
		t.Errorf("role provider wrong: %v", premium["provider"])
	}
	refs := premium["models"].([]any)
	if refs[0] != "iff-local/fabric-small" || refs[1] != "iff-local/fabric-fast" {
		t.Errorf("role refs wrong (bare ids are not normalized by atlascode): %v", refs)
	}
	if out["defaultRole"] != "small" {
		t.Errorf("defaultRole must be preserved: %v", out["defaultRole"])
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json.fc-backup")); err != nil {
		t.Error("first write must keep a backup of the original settings")
	}
}

func TestProviderEnvAtlascodeUsesSettingsWriter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATLAS_CONFIG_DIR", dir)
	t.Setenv("FUSION_CONSOLE_APIKEY_IFF_KEY", "sk-test-iff")
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	s.cfg.Provider = []config.Provider{
		{ID: "iff-local", Type: config.ProviderOpenAICompat, BaseURL: "http://127.0.0.1:8999/v1", Model: "fabric-small", APIKeyID: "iff-key", Enabled: true},
	}
	s.cfg.Agent = []config.AgentSettings{
		{Name: "atlascode", Command: "atlascode", Enabled: true, ProviderID: "iff-local", MaxBudgetTokens: 2_000_000},
	}

	env, model, _ := s.providerEnv("atlascode", nil)
	if env == nil || env["PROVIDER_ID"] != "iff-local" {
		t.Fatalf("atlascode env wrong: %v", env)
	}
	// No OPENAI_* injection for atlascode: settings.json is the forcing path.
	if _, ok := env["OPENAI_BASE_URL"]; ok {
		t.Errorf("OPENAI_BASE_URL must not be injected for atlascode: %v", env)
	}
	if model != "iff-local/fabric-small" {
		t.Fatalf("model ref wrong: %q", model)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
}
