package skillcatalog

import (
	"strings"
	"testing"
)

// TestValidate keeps catalog.json and the vendored files in sync and
// enforces the redistributable-license allowlist.
func TestValidate(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFiles(t *testing.T) {
	m, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Skills {
		files, err := Files(s.Name)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		hasSkill := false
		for _, f := range files {
			if f.Path == "SKILL.md" {
				hasSkill = true
			}
			if strings.HasPrefix(f.Path, "/") || strings.Contains(f.Path, "..") {
				t.Fatalf("%s: unsafe path %q", s.Name, f.Path)
			}
		}
		if !hasSkill {
			t.Fatalf("%s: no SKILL.md in Files()", s.Name)
		}
	}
	if _, err := Files("does-not-exist"); err == nil {
		t.Fatal("unknown skill should error")
	}
}

func TestSourceURL(t *testing.T) {
	src := Source{Repo: "obra/superpowers", Commit: "abc", Base: "skills"}
	if got := src.URL("tdd"); got != "https://github.com/obra/superpowers/tree/abc/skills/tdd" {
		t.Fatalf("URL = %s", got)
	}
}
