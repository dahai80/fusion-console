package config

import (
	"os"
	"strings"
	"testing"
)

func TestCavemanDefaultsOnWhenSectionAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Orchestrator.CavemanEnabled() {
		t.Error("caveman must default to on")
	}
}

func TestCavemanDefaultsOnWithLegacySection(t *testing.T) {
	// An existing orchestrator section without caveman keys keeps the
	// default-on behavior.
	dir := t.TempDir()
	cfgJSON := `{"orchestrator":{"mode":"auto","configured":true}}`
	if err := os.WriteFile(Path(dir), []byte(cfgJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Orchestrator.CavemanEnabled() {
		t.Error("legacy section without caveman keys must default to on")
	}
	if cfg.Orchestrator.CavemanLevel != "full" {
		t.Errorf("level default = %q, want full", cfg.Orchestrator.CavemanLevel)
	}
}

func TestCavemanToggleOffHonored(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{"orchestrator":{"mode":"auto","caveman_default":false,"caveman_level":"lite"}}`
	if err := os.WriteFile(Path(dir), []byte(cfgJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Orchestrator.CavemanEnabled() {
		t.Error("explicit caveman_default=false must stay off")
	}
	if cfg.Orchestrator.CavemanLevel != "lite" {
		t.Errorf("level = %q, want lite", cfg.Orchestrator.CavemanLevel)
	}
}

func TestCavemanToggleRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	off := false
	cfg.Orchestrator.CavemanDefault = &off
	cfg.Orchestrator.CavemanLevel = "ultra"
	if err := cfg.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `"caveman_default": false`) {
		t.Errorf("persisted config missing caveman_default=false: %s", data)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Orchestrator.CavemanEnabled() || reloaded.Orchestrator.CavemanLevel != "ultra" {
		t.Error("round trip lost caveman settings")
	}
}
