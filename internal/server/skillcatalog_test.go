package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func catalogStatus(t *testing.T, s *Server, name, query string) map[string]any {
	t.Helper()
	code, out := doJSON(t, s.handleSkillsCatalog, "GET", "/api/skills/catalog"+query, nil)
	if code != http.StatusOK {
		t.Fatalf("catalog code = %d %v", code, out)
	}
	for _, e := range out["skills"].([]any) {
		if m := e.(map[string]any); m["name"] == name {
			return m
		}
	}
	t.Fatalf("catalog has no %s", name)
	return nil
}

func TestSkillsCatalogListsProvenance(t *testing.T) {
	skillsTestHome(t)
	e := catalogStatus(t, &Server{}, "systematic-debugging", "")
	if e["source"] != "obra/superpowers" || e["license"] != "MIT" || e["description"] == "" || e["global"] != "" {
		t.Fatalf("entry = %v", e)
	}
	if e["files"].(float64) < 2 {
		t.Fatalf("expected bundled files, got %v", e["files"])
	}
}

func TestSkillsCatalogInstallWholeDirectory(t *testing.T) {
	home := skillsTestHome(t)
	s := &Server{}
	code, out := doJSON(t, s.handleSkillsCatalogInstall, "POST", "/api/skills/catalog/install",
		map[string]any{"name": "webapp-testing", "scope": "global"})
	if code != http.StatusOK {
		t.Fatalf("install: %d %v", code, out)
	}
	dir := filepath.Join(home, ".claude", "skills", "webapp-testing")
	// Bundled files come along (GitHub import only fetched SKILL.md) ...
	info, err := os.Stat(filepath.Join(dir, "scripts", "with_server.py"))
	if err != nil {
		t.Fatalf("bundled script not installed: %v", err)
	}
	// ... with the upstream executable bit, and the license file.
	if info.Mode()&0o111 == 0 {
		t.Fatalf("with_server.py lost its executable bit: %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(dir, "LICENSE.txt")); err != nil {
		t.Fatalf("license not installed: %v", err)
	}
	if e := catalogStatus(t, s, "webapp-testing", ""); e["global"] != "installed" {
		t.Fatalf("status = %v", e["global"])
	}
	// No staging directory left behind, and it would not be listed anyway.
	entries, _ := os.ReadDir(filepath.Dir(dir))
	for _, ent := range entries {
		if ent.Name() != "webapp-testing" {
			t.Fatalf("unexpected entry %s in skills dir", ent.Name())
		}
	}
}

func TestSkillsCatalogInstallDoesNotOverwriteByDefault(t *testing.T) {
	home := skillsTestHome(t)
	s := &Server{}
	req := map[string]any{"name": "systematic-debugging", "scope": "global"}
	if code, out := doJSON(t, s.handleSkillsCatalogInstall, "POST", "/", req); code != http.StatusOK {
		t.Fatalf("install: %d %v", code, out)
	}
	skillFile := filepath.Join(home, ".claude", "skills", "systematic-debugging", "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("---\nname: systematic-debugging\n---\nmy local edits\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if e := catalogStatus(t, s, "systematic-debugging", ""); e["global"] != "modified" {
		t.Fatalf("status after local edit = %v", e["global"])
	}
	if code, _ := doJSON(t, s.handleSkillsCatalogInstall, "POST", "/", req); code != http.StatusConflict {
		t.Fatalf("reinstall without overwrite: code %d, want 409", code)
	}
	if data, _ := os.ReadFile(skillFile); string(data) != "---\nname: systematic-debugging\n---\nmy local edits\n" {
		t.Fatal("local edits were overwritten without overwrite=true")
	}
	req["overwrite"] = true
	if code, out := doJSON(t, s.handleSkillsCatalogInstall, "POST", "/", req); code != http.StatusOK {
		t.Fatalf("overwrite: %d %v", code, out)
	}
	if e := catalogStatus(t, s, "systematic-debugging", ""); e["global"] != "installed" {
		t.Fatalf("status after overwrite = %v", e["global"])
	}
}

func TestSkillsCatalogInstallProjectScopeAndErrors(t *testing.T) {
	skillsTestHome(t)
	s := &Server{}
	repo := t.TempDir()
	if code, out := doJSON(t, s.handleSkillsCatalogInstall, "POST", "/",
		map[string]any{"name": "test-driven-development", "scope": "project", "repo": repo}); code != http.StatusOK {
		t.Fatalf("project install: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "skills", "test-driven-development", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if e := catalogStatus(t, s, "test-driven-development", "?repo="+repo); e["project"] != "installed" || e["global"] != "" {
		t.Fatalf("statuses = global %v project %v", e["global"], e["project"])
	}
	if code, _ := doJSON(t, s.handleSkillsCatalogInstall, "POST", "/", map[string]any{"name": "nope", "scope": "global"}); code != http.StatusNotFound {
		t.Fatalf("unknown skill: code %d", code)
	}
	if code, _ := doJSON(t, s.handleSkillsCatalogInstall, "POST", "/", map[string]any{"name": "pdf", "scope": "project", "repo": "relative"}); code != http.StatusBadRequest {
		t.Fatalf("relative repo: code %d", code)
	}
}

func TestSkillsCatalogInstallDirMode(t *testing.T) {
	home := skillsTestHome(t)
	if code, out := doJSON(t, (&Server{}).handleSkillsCatalogInstall, "POST", "/",
		map[string]any{"name": "writing-plans", "scope": "global"}); code != http.StatusOK {
		t.Fatalf("install: %d %v", code, out)
	}
	got, err := os.Stat(filepath.Join(home, ".claude", "skills", "writing-plans"))
	if err != nil {
		t.Fatal(err)
	}
	// Same mode a plain 0755 mkdir gets under the current umask.
	ref := filepath.Join(t.TempDir(), "ref")
	if err := os.Mkdir(ref, 0o755); err != nil {
		t.Fatal(err)
	}
	want, _ := os.Stat(ref)
	if got.Mode().Perm() != want.Mode().Perm() {
		t.Fatalf("skill dir mode = %v, want %v", got.Mode().Perm(), want.Mode().Perm())
	}
}
