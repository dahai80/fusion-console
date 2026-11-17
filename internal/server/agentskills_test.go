package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/skillcatalog"
)

// agentTestHome isolates HOME and clears the config-dir overrides the
// layouts honour.
func agentTestHome(t *testing.T) string {
	t.Helper()
	home := skillsTestHome(t)
	for _, k := range []string{"FUSION_CODE_CONFIG_DIR", "ATLAS_CONFIG_DIR", "XDG_CONFIG_HOME"} {
		t.Setenv(k, "")
	}
	return home
}

func TestLayoutForBuiltinAgents(t *testing.T) {
	home := agentTestHome(t)
	cases := map[string]struct{ family, global, project string }{
		"claude":      {"claude", ".claude/skills", ".claude/skills"},
		"fusion-code": {"claude", ".fusion-code/skills", ".fusion-code/skills"},
		"atlascode":   {"claude", ".atlas/skills", ".atlas/skills"},
		"codex":       {"codex", ".agents/skills", ".agents/skills"},
		"opencode":    {"opencode", ".config/opencode/skills", ".opencode/skills"},
	}
	for name, want := range cases {
		l := layoutFor(name, "")
		if l.Family != want.family || l.Global != filepath.Join(home, want.global) || l.Project != filepath.FromSlash(want.project) {
			t.Errorf("%s: layout = %+v", name, l)
		}
		if l.Ref == "" {
			t.Errorf("%s: no source reference", name)
		}
	}
	if len(layoutFor("opencode", "").AlsoReads) == 0 {
		t.Error("opencode should report that it also reads the claude/agents dirs")
	}
}

func TestLayoutForHonoursConfigDirOverrides(t *testing.T) {
	agentTestHome(t)
	t.Setenv("FUSION_CODE_CONFIG_DIR", "/opt/fc")
	t.Setenv("ATLAS_CONFIG_DIR", "/opt/atlas")
	t.Setenv("XDG_CONFIG_HOME", "/opt/xdg")
	for name, want := range map[string]string{
		"fusion-code": "/opt/fc/skills",
		"atlascode":   "/opt/atlas/skills",
		"opencode":    "/opt/xdg/opencode/skills",
	} {
		if got := layoutFor(name, "").Global; got != filepath.FromSlash(want) {
			t.Errorf("%s: global = %s, want %s", name, got, want)
		}
	}
}

func TestLayoutForCustomAgentFollowsProtocol(t *testing.T) {
	home := agentTestHome(t)
	l := layoutFor("my-codex-fork", "codex-jsonl")
	if l.Agent != "my-codex-fork" || l.Family != "codex" || l.Global != filepath.Join(home, ".agents", "skills") {
		t.Fatalf("layout = %+v", l)
	}
	if !strings.HasPrefix(l.Ref, "assumed from protocol codex-jsonl") {
		t.Fatalf("custom layout must say it is assumed: %q", l.Ref)
	}
}

func TestSkillLayoutForValidatesAgent(t *testing.T) {
	agentTestHome(t)
	s := &Server{}
	if l, err := s.skillLayoutFor(""); err != nil || l.Agent != "claude" {
		t.Fatalf("empty agent should default to claude: %+v %v", l, err)
	}
	if _, err := s.skillLayoutFor("nope"); err == nil {
		t.Fatal("unknown agent accepted")
	}
	// Configured custom agents are accepted.
	s.cfg = &config.Config{Agent: append(config.DefaultAgents(), config.AgentSettings{Name: "fork", Protocol: "opencode-jsonl"})}
	if l, err := s.skillLayoutFor("fork"); err != nil || l.Family != "opencode" {
		t.Fatalf("custom agent: %+v %v", l, err)
	}
}

func TestSkillsHandlersAreAgentScoped(t *testing.T) {
	home := agentTestHome(t)
	s := &Server{}
	// Install for codex: lands in ~/.agents/skills, not ~/.claude/skills.
	if code, out := doJSON(t, s.handleSkillsCatalogInstall, "POST", "/",
		map[string]any{"name": "gh-fix-ci", "scope": "global", "agent": "codex"}); code != http.StatusOK {
		t.Fatalf("install: %d %v", code, out)
	}
	codexFile := filepath.Join(home, ".agents", "skills", "gh-fix-ci", "SKILL.md")
	if _, err := os.Stat(codexFile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "gh-fix-ci")); err == nil {
		t.Fatal("codex install leaked into the claude skills dir")
	}
	// List per agent.
	_, out := doJSON(t, s.handleSkillsList, "GET", "/api/skills?agent=codex", nil)
	if n := len(out["skills"].([]any)); n != 1 {
		t.Fatalf("codex list = %v", out)
	}
	_, out = doJSON(t, s.handleSkillsList, "GET", "/api/skills", nil)
	if n := len(out["skills"].([]any)); n != 0 {
		t.Fatalf("claude list should be empty: %v", out)
	}
	// Catalog status per agent.
	if e := catalogStatus(t, s, "gh-fix-ci", "?agent=codex"); e["global"] != "installed" {
		t.Fatalf("codex status = %v", e["global"])
	}
	if e := catalogStatus(t, s, "gh-fix-ci", ""); e["global"] != "" {
		t.Fatalf("claude status = %v", e["global"])
	}
	// Read is confined to the requested agent's directories.
	if code, _ := doJSON(t, s.handleSkillsRead, "GET", "/api/skills/read?agent=codex&path="+codexFile, nil); code != http.StatusOK {
		t.Fatalf("codex read: %d", code)
	}
	if code, _ := doJSON(t, s.handleSkillsRead, "GET", "/api/skills/read?path="+codexFile, nil); code != http.StatusBadRequest {
		t.Fatalf("reading a codex skill as claude should be rejected, got %d", code)
	}
	// Save + delete for fusion-code use its own directory.
	if code, out := doJSON(t, s.handleSkillsSave, "POST", "/", map[string]any{
		"name": "mine", "body": "b", "scope": "global", "agent": "fusion-code"}); code != http.StatusOK {
		t.Fatalf("save: %d %v", code, out)
	}
	fcDir := filepath.Join(home, ".fusion-code", "skills", "mine")
	if _, err := os.Stat(fcDir); err != nil {
		t.Fatal(err)
	}
	// Delete reads {name} via PathValue, so it has to go through a mux.
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/skills/{name}", s.handleSkillsDelete)
	if code, out := doJSON(t, mux.ServeHTTP, "DELETE", "/api/skills/mine?scope=global&agent=fusion-code", nil); code != http.StatusOK {
		t.Fatalf("delete: %d %v", code, out)
	}
	if _, err := os.Stat(fcDir); err == nil {
		t.Fatal("fusion-code skill not deleted")
	}
	// Unknown agent is a 400 everywhere.
	if code, _ := doJSON(t, s.handleSkillsList, "GET", "/api/skills?agent=nope", nil); code != http.StatusBadRequest {
		t.Fatalf("unknown agent list: %d", code)
	}
}

