// Coding Agent menu: generalized optional installer for the headless coding
// agent CLIs — fusion-code (source build via bun), claude code, codex and
// atlascode (npm global installs). Nothing downloads until the user clicks
// install; claude/codex/atlascode detection is just LookPath.
package server

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// codeAgentID enumerates the installable coding agents.
type codeAgentID string

const (
	codeAgentFusion    codeAgentID = "fusion-code"
	codeAgentClaude    codeAgentID = "claude"
	codeAgentCodex     codeAgentID = "codex"
	codeAgentAtlascode codeAgentID = "atlascode"
)

// knownGoodNpmVersions pins console-installed CLIs to versions verified with
// the current release's parsers (atlascode ≥0.1.120 emits usage on the
// result event). The table moves with console releases; a user-managed CLI
// is never downgraded — pinning applies only when the user clicks install.
var knownGoodNpmVersions = map[codeAgentID]string{
	codeAgentClaude:    "2.1.325",
	codeAgentCodex:     "0.167.0",
	codeAgentAtlascode: "0.1.120",
}

type codeAgentStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Detail    string `json:"detail,omitempty"` // version line when detected
	// Service state (fusion-code only — the others are plain CLIs).
	Running bool `json:"running"`
}

// codeAgents reports install state for the four agents. claude/codex/
// atlascode count as installed when their CLI is on PATH (however it got
// there); fusion-code when the console installed it.
func (s *Server) codeAgents() []codeAgentStatus {
	agents := []codeAgentStatus{
		{ID: string(codeAgentFusion), Name: "fusion-code", Installed: s.cfg.Code.Installed, Running: s.cfg.Code.Installed && s.codeHealthy(time.Second)},
		{ID: string(codeAgentClaude), Name: "Claude Code"},
		{ID: string(codeAgentCodex), Name: "Codex CLI"},
		{ID: string(codeAgentAtlascode), Name: "AtlasCode"},
	}
	for i := range agents {
		if agents[i].ID == string(codeAgentFusion) {
			continue
		}
		out, err := exec.LookPath(agents[i].ID)
		if err != nil {
			continue
		}
		agents[i].Installed = true
		if v, verr := exec.Command(out, "--version").CombinedOutput(); verr == nil && len(v) > 0 {
			line := string(v)
			for j := 0; j < len(line); j++ {
				if line[j] == '\n' || line[j] == '\r' {
					line = line[:j]
					break
				}
			}
			if len(line) > 80 {
				line = line[:80]
			}
			agents[i].Detail = line
		}
	}
	return agents
}

// installCodeAgent starts the background install for one agent. claude/codex/
// atlascode go through npm at pinned known-good versions; fusion-code runs
// the bun source build.
func (s *Server) installCodeAgent(ctx context.Context, agent codeAgentID, st *ragInstallState) error {
	switch agent {
	case codeAgentFusion:
		go s.installCode(ctx, st)
		return nil
	case codeAgentClaude:
		return s.startNpmInstall(ctx, st, "claude", "@anthropic-ai/claude-code@"+knownGoodNpmVersions[codeAgentClaude])
	case codeAgentCodex:
		return s.startNpmInstall(ctx, st, "codex", "@openai/codex@"+knownGoodNpmVersions[codeAgentCodex])
	case codeAgentAtlascode:
		return s.startNpmInstall(ctx, st, "atlascode", "@atlasharness/atlascode@"+knownGoodNpmVersions[codeAgentAtlascode])
	default:
		return fmt.Errorf("unknown agent %q", agent)
	}
}

// startNpmInstall validates npm presence, marks the job downloading, and
// runs one npm global install in the background with streamed logs.
func (s *Server) startNpmInstall(ctx context.Context, st *ragInstallState, name, pkg string) error {
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("npm is required to install %s but was not found on PATH", name)
	}
	go func() {
		st.setPhase("downloading")
		st.logf("npm install -g %s", pkg)
		st.setPhase("installing")
		if err := runStream(ctx, st, s.cfg.Code.InstallDir, "npm", "install", "-g", pkg); err != nil {
			s.ragInstallFail(st, fmt.Errorf("npm install %s: %v", pkg, err))
			return
		}
		if _, err := exec.LookPath(name); err != nil {
			s.ragInstallFail(st, fmt.Errorf("%s still not on PATH after install — check npm global bin", name))
			return
		}
		st.logf("%s installed and on PATH", name)
		st.setPhase("done")
	}()
	return nil
}
