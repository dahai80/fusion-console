// Package skillcatalog embeds the vendored skills catalog: SKILL.md
// directories copied verbatim from pinned upstream commits (see catalog.json
// and cmd/skillsync), each with its license file. The console installs them
// on request; nothing here is loaded by an agent until a user installs it.
package skillcatalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed catalog.json executables.json all:skills
var embedded embed.FS

// executables lists "<skill>/<path>" entries that are executable upstream.
// embed drops file modes, so cmd/skillsync records them here.
func executables() map[string]bool {
	out := map[string]bool{}
	data, err := embedded.ReadFile("executables.json")
	if err != nil {
		return out
	}
	var list []string
	if json.Unmarshal(data, &list) == nil {
		for _, p := range list {
			out[p] = true
		}
	}
	return out
}

// Source is one upstream repository pinned to a commit.
type Source struct {
	ID              string `json:"id"`
	Repo            string `json:"repo"`
	Commit          string `json:"commit"`
	Base            string `json:"base"`              // directory holding the skills in the repo
	License         string `json:"license"`           // SPDX id
	LicenseFile     string `json:"license_file"`      // per-skill license file name, when upstream ships one
	RepoLicenseFile string `json:"repo_license_file"` // repo-level license copied into each skill as LICENSE
}

// Skill is one catalog entry.
type Skill struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Category string `json:"category"`
}

// Profile is a default skill set for one agent family (claude, codex,
// opencode). Agents pick their family by stream protocol.
type Profile struct {
	Title  string   `json:"title"`
	Agents string   `json:"agents"` // human-readable: which agents use it
	Skills []string `json:"skills"`
}

// Manifest is catalog.json.
type Manifest struct {
	Sources  []Source           `json:"sources"`
	Profiles map[string]Profile `json:"profiles"`
	Skills   []Skill            `json:"skills"`
}

// Load parses the embedded manifest.
func Load() (*Manifest, error) {
	data, err := embedded.ReadFile("catalog.json")
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("skillcatalog: parse catalog.json: %w", err)
	}
	return &m, nil
}

// Source returns the source entry with the given id.
func (m *Manifest) Source(id string) (Source, bool) {
	for _, s := range m.Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}

// Skill returns the catalog entry with the given name.
func (m *Manifest) Skill(name string) (Skill, bool) {
	for _, s := range m.Skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// URL is the upstream location of a skill at its pinned commit.
func (src Source) URL(skill string) string {
	return fmt.Sprintf("https://github.com/%s/tree/%s/%s/%s", src.Repo, src.Commit, src.Base, skill)
}

// File is one vendored file of a skill, path relative to the skill directory.
type File struct {
	Path       string
	Data       []byte
	Executable bool
}

// Files returns every vendored file of a skill, sorted by path.
func Files(name string) ([]File, error) {
	root := path.Join("skills", name)
	if _, err := fs.Stat(embedded, path.Join(root, "SKILL.md")); err != nil {
		return nil, fmt.Errorf("skillcatalog: %s is not vendored", name)
	}
	exec := executables()
	var files []File
	err := fs.WalkDir(embedded, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := embedded.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		files = append(files, File{Path: rel, Data: data, Executable: exec[name+"/"+rel]})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

// SkillMD returns a vendored skill's SKILL.md.
func SkillMD(name string) ([]byte, error) {
	return embedded.ReadFile(path.Join("skills", name, "SKILL.md"))
}

// LicenseFileName is the license file every vendored copy of a skill from
// this source carries.
func (src Source) LicenseFileName() string {
	if src.LicenseFile != "" {
		return src.LicenseFile
	}
	return "LICENSE"
}

// Validate checks the manifest against the embedded files: unique names,
// known sources, a SKILL.md and a license file per skill, and no vendored
// directory missing from the manifest.
func Validate() error {
	m, err := Load()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, s := range m.Skills {
		if seen[s.Name] {
			return fmt.Errorf("duplicate skill %q", s.Name)
		}
		seen[s.Name] = true
		src, ok := m.Source(s.Source)
		if !ok {
			return fmt.Errorf("skill %q: unknown source %q", s.Name, s.Source)
		}
		if src.License != "MIT" && src.License != "Apache-2.0" {
			return fmt.Errorf("source %q: license %q is not on the redistributable allowlist", src.ID, src.License)
		}
		if _, err := SkillMD(s.Name); err != nil {
			return fmt.Errorf("skill %q: SKILL.md not vendored (run make skills-sync)", s.Name)
		}
		if _, err := fs.Stat(embedded, path.Join("skills", s.Name, src.LicenseFileName())); err != nil {
			return fmt.Errorf("skill %q: license file %s missing", s.Name, src.LicenseFileName())
		}
	}
	for family, p := range m.Profiles {
		for _, name := range p.Skills {
			if !seen[name] {
				return fmt.Errorf("profile %q lists %q, which is not in the catalog", family, name)
			}
		}
	}
	for p := range executables() {
		if _, err := fs.Stat(embedded, path.Join("skills", p)); err != nil {
			return fmt.Errorf("executables.json lists %s, which is not vendored", p)
		}
	}
	entries, err := fs.ReadDir(embedded, "skills")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() && !seen[e.Name()] {
			return fmt.Errorf("vendored directory skills/%s is not in catalog.json", e.Name())
		}
	}
	return nil
}
