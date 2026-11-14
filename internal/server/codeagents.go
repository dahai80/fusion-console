// Coding Agent menu: generalized optional installer for the three headless
// coding agent CLIs — fusion-code (source build via bun), claude code and
// codex (npm global installs). Nothing downloads until the user clicks
// install; claude/codex detection is just LookPath.
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
	codeAgentFusion codeAgentID = "fusion-code"
	codeAgentClaude codeAgentID = "claude"
	codeAgentCodex  codeAgentID = "codex"
)

type codeAgentStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Detail    string `json:"detail,omitempty"` // version line when detected
	// Service state (fusion-code only — the others are plain CLIs).
	Running bool `json:"running"`
}

// codeAgents reports install state for all three agents. claude/codex count
// as installed when their CLI is on PATH (however it got there); fusion-code
// when the console installed it.
func (s *Server) codeAgents() []codeAgentStatus {
	agents := []codeAgentStatus{
		{ID: string(codeAgentFusion), Name: "fusion-code", Installed: s.cfg.Code.Installed, Running: s.cfg.Code.Installed && s.codeHealthy(time.Second)},
		{ID: string(codeAgentClaude), Name: "Claude Code"},
		{ID: string(codeAgentCodex), Name: "Codex CLI"},
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

// installCodeAgent starts the background install for one agent. claude/codex
// go through npm; fusion-code runs the bun source build.
func (s *Server) installCodeAgent(ctx context.Context, agent codeAgentID, st *ragInstallState) error {
	switch agent {
	case codeAgentFusion:
		go s.installCode(ctx, st)
		return nil
	case codeAgentClaude:
		return s.startNpmInstall(ctx, st, "claude", "@anthropic-ai/claude-code")
	case codeAgentCodex:
		return s.startNpmInstall(ctx, st, "codex", "@openai/codex")
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
