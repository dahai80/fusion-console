package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
)

// Skills are SKILL.md directories (Claude Code agent-skill format) that
// headless workers load from ~/.claude/skills (global) or
// <repo>/.claude/skills (project). The console only manages the files —
// the agents load them natively.

func globalSkillsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "skills")
}

func projectSkillsDir(repo string) string {
	return filepath.Join(repo, ".claude", "skills")
}

var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func sanitizeSkillName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	if !skillNameRe.MatchString(name) {
		return "", fmt.Errorf("skill name must be lowercase letters, digits and dashes (got %q)", name)
	}
	return name, nil
}

// skillsDirFor resolves the target directory for a scope, validating the
// project repo path. Returns an error for unknown scopes.
func skillsDirFor(scope, repo string) (string, error) {
	switch scope {
	case "", "global":
		return globalSkillsDir(), nil
	case "project":
		repo = strings.TrimSpace(repo)
		if repo == "" {
			return "", fmt.Errorf("project scope needs a repo path")
		}
		if !filepath.IsAbs(repo) {
			return "", fmt.Errorf("repo path must be absolute")
		}
		return projectSkillsDir(repo), nil
	default:
		return "", fmt.Errorf("unknown scope %q", scope)
	}
}

type skillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Path        string `json:"path"`
}

// readSkillFrontmatter extracts the description from a SKILL.md's
// frontmatter block; everything else is left to the editor.
func readSkillFrontmatter(path string) (description string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return parseSkillFile(string(data)).Description
}

func scanSkillsDir(dir, scope string) []skillEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []skillEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillFile := filepath.Join(dir, e.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			continue
		}
		out = append(out, skillEntry{
			Name:        e.Name(),
			Description: readSkillFrontmatter(skillFile),
			Scope:       scope,
			Path:        skillFile,
		})
	}
	return out
}

func (s *Server) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	skills := scanSkillsDir(globalSkillsDir(), "global")
	if repo := strings.TrimSpace(r.URL.Query().Get("repo")); repo != "" {
		skills = append(skills, scanSkillsDir(projectSkillsDir(repo), "project")...)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	if skills == nil {
		skills = []skillEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": skills})
}

func (s *Server) handleSkillsSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
		Scope       string `json:"scope"`
		Repo        string `json:"repo"`
		// Extra is every other frontmatter line (allowed-tools, license,
		// metadata, ...), written back verbatim. When the field is absent
		// (older clients) the existing file's extra frontmatter is kept, so
		// a save never silently drops fields.
		Extra *string `json:"extra"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	name, err := sanitizeSkillName(req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "skill body is empty"})
		return
	}
	dir, err := skillsDirFor(req.Scope, req.Repo)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	skillDir := filepath.Join(dir, name)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	extra := ""
	if req.Extra != nil {
		extra = *req.Extra
	} else if existing, err := os.ReadFile(skillFile); err == nil {
		extra = parseSkillFile(string(existing)).Extra
	}
	if err := validateSkillExtra(extra); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	content := renderSkillFile(skillDoc{Name: name, Description: req.Description, Extra: extra, Body: req.Body})
	if err := os.WriteFile(skillFile, []byte(content), 0o644); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	log.Printf("server: skill %s saved (%s scope, %s)", name, req.Scope, skillDir)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": skillFile})
}

func (s *Server) handleSkillsDelete(w http.ResponseWriter, r *http.Request) {
	name, err := sanitizeSkillName(r.PathValue("name"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	dir, err := skillsDirFor(r.URL.Query().Get("scope"), r.URL.Query().Get("repo"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	skillDir := filepath.Join(dir, name)
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "skill not found: " + name})
		return
	}
	if err := os.RemoveAll(skillDir); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	log.Printf("server: skill %s deleted (%s)", name, skillDir)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSkillsRead returns a SKILL.md's content for editing. The path must
// be the SKILL.md of a skill under the global or a project skills dir —
// anything else is rejected.
func (s *Server) handleSkillsRead(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	candidates := []string{globalSkillsDir()}
	if repo := strings.TrimSpace(r.URL.Query().Get("repo")); repo != "" && filepath.IsAbs(repo) {
		candidates = append(candidates, projectSkillsDir(repo))
	}
	ok := false
	for _, dir := range candidates {
		parent := filepath.Dir(path)
		if parent == dir || strings.HasPrefix(parent, dir+string(filepath.Separator)) {
			ok = true
			break
		}
	}
	if !ok || filepath.Base(path) != "SKILL.md" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is not a skill file in a managed skills directory"})
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": string(data), "doc": parseSkillFile(string(data))})
}

// handleSkillsDraft turns a hint plus optional reference text into a
// SKILL.md draft using the first enabled provider (the brain). The model
// only drafts — the human edits and confirms in the UI before saving.
func (s *Server) handleSkillsDraft(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hint string `json:"hint"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if strings.TrimSpace(req.Hint) == "" && strings.TrimSpace(req.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provide a hint or reference text"})
		return
	}
	// Prefer the gateway provider (its key self-heals) and resolve the key
	// through resolveProviderKey — same policy as handleTestCall. A zombie
	// direct provider with a stale keychain entry must not break drafting.
	var prov *config.Provider
	for i := range s.cfg.Provider {
		p := &s.cfg.Provider[i]
		if p.Enabled && p.Model != "" && p.Type == config.ProviderGateway {
			prov = p
			break
		}
	}
	if prov == nil {
		for i := range s.cfg.Provider {
			p := &s.cfg.Provider[i]
			if p.Enabled && p.Model != "" {
				prov = p
				break
			}
		}
	}
	if prov == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no enabled LLM provider — configure one first"})
		return
	}
	provider, err := buildProvider(prov)
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	apiKey, err := s.resolveProviderKey(r.Context(), prov)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "api key unavailable: " + err.Error()})
		return
	}
	prompt := "Write a Claude Code agent skill as a single SKILL.md file. Rules: the file must start with a YAML frontmatter block containing exactly 'name:' (lowercase-dash slug) and 'description:' (one sentence saying when to use it), followed by concise markdown instructions (steps, checks, guardrails). No commentary before or after the file.\n"
	if strings.TrimSpace(req.Hint) != "" {
		prompt += "Skill purpose: " + strings.TrimSpace(req.Hint) + "\n"
	}
	if strings.TrimSpace(req.Text) != "" {
		prompt += "\nReference material to distill into the skill:\n" + truncateUTF8(req.Text, 12000) + "\n"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	evCh, err := provider.ChatStream(ctx, prov.Model, apiKey, []chat.Message{{Role: "user", Content: prompt}})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	var content string
	for ev := range evCh {
		if ev.Type == chat.EventDelta && ev.Text != "" {
			content += ev.Text
		}
		if ev.Type == chat.EventError {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": ev.Text})
			return
		}
		if ev.Type == chat.EventDone {
			break
		}
	}
	if strings.TrimSpace(content) == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "model returned an empty draft"})
		return
	}
	log.Printf("server: skill draft generated (provider %s, model %s, %d bytes)", prov.ID, prov.Model, len(content))
	writeJSON(w, http.StatusOK, map[string]any{
		"content":  content,
		"doc":      parseSkillFile(content),
		"warnings": auditSkillContent(content),
	})
}

