package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 11480 || cfg.Server.Host != "127.0.0.1" {
		t.Errorf("defaults wrong: %+v", cfg.Server)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Server: ServerConfig{Host: "127.0.0.1", Port: 11480, DataDir: dir}}
	cfg.Users = []UserEntry{{Name: "admin", PasswordHash: "x"}}
	cfg.Provider = []Provider{{ID: "gw", Type: ProviderGateway, BaseURL: "http://127.0.0.1:11432/v1", Model: "glm-5", APIKeyID: "gw-main", Enabled: true}}
	cfg.Agent = DefaultAgents()
	if err := cfg.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Provider) != 1 || got.Provider[0].Model != "glm-5" || len(got.Agent) != len(DefaultAgents()) {
		t.Errorf("round trip mismatch: %+v", got)
	}
	var atlas AgentSettings
	for _, a := range got.Agent {
		if a.Name == "atlascode" {
			atlas = a
		}
	}
	if atlas.Command != "atlascode" || atlas.MaxBudgetTokens != 2_000_000 || !atlas.Builtin {
		t.Errorf("atlascode default entry wrong: %+v", atlas)
	}
	info, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config file perms = %v, want 0600", info.Mode().Perm())
	}
}

func TestMergeBuiltinAgentsAdditive(t *testing.T) {
	// A pre-atlascode config must gain the new built-ins on Load without
	// touching user-edited fields or disabled entries.
	dir := t.TempDir()
	cfg := &Config{Server: ServerConfig{Host: "127.0.0.1", Port: 11480, DataDir: dir}}
	cfg.Agent = []AgentSettings{
		{Name: "claude", Command: "/opt/claude-wrapper", PermissionMode: "plan", Enabled: false, MaxBudgetUSD: 9, Builtin: true},
	}
	if err := cfg.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var claude, atlascode int
	for _, a := range got.Agent {
		switch a.Name {
		case "claude":
			claude++
			if a.Command != "/opt/claude-wrapper" || a.Enabled || a.MaxBudgetUSD != 9 {
				t.Errorf("user-edited entry was overwritten: %+v", a)
			}
		case "atlascode":
			atlascode++
		}
	}
	if claude != 1 || atlascode != 1 {
		t.Errorf("merge wrong: claude=%d atlascode=%d agents=%+v", claude, atlascode, got.Agent)
	}
}

func TestGetAPIKeyEnvOverride(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_APIKEY_TEST_KEY", "env-secret")
	got, err := GetAPIKey("test-key")
	if err != nil || got != "env-secret" {
		t.Errorf("env override failed: %q %v", got, err)
	}
}

func TestUserLookup(t *testing.T) {
	cfg := &Config{Users: []UserEntry{{Name: "admin", PasswordHash: "h"}}}
	if _, ok := cfg.User("admin"); !ok {
		t.Error("admin not found")
	}
	if _, ok := cfg.User("nobody"); ok {
		t.Error("nobody must not be found")
	}
}

func TestSanitizeEnvSuffix(t *testing.T) {
	if got := sanitizeEnvSuffix("gw.main-1"); got != "GW_MAIN_1" {
		t.Errorf("sanitize = %q", got)
	}
	_ = filepath.Join // keep import stable across toolchains
	_ = strings.TrimSpace
}