func TestSkillsAgentsListsProfiles(t *testing.T) {
	agentTestHome(t)
	_, out := doJSON(t, (&Server{}).handleSkillsAgents, "GET", "/api/skills/agents", nil)
	agents := out["agents"].([]any)
	if len(agents) != len(config.DefaultAgents()) {
		t.Fatalf("agents = %d", len(agents))
	}
	m, _ := skillcatalog.Load()
	byName := map[string]map[string]any{}
	for _, a := range agents {
		e := a.(map[string]any)
		byName[e["agent"].(string)] = e
	}
	for agent, family := range map[string]string{"claude": "claude", "fusion-code": "claude", "atlascode": "claude", "codex": "codex", "opencode": "opencode"} {
		e := byName[agent]
		if e == nil || e["profile"] != family {
			t.Fatalf("%s: entry = %v", agent, e)
		}
		if got := len(e["skills"].([]any)); got != len(m.Profiles[family].Skills) || got == 0 {
			t.Fatalf("%s: %d profile skills, want %d", agent, got, len(m.Profiles[family].Skills))
		}
	}
}

func TestProfilesDifferPerFamily(t *testing.T) {
	m, err := skillcatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, family := range []string{"claude", "codex", "opencode"} {
		p, ok := m.Profiles[family]
		if !ok || len(p.Skills) == 0 {
			t.Fatalf("profile %s missing", family)
		}
		key := append([]string(nil), p.Skills...)
		sort.Strings(key)
		k := strings.Join(key, ",")
		if other, dup := seen[k]; dup {
			t.Fatalf("profiles %s and %s are identical", family, other)
		}
		seen[k] = family
	}
}

func TestSkillsAgentDefaultsInstallMissingOnly(t *testing.T) {
	home := agentTestHome(t)
	s := &Server{}
	m, _ := skillcatalog.Load()
	want := m.Profiles["codex"].Skills
	dir := filepath.Join(home, ".agents", "skills")

	// A locally modified profile skill and a foreign directory must survive.
	if err := os.MkdirAll(filepath.Join(dir, "gh-fix-ci"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "---\nname: gh-fix-ci\ndescription: my version\n---\nmine\n"
	if err := os.WriteFile(filepath.Join(dir, "gh-fix-ci", "SKILL.md"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "gh-address-comments", "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, out := doJSON(t, s.handleSkillsAgentDefaults, "POST", "/", map[string]any{"agent": "codex"})
	if code != http.StatusOK {
		t.Fatalf("defaults: %d %v", code, out)
	}
	installed := out["installed"].([]any)
	skipped := out["skipped"].([]any)
	if len(installed) != len(want)-2 || len(skipped) != 2 {
		t.Fatalf("installed %v skipped %v", installed, skipped)
	}
	statuses := map[string]string{}
	for _, sk := range skipped {
		e := sk.(map[string]any)
		statuses[e["name"].(string)] = e["status"].(string)
	}
	if statuses["gh-fix-ci"] != "modified" || statuses["gh-address-comments"] != "exists" {
		t.Fatalf("skip statuses = %v", statuses)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "gh-fix-ci", "SKILL.md")); string(data) != mine {
		t.Fatal("defaults overwrote a locally modified skill")
	}
	// Claude-family skills did not land in codex's directory.
	if _, err := os.Stat(filepath.Join(dir, "systematic-debugging")); err == nil {
		t.Fatal("claude-profile skill installed for codex")
	}
	// Idempotent: a second run installs nothing.
	_, out = doJSON(t, s.handleSkillsAgentDefaults, "POST", "/", map[string]any{"agent": "codex"})
	if n := len(out["installed"].([]any)); n != 0 {
		t.Fatalf("second run installed %d", n)
	}

	for _, bad := range []map[string]any{{}, {"agent": "nope"}, {"agent": "codex", "scope": "project", "repo": "rel"}} {
		if code, _ := doJSON(t, s.handleSkillsAgentDefaults, "POST", "/", bad); code != http.StatusBadRequest {
			t.Errorf("%v: code %d, want 400", bad, code)
		}
	}
}
