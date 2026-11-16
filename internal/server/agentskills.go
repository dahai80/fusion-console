package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/skillcatalog"
)

// Each agent CLI discovers skills in its own directories, so a skill
// installed for claude (~/.claude/skills) is invisible to codex or
// fusion-code. skillLayout is the per-agent answer to "where do skills go",
// with the source each path was taken from.

type skillLayout struct {
	Agent  string `json:"agent"`
	Family string `json:"family"` // claude | codex | opencode — selects the default profile
	// Global is the user-level skills directory (absolute).
	Global string `json:"global"`
	// Project is the repo-relative skills directory.
	Project string `json:"project"`
	// AlsoReads lists other directories the CLI loads skills from, so the
	// UI can say when skills overlap between agents.
	AlsoReads []string `json:"also_reads,omitempty"`
	// Ref is where this layout is documented (docs URL or upstream source).
	Ref string `json:"ref"`
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// envDir returns $name when set, otherwise filepath.Join(home, rel...).
func envDir(name string, rel ...string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return filepath.Join(append([]string{homeDir()}, rel...)...)
}

// layoutFor resolves the skill directories for an agent by name, falling
// back to its stream-protocol family for agents the console does not know.
func layoutFor(name, protocol string) skillLayout {
	switch name {
	case "claude":
		return skillLayout{Agent: name, Family: "claude",
			Global:  filepath.Join(homeDir(), ".claude", "skills"),
			Project: filepath.Join(".claude", "skills"),
			Ref:     "https://code.claude.com/docs/en/skills"}
	case "fusion-code":
		return skillLayout{Agent: name, Family: "claude",
			Global:  filepath.Join(envDir("FUSION_CODE_CONFIG_DIR", ".fusion-code"), "skills"),
			Project: filepath.Join(".fusion-code", "skills"),
			Ref:     "https://github.com/dahai80/fusion-code/blob/main/src/skills/loadSkillsDir.ts"}
	case "atlascode":
		return skillLayout{Agent: name, Family: "claude",
			Global:  filepath.Join(envDir("ATLAS_CONFIG_DIR", ".atlas"), "skills"),
			Project: filepath.Join(".atlas", "skills"),
			Ref:     "https://github.com/vincentlau2046/AtlasCode/blob/master/src/engine/skill/loadSkillsDir.ts"}
	case "codex":
		return skillLayout{Agent: name, Family: "codex",
			Global:  filepath.Join(homeDir(), ".agents", "skills"),
			Project: filepath.Join(".agents", "skills"),
			Ref:     "https://learn.chatgpt.com/docs/build-skills"}
	case "opencode":
		return skillLayout{Agent: name, Family: "opencode",
			Global:    filepath.Join(envDir("XDG_CONFIG_HOME", ".config"), "opencode", "skills"),
			Project:   filepath.Join(".opencode", "skills"),
			AlsoReads: []string{"~/.claude/skills", "~/.agents/skills", ".claude/skills", ".agents/skills"},
			Ref:       "https://opencode.ai/docs/skills"}
	}
	// Custom agent: assume it reads skills like the CLI whose stream
	// protocol it speaks.
	var l skillLayout
	switch agent.ProtocolFor(name, protocol) {
	case agent.ProtocolCodexJSONL:
		l = layoutFor("codex", "")
	case agent.ProtocolOpencodeJSONL:
		l = layoutFor("opencode", "")
	default:
		l = layoutFor("claude", "")
	}
	l.Agent = name
	l.Ref = "assumed from protocol " + agent.ProtocolFor(name, protocol) + " — " + l.Ref
	return l
}

// agentList is the configured agents, or the built-ins when no config is
// loaded (tests).
func (s *Server) agentList() []config.AgentSettings {
	if s.cfg != nil && len(s.cfg.Agent) > 0 {
		return s.cfg.Agent
	}
	return config.DefaultAgents()
}

// skillLayoutFor validates an agent name from a request ("" = claude, the
// console's historical default) and returns its layout.
func (s *Server) skillLayoutFor(name string) (skillLayout, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "claude"
	}
	for _, a := range s.agentList() {
		if a.Name == name {
			return layoutFor(a.Name, a.Protocol), nil
		}
	}
	return skillLayout{}, fmt.Errorf("unknown agent %q", name)
}

