package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/config"
)

// cavemanFixtureSkill is a minimal SKILL.md shaped like the plugin's: YAML
// frontmatter, an intensity table with several levels, and per-level example
// lines.
const cavemanFixtureSkill = `---
name: caveman
description: Ultra-compressed communication mode.
---

Respond terse like smart caveman. All technical substance stay. Only fluff die.

## Intensity

| Level | What change |
|-------|------------|
| **lite** | Keep articles |
| **full** | Drop articles |
| **ultra** | One word when one word enough |

Example — "Why re-render?"
- lite: "Keep articles. useMemo."
- full: "Drop articles. useMemo."
- ultra: "useMemo."

## Boundaries

Code/commits/PRs: write normal.
`

func TestRenderCavemanRulesFiltersToLevel(t *testing.T) {
	out := renderCavemanRules(cavemanFixtureSkill, "full")
	for _, want := range []string{
		"Respond terse like smart caveman",
		"| **full** | Drop articles |",
		`- full: "Drop articles. useMemo."`,
		"## Boundaries",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("filtered rules missing %q", want)
		}
	}
	for _, gone := range []string{"**lite** | Keep articles", "**ultra** | One word", `- lite:`, `- ultra:`, "name: caveman", "description:"} {
		if strings.Contains(out, gone) {
			t.Errorf("filtered rules still contain %q", gone)
		}
	}
}

func TestRenderCavemanRulesKeepsOtherLevelsRows(t *testing.T) {
	out := renderCavemanRules(cavemanFixtureSkill, "ultra")
	if !strings.Contains(out, "| **ultra** | One word when one word enough |") {
		t.Error("ultra row missing")
	}
	if strings.Contains(out, "| **full** |") || strings.Contains(out, `- full:`) {
		t.Error("foreign level rows leaked into ultra ruleset")
	}
}

func TestRenderCavemanRulesStripsFrontmatter(t *testing.T) {
	out := renderCavemanRules(cavemanFixtureSkill, "full")
	// The table separator row legitimately contains dashes, so only the
	// frontmatter keys and values are checked here.
	if strings.Contains(out, "name: caveman") || strings.Contains(out, "Ultra-compressed communication mode") {
		t.Error("frontmatter leaked into ruleset")
	}
}

// stubCavemanSkill installs a fixture SKILL.md where the candidate lookup
// finds it (CLAUDE_CONFIG_DIR override) and restores the env afterwards.
func stubCavemanSkill(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "plugins", "marketplaces", "caveman", "skills", "caveman")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
}

func TestCavemanActivationTextUsesPluginSkill(t *testing.T) {
	stubCavemanSkill(t, cavemanFixtureSkill)
	txt := cavemanActivationText("full")
	if !strings.HasPrefix(txt, "CAVEMAN MODE ACTIVE — level: full\n\n") {
		t.Errorf("missing activation header: %q", txt[:60])
	}
	if !strings.Contains(txt, "| **full** | Drop articles |") {
		t.Error("plugin skill content not used")
	}
	if strings.Contains(txt, "**lite**") {
		t.Error("unfiltered level rows present")
	}
}

func TestCavemanActivationTextFallsBackWithoutPlugin(t *testing.T) {
	restore := stubNoCavemanSkill(t)
	defer restore()
	txt := cavemanActivationText("full")
	if !strings.Contains(txt, "CAVEMAN MODE ACTIVE — level: full") {
		t.Error("missing activation header")
	}
	if !strings.Contains(txt, "Respond terse like smart caveman") || !strings.Contains(txt, "## Auto-Clarity") {
		t.Error("fallback ruleset incomplete")
	}
}

// stubNoCavemanSkill points the SKILL.md lookup at an empty temp dir and
// returns a restore func.
func stubNoCavemanSkill(t *testing.T) func() {
	t.Helper()
	saved := cavemanCandidates
	cavemanCandidates = func() []string { return []string{filepath.Join(t.TempDir(), "missing", "SKILL.md")} }
	return func() { cavemanCandidates = saved }
}

// resetCavemanCache clears the per-level activation cache between tests so
// each test sees its own fixture, not a cached ruleset from an earlier one.
func resetCavemanCache(t *testing.T) {
	t.Helper()
	cavemanMu.Lock()
	defer cavemanMu.Unlock()
	cavemanCache = map[string]string{}
}

