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
	if len(got.Provider) != 1 || got.Provider[0].Model != "glm-5" || len(got.Agent) != 4 {
		t.Errorf("round trip mismatch: %+v", got)
	}
	info, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config file perms = %v, want 0600", info.Mode().Perm())
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