// skillImportSources are tried in order. raw.githubusercontent.com is
// unreachable from some networks (CN); the gh-proxy mirror (also used by the
// packaging script) is a third-party fallback, so content fetched through it
// is flagged in the response for the reviewer. Package-level for tests.
var skillImportSources = []struct {
	base   string
	mirror bool
}{
	{"https://raw.githubusercontent.com", false},
	{"https://gh-proxy.com/https://raw.githubusercontent.com", true},
}

var skillImportSourceRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/[A-Za-z0-9_./-]+)?$`)

// handleSkillsImport fetches a SKILL.md from GitHub
// (source: "owner/repo" or "owner/repo/sub/path") for preview before save.
// Nothing is written: the response carries the parsed skill, the URL it came
// from, whether a third-party mirror served it, audit warnings, and the
// bundled files it references (only SKILL.md itself is fetched).
func (s *Server) handleSkillsImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	src := strings.Trim(strings.TrimSpace(req.Source), "/")
	if !skillImportSourceRe.MatchString(src) || strings.Contains(src, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source must look like owner/repo or owner/repo/path"})
		return
	}
	client := &http.Client{Timeout: 20 * time.Second}
	// raw URL layout: <base>/<owner>/<repo>/<branch>/<sub path>/SKILL.md.
	parts := strings.SplitN(src, "/", 3)
	repoPart := parts[0] + "/" + parts[1]
	subPath := ""
	if len(parts) == 3 {
		subPath = parts[2] + "/"
	}
	var content, fromURL string
	viaMirror := false
fetch:
	for _, source := range skillImportSources {
		for _, branch := range []string{"main", "master"} {
			url := fmt.Sprintf("%s/%s/%s/%sSKILL.md", source.base, repoPart, branch, subPath)
			resp, err := client.Get(url)
			if err != nil {
				continue
			}
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
			resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK {
				content, fromURL, viaMirror = string(data), url, source.mirror
				break fetch
			}
		}
	}
	if content == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no SKILL.md found at " + src + " (tried main and master)"})
		return
	}
	warnings := auditSkillContent(content)
	if viaMirror {
		warnings = append([]string{"fetched through the third-party mirror gh-proxy.com, not directly from GitHub — compare with the original before saving"}, warnings...)
	}
	log.Printf("server: skill imported from %s (%d bytes, %d warnings)", fromURL, len(content), len(warnings))
	writeJSON(w, http.StatusOK, map[string]any{
		"content":       content,
		"doc":           parseSkillFile(content),
		"url":           fromURL,
		"via_mirror":    viaMirror,
		"warnings":      warnings,
		"missing_files": bundledFileRefs(content),
	})
}
