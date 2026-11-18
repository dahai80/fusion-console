package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dahai80/fusion-console/internal/agent"
)

// Caveman default activation (issue #30): the console injects the caveman
// skill's own activation ruleset into every brain session and dispatched
// worker, controlled by orchestrator.caveman_default / caveman_level.
//
// The ruleset text is read from the installed caveman plugin's SKILL.md at
// runtime (same source of truth the plugin's SessionStart hook uses) and
// filtered to the configured intensity level with the same rules the hook
// applies. When the plugin is not installed, a verbatim copy of the hook's
// hardcoded fallback ruleset is used — never invented wording.

// cavemanLevels are the intensity levels the caveman plugin defines. An
// unknown configured level falls back to "full" (the plugin default).
var cavemanLevels = map[string]bool{
	"lite": true, "full": true, "ultra": true,
	"wenyan-lite": true, "wenyan-full": true, "wenyan-ultra": true,
}

// cavemanFallbackRules is the caveman plugin's own hardcoded fallback
// ruleset (src/hooks/caveman-activate.js) carried verbatim for hosts
// without the plugin installed.
const cavemanFallbackRules = `Respond terse like smart caveman. All technical substance stay. Only fluff die.

## Persistence

ACTIVE EVERY RESPONSE. No revert after many turns. No filler drift. Still active if unsure. Off only: "stop caveman" / "normal mode".

Current level: **%[1]s**. Switch: ` + "`/caveman lite|full|ultra`" + `.

## Rules

Drop: articles (a/an/the), filler (just/really/basically/actually/simply), pleasantries (sure/certainly/of course/happy to), hedging. Fragments OK. Short synonyms (big not extensive, fix not "implement a solution for"). Technical terms exact. Code blocks unchanged. Errors quoted exact.

Preserve user's dominant language. User write Portuguese → reply Portuguese caveman. Compress the style, not the language. Technical terms, code, API names, commands, error strings stay verbatim.

No self-reference. Never name or announce the style. No "caveman mode on" tags. Output caveman-only.

Pattern: ` + "`[thing] [action] [reason]. [next step].`" + `

Not: "Sure! I'd be happy to help you with that. The issue you're experiencing is likely caused by..."
Yes: "Bug in auth middleware. Token expiry check use ` + "`<`" + ` not ` + "`<=`" + `. Fix:"

## Auto-Clarity

Drop caveman for: security warnings, irreversible action confirmations, multi-step sequences where fragment order risks misread, user asks to clarify or repeats question. Resume caveman after clear part done.

## Boundaries

Code/commits/PRs: write normal. "stop caveman" or "normal mode": revert. Level persist until changed or session end.`

// cavemanSkillCandidates lists the plugin SKILL.md locations tried in order:
// CLAUDE_CONFIG_DIR override first, then the standard ~/.claude install.
func cavemanSkillCandidates() []string {
	var out []string
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		out = append(out, filepath.Join(d, "plugins", "marketplaces", "caveman", "skills", "caveman", "SKILL.md"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".claude", "plugins", "marketplaces", "caveman", "skills", "caveman", "SKILL.md"))
	}
	return out
}

// cavemanCandidates is the SKILL.md location provider; a var so tests can
// redirect the lookup at a fixture directory.
var cavemanCandidates = cavemanSkillCandidates

// readCavemanSkill returns the plugin SKILL.md content and its path, or
// ok=false when no installed plugin copy is readable.
func readCavemanSkill() (content, path string, ok bool) {
	for _, p := range cavemanCandidates() {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		return string(data), p, true
	}
	return "", "", false
}

