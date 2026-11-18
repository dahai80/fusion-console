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

func TestValidateProvider(t *testing.T) {
	base := Provider{ID: "src", Name: "src", Type: ProviderOpenAICompat, BaseURL: "http://h:1/v1", Enabled: true}

	// Normalization: trailing slash trimmed; no note for /v1-terminated URL.
	p := base
	note, err := ValidateProvider(&p)
	if err != nil || p.BaseURL != "http://h:1/v1" || note != "" {
		t.Fatalf("clean openai provider: note=%q err=%v url=%q", note, err, p.BaseURL)
	}

	// OpenAI without /v1: allowed with a note.
	p = base
	p.BaseURL = "http://h:8999/"
	note, err = ValidateProvider(&p)
	if err != nil || note == "" || p.BaseURL != "http://h:8999" {
		t.Fatalf("openai without /v1: note=%q err=%v", note, err)
	}

	// Anthropic with /v1 must be rejected (protocol appends the path itself).
	p = base
	p.Type = ProviderAnthropic
	p.BaseURL = "https://api.anthropic.com/v1"
	if _, err := ValidateProvider(&p); err == nil {
		t.Fatal("anthropic /v1 suffix must error")
	}

	// Bad scheme, empty id, bad id charset, duplicate pool entry.
	p = base
	p.BaseURL = "ftp://h"
	if _, err := ValidateProvider(&p); err == nil {
		t.Fatal("bad scheme must error")
	}
	p = base
	p.ID = ""
	if _, err := ValidateProvider(&p); err == nil {
		t.Fatal("empty id must error")
	}
	p = base
	p.ID = "Bad ID"
	if _, err := ValidateProvider(&p); err == nil {
		t.Fatal("id charset must error")
	}
	p = base
	p.Models = []string{"m1", "m1"}
	if _, err := ValidateProvider(&p); err == nil {
		t.Fatal("duplicate pool entry must error")
	}
}

func TestChatDefaultConfig(t *testing.T) {
	cfg := &Config{Chat: ChatConfig{DefaultProviderID: "iff", DefaultModel: "fabric-small"}}
	if cfg.Chat.DefaultModel != "fabric-small" {
		t.Fatal("chat default not stored")
	}
}

func TestMergeBuiltinAgentsMigratesLegacyFusionCommand(t *testing.T) {
	// Configs seeded when the fusion-code agent pointed at the "fusion"
	// wrapper must migrate to the "fusion-code" binary; an explicit
	// non-legacy command stays untouched.
	dir := t.TempDir()
	cfg := &Config{Server: ServerConfig{Host: "127.0.0.1", Port: 11480, DataDir: dir}}
	cfg.Agent = []AgentSettings{
		{Name: "fusion-code", Command: "fusion", PermissionMode: "bypassPermissions", Enabled: true, Builtin: true},
		{Name: "claude", Command: "/opt/claude-wrapper", Enabled: true, Builtin: true},
	}
	if err := cfg.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var fusion, claude string
	for _, a := range got.Agent {
		switch a.Name {
		case "fusion-code":
			fusion = a.Command
		case "claude":
			claude = a.Command
		}
	}
	if fusion != "fusion-code" {
		t.Errorf("legacy fusion command not migrated: %q", fusion)
	}
	if claude != "/opt/claude-wrapper" {
		t.Errorf("unrelated command was touched: %q", claude)
	}
}
