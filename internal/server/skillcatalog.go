package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/skillcatalog"
)

// Catalog endpoints: the vendored skills (internal/skillcatalog) listed with
// provenance and install state, and installed on explicit request. Install
// copies the whole skill directory (SKILL.md plus scripts/references), which
// GitHub import cannot do.

type catalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Source      string `json:"source"`  // upstream repo, e.g. obra/superpowers
	Commit      string `json:"commit"`  // pinned upstream commit
	License     string `json:"license"` // SPDX id
	URL         string `json:"url"`     // upstream directory at the pinned commit
	Files       int    `json:"files"`
	Bytes       int    `json:"bytes"`
	// Status per scope: "" (not installed), "installed" (SKILL.md identical
	// to the catalog copy) or "modified" (present but different — edited
	// locally or installed from another version).
	Global  string `json:"global"`
	Project string `json:"project,omitempty"`
}

// skillInstallStatus compares an installed SKILL.md with the catalog copy.
func skillInstallStatus(dir, name string, want []byte) string {
	have, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
	if err != nil {
		return ""
	}
	if bytes.Equal(have, want) {
		return "installed"
	}
	return "modified"
}

func (s *Server) handleSkillsCatalog(w http.ResponseWriter, r *http.Request) {
	m, err := skillcatalog.Load()
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	projectDir := ""
	if repo != "" && filepath.IsAbs(repo) {
		projectDir = projectSkillsDir(repo)
	}
	out := make([]catalogEntry, 0, len(m.Skills))
	for _, sk := range m.Skills {
		src, _ := m.Source(sk.Source)
		md, err := skillcatalog.SkillMD(sk.Name)
		if err != nil {
			continue
		}
		files, _ := skillcatalog.Files(sk.Name)
		size := 0
		for _, f := range files {
			size += len(f.Data)
		}
		e := catalogEntry{
			Name:        sk.Name,
			Description: parseSkillFile(string(md)).Description,
			Category:    sk.Category,
			Source:      src.Repo,
			Commit:      src.Commit,
			License:     src.License,
			URL:         src.URL(sk.Name),
			Files:       len(files),
			Bytes:       size,
			Global:      skillInstallStatus(globalSkillsDir(), sk.Name, md),
		}
		if projectDir != "" {
			e.Project = skillInstallStatus(projectDir, sk.Name, md)
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

func (s *Server) handleSkillsCatalogInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Scope     string `json:"scope"`
		Repo      string `json:"repo"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	m, err := skillcatalog.Load()
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	if _, ok := m.Skill(req.Name); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no catalog skill named " + req.Name})
		return
	}
	dir, err := skillsDirFor(req.Scope, req.Repo)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	path, err := installCatalogSkill(dir, req.Name, req.Overwrite)
	if errors.Is(err, errSkillExists) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
}

var errSkillExists = errors.New("skill already installed")

// installCatalogSkill copies a vendored skill directory into dir/<name>.
// An existing directory is only replaced when overwrite is set; the new copy
// is staged next to the target and renamed into place, so a failed install
// never leaves a half-written skill behind.
func installCatalogSkill(dir, name string, overwrite bool) (string, error) {
	files, err := skillcatalog.Files(name)
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); err == nil && !overwrite {
		return "", fmt.Errorf("%w: %s exists (reinstall to overwrite)", errSkillExists, target)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Not MkdirTemp: that forces 0700, while every other skill directory is
	// 0755 minus the user's umask.
	staging := filepath.Join(dir, fmt.Sprintf(".%s.install-%d", name, time.Now().UnixNano()))
	if err := os.Mkdir(staging, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	for _, f := range files {
		dst := filepath.Join(staging, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		mode := os.FileMode(0o644)
		if f.Executable {
			mode = 0o755
		}
		if err := os.WriteFile(dst, f.Data, mode); err != nil {
			return "", err
		}
	}
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil {
		return "", err
	}
	log.Printf("server: catalog skill %s installed to %s (%d files)", name, target, len(files))
	return filepath.Join(target, "SKILL.md"), nil
}