// renderCavemanRules strips the SKILL.md frontmatter and filters the
// intensity table and example lines down to the active level, mirroring the
// plugin hook's filtering so the emitted ruleset matches what the plugin
// itself would activate with.
func renderCavemanRules(skillContent, level string) string {
	body := skillContent
	if i := strings.Index(body, "\n---\n"); strings.HasPrefix(body, "---\n") && i >= 0 {
		body = body[i+len("\n---\n"):]
	} else {
		body = strings.TrimPrefix(body, "---\n")
	}
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if m := cavemanTableRowRe(line); m != "" {
			if m == level {
				lines = append(lines, line)
			}
			continue
		}
		if ex := cavemanExampleLabel(line); ex != "" {
			if ex == level {
				lines = append(lines, line)
			}
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// cavemanTableRowRe reports the level label of an intensity-table row
// ("| **full** | ..."), or "" for other lines.
func cavemanTableRowRe(line string) string {
	if !strings.HasPrefix(line, "|") {
		return ""
	}
	s := strings.TrimLeft(strings.TrimPrefix(line, "|"), " ")
	if !strings.HasPrefix(s, "**") {
		return ""
	}
	end := strings.Index(s[2:], "**")
	if end < 0 {
		return ""
	}
	return s[2 : 2+end]
}

// cavemanExampleLabel reports the level label of an example line
// ("- full: ..."), or "" for other lines.
func cavemanExampleLabel(line string) string {
	if !strings.HasPrefix(line, "- ") {
		return ""
	}
	rest := line[2:]
	i := strings.Index(rest, ":")
	if i <= 0 {
		return ""
	}
	label := rest[:i]
	if strings.ContainsAny(label, " *|") {
		return ""
	}
	return label
}

// cavemanActivationText assembles the full activation block for one level:
// the header line the plugin hook emits plus the filtered ruleset. Falls
// back to the hook's hardcoded ruleset when the plugin is not installed.
func cavemanActivationText(level string) string {
	var rules string
	if content, path, ok := readCavemanSkill(); ok {
		rules = renderCavemanRules(content, level)
		log.Printf("caveman: activation rules loaded from %s (level=%s)", path, level)
	} else {
		rules = fmt.Sprintf(cavemanFallbackRules, level)
		log.Printf("caveman: plugin SKILL.md not found — using built-in fallback ruleset (level=%s)", level)
	}
	return "CAVEMAN MODE ACTIVE — level: " + level + "\n\n" + rules
}

// cavemanOnce caches the resolved activation text per level: the SKILL.md
// lookup runs once per process per level, not once per dispatch.
var (
	cavemanMu       sync.Mutex
	cavemanCache    = map[string]string{}
	cavemanFallback = map[string]string{}
)

// cavemanPrompt returns the activation block to inject for this server's
// configuration, or "" when the toggle is off (orchestrator.caveman_default
// defaults to on; caveman_level defaults to "full").
func (s *Server) cavemanPrompt() string {
	orch := s.cfg.Orchestrator
	if !orch.CavemanEnabled() {
		return ""
	}
	level := strings.TrimSpace(orch.CavemanLevel)
	if level == "" {
		level = "full"
	}
	if !cavemanLevels[level] {
		log.Printf("caveman: unknown level %q configured — falling back to full", level)
		level = "full"
	}
	cavemanMu.Lock()
	defer cavemanMu.Unlock()
	if txt, ok := cavemanCache[level]; ok {
		return txt
	}
	txt := cavemanActivationText(level)
	cavemanCache[level] = txt
	return txt
}

// injectCaveman routes the activation block into one worker run: claude-family
// CLIs take it as an appended system prompt; other CLI families (codex,
// opencode) have no system-prompt flag, so the block rides the task text.
// Both channels are per-invocation, so repeated dispatches never duplicate
// the block in workspace files.
func injectCaveman(prompt string, claudeFamily bool, task *string, opts *agent.RunOpts) {
	if prompt == "" {
		return
	}
	if claudeFamily {
		opts.AppendSystemPrompt = prompt
		log.Printf("caveman: injecting activation via --append-system-prompt (%d bytes)", len(prompt))
		return
	}
	*task = prompt + "\n\n---\n\n" + *task
	log.Printf("caveman: injecting activation via task prefix, no system-prompt flag on this CLI family (%d bytes)", len(prompt))
}

// cavemanStatus is the visibility payload for the Skills tab "Built-in
// injections" block: what gets injected, from where, at which level.
type cavemanStatus struct {
	Enabled bool     `json:"enabled"`
	Level   string   `json:"level"`
	Source  string   `json:"source"`        // plugin SKILL.md path or "built-in fallback"
	Bytes   int      `json:"ruleset_bytes"` // rendered activation block size
	Targets []string `json:"targets"`       // where the block is injected
}

// handleCavemanStatus backs GET /api/caveman.
func (s *Server) handleCavemanStatus(w http.ResponseWriter, _ *http.Request) {
	orch := s.cfg.Orchestrator
	level := strings.TrimSpace(orch.CavemanLevel)
	if level == "" {
		level = "full"
	}
	if !cavemanLevels[level] {
		level = "full"
	}
	st := cavemanStatus{Enabled: orch.CavemanEnabled(), Level: level, Targets: []string{"chat brain sessions", "dispatched workers"}}
	if !st.Enabled {
		writeJSON(w, http.StatusOK, st)
		return
	}
	txt := s.cavemanPrompt()
	st.Bytes = len(txt)
	if _, path, ok := readCavemanSkill(); ok {
		st.Source = path
	} else {
		st.Source = "built-in fallback"
	}
	writeJSON(w, http.StatusOK, st)
}
