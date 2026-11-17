package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestClaudeSettingsOverride(t *testing.T) {
	// No ANTHROPIC_BASE_URL → no settings file, no-op cleanup.
	path, cleanup, err := claudeSettingsOverride(map[string]string{"PROVIDER_ID": "x"})
	if err != nil || path != "" {
		t.Fatalf("expected no-op, got path=%q err=%v", path, err)
	}
	cleanup()

	// ANTHROPIC_* env present → settings file carries them in an env block;
	// non-ANTHROPIC keys are not leaked into the file.
	env := map[string]string{
		"ANTHROPIC_BASE_URL":   "http://127.0.0.1:8999",
		"ANTHROPIC_AUTH_TOKEN": "sk-test",
		"ANTHROPIC_MODEL":      "fabric-small",
		"PROVIDER_ID":          "iff-anthropic",
		"OPENAI_BASE_URL":      "http://elsewhere",
	}
	path, cleanup, err = claudeSettingsOverride(env)
	if err != nil || path == "" {
		t.Fatalf("expected settings file, got path=%q err=%v", path, err)
	}
	defer cleanup()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings file missing: %v", err)
	}
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	envBlock, _ := doc["env"].(map[string]any)
	if envBlock["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8999" ||
		envBlock["ANTHROPIC_MODEL"] != "fabric-small" {
		t.Fatalf("env block wrong: %v", envBlock)
	}
	if _, ok := envBlock["PROVIDER_ID"]; ok {
		t.Fatalf("non-ANTHROPIC key leaked into settings env block")
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove settings file")
	}
}

func TestCodexHomeOverride(t *testing.T) {
	path, cleanup, err := codexHomeOverride(map[string]string{"PROVIDER_ID": "x"})
	if err != nil || path != "" {
		t.Fatalf("expected no-op, got path=%q err=%v", path, err)
	}
	cleanup()

	env := map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:8999/v1",
		"OPENAI_API_KEY":  "sk-test",
	}
	home, cleanup, err := codexHomeOverride(env)
	if err != nil || home == "" {
		t.Fatalf("expected codex home, got home=%q err=%v", home, err)
	}
	defer cleanup()
	raw, _ := os.ReadFile(home + "/config.toml")
	cfg := string(raw)
	for _, want := range []string{
		`base_url = "http://127.0.0.1:8999/v1"`,
		`env_key = "OPENAI_API_KEY"`,
		`wire_api = "responses"`,
		`model_provider = "macfabric"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config.toml missing %q:\n%s", want, cfg)
		}
	}
	cleanup()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove codex home")
	}
}