// skillsDir resolves the target directory for an agent and scope, validating
// the project repo path.
func (l skillLayout) skillsDir(scope, repo string) (string, error) {
	switch scope {
	case "", "global":
		return l.Global, nil
	case "project":
		repo = strings.TrimSpace(repo)
		if repo == "" {
			return "", fmt.Errorf("project scope needs a repo path")
		}
		if !filepath.IsAbs(repo) {
			return "", fmt.Errorf("repo path must be absolute")
		}
		return filepath.Join(repo, l.Project), nil
	default:
		return "", fmt.Errorf("unknown scope %q", scope)
	}
}

// --- per-agent default skills ---------------------------------------------
//
// catalog.json defines one default profile per agent family. Nothing is
// installed automatically: the Agent defaults tab shows each agent's
// directories and profile status, and installs the missing defaults on
// request. Existing skills (including locally modified ones) are never
// overwritten by this path.

type profileSkillStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "" | "installed" | "modified"
}

type agentSkillsEntry struct {
	skillLayout
	Enabled bool                 `json:"enabled"`
	Profile string               `json:"profile"`       // family key in catalog.json
	Title   string               `json:"profile_title"` // e.g. "TDD & review"
	Skills  []profileSkillStatus `json:"skills"`
}

func (s *Server) handleSkillsAgents(w http.ResponseWriter, r *http.Request) {
	m, err := skillcatalog.Load()
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	out := []agentSkillsEntry{}
	for _, a := range s.agentList() {
		l := layoutFor(a.Name, a.Protocol)
		p := m.Profiles[l.Family]
		e := agentSkillsEntry{skillLayout: l, Enabled: a.Enabled, Profile: l.Family, Title: p.Title, Skills: []profileSkillStatus{}}
		for _, name := range p.Skills {
			md, err := skillcatalog.SkillMD(name)
			if err != nil {
				continue
			}
			e.Skills = append(e.Skills, profileSkillStatus{Name: name, Status: skillInstallStatus(l.Global, name, md)})
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (s *Server) handleSkillsAgentDefaults(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent string `json:"agent"`
		Scope string `json:"scope"`
		Repo  string `json:"repo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if strings.TrimSpace(req.Agent) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent is required"})
		return
	}
	layout, err := s.skillLayoutFor(req.Agent)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	dir, err := layout.skillsDir(req.Scope, req.Repo)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	m, err := skillcatalog.Load()
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	installed := []string{}
	skipped := []profileSkillStatus{}
	for _, name := range m.Profiles[layout.Family].Skills {
		md, err := skillcatalog.SkillMD(name)
		if err != nil {
			serverError(w, err, http.StatusInternalServerError)
			return
		}
		if st := skillInstallStatus(dir, name, md); st != "" {
			skipped = append(skipped, profileSkillStatus{Name: name, Status: st})
			continue
		}
		if _, err := installCatalogSkill(dir, name, false); err != nil {
			if errors.Is(err, errSkillExists) {
				// A directory without SKILL.md: not ours to replace.
				skipped = append(skipped, profileSkillStatus{Name: name, Status: "exists"})
				continue
			}
			serverError(w, err, http.StatusInternalServerError)
			return
		}
		installed = append(installed, name)
	}
	log.Printf("server: %s default skills (%s profile) -> %s: installed %v, skipped %d", layout.Agent, layout.Family, dir, installed, len(skipped))
	writeJSON(w, http.StatusOK, map[string]any{"dir": dir, "installed": installed, "skipped": skipped})
}