func TestCavemanPromptToggle(t *testing.T) {
	stubCavemanSkill(t, cavemanFixtureSkill)
	resetCavemanCache(t)
	on := config.DefaultOrchestrator()
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}, Orchestrator: on}, t.TempDir(), nil)

	if got := s.cavemanPrompt(); got == "" {
		t.Error("default config must enable caveman activation")
	}

	off := false
	s.cfg.Orchestrator.CavemanDefault = &off
	if got := s.cavemanPrompt(); got != "" {
		t.Errorf("explicit caveman_default=false must disable injection, got %d bytes", len(got))
	}
}

func TestCavemanPromptUnknownLevelFallsBackToFull(t *testing.T) {
	stubCavemanSkill(t, cavemanFixtureSkill)
	resetCavemanCache(t)
	orch := config.DefaultOrchestrator()
	orch.CavemanLevel = "shouty"
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}, Orchestrator: orch}, t.TempDir(), nil)
	if got := s.cavemanPrompt(); !strings.Contains(got, "level: full") {
		t.Errorf("unknown level must fall back to full, got %q", got[:40])
	}
}

func TestCavemanPromptLevelConfigurable(t *testing.T) {
	stubCavemanSkill(t, cavemanFixtureSkill)
	resetCavemanCache(t)
	orch := config.DefaultOrchestrator()
	orch.CavemanLevel = "lite"
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}, Orchestrator: orch}, t.TempDir(), nil)
	got := s.cavemanPrompt()
	if !strings.Contains(got, "level: lite") || !strings.Contains(got, "| **lite** | Keep articles |") {
		t.Error("configured level lite not honored")
	}
	if strings.Contains(got, "**full**") {
		t.Error("full-level rows leaked into lite ruleset")
	}
}

func TestInjectCavemanChannels(t *testing.T) {
	task := "fix the bug"
	opts := agent.RunOpts{Task: task}
	injectCaveman("", true, &opts.Task, &opts)
	if opts.Task != "fix the bug" || opts.AppendSystemPrompt != "" {
		t.Error("empty prompt must inject nothing")
	}

	opts = agent.RunOpts{Task: "fix the bug"}
	injectCaveman("CAVEMAN MODE ACTIVE — level: full", true, &opts.Task, &opts)
	if opts.Task != "fix the bug" {
		t.Error("claude-family task text must stay untouched")
	}
	if opts.AppendSystemPrompt == "" {
		t.Error("claude-family injection must ride the system prompt")
	}

	opts = agent.RunOpts{Task: "fix the bug"}
	injectCaveman("CAVEMAN MODE ACTIVE — level: full", false, &opts.Task, &opts)
	if !strings.HasPrefix(opts.Task, "CAVEMAN MODE ACTIVE — level: full\n\n---\n\nfix the bug") {
		t.Errorf("non-claude family must prepend to task, got %q", opts.Task)
	}
	if opts.AppendSystemPrompt != "" {
		t.Error("non-claude family must not set the system prompt field")
	}
}

func TestOrchestratorPromptCarriesCaveman(t *testing.T) {
	stubCavemanSkill(t, cavemanFixtureSkill)
	resetCavemanCache(t)
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}, Orchestrator: config.DefaultOrchestrator()}, t.TempDir(), nil)
	if !strings.Contains(s.orchestratorPrompt(), "CAVEMAN MODE ACTIVE — level: full") {
		t.Error("brain prompt missing caveman activation")
	}
	off := false
	s.cfg.Orchestrator.CavemanDefault = &off
	if strings.Contains(s.orchestratorPrompt(), "CAVEMAN MODE ACTIVE") {
		t.Error("toggle-off brain prompt must not carry activation")
	}
}

func TestCavemanStatusEndpoint(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	cookie := loginCookie(t, s)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/caveman", nil)
	req.AddCookie(cookie)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Enabled bool     `json:"enabled"`
		Level   string   `json:"level"`
		Source  string   `json:"source"`
		Bytes   int      `json:"ruleset_bytes"`
		Targets []string `json:"targets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Enabled {
		t.Error("caveman must default to enabled")
	}
	if out.Level != "full" {
		t.Errorf("level = %q, want full", out.Level)
	}
	if out.Bytes == 0 {
		t.Error("ruleset_bytes must be positive when enabled")
	}
	if len(out.Targets) != 2 {
		t.Errorf("targets = %v", out.Targets)
	}
}
