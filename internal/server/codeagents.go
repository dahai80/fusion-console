// Coding Agent menu: generalized optional installer for the headless coding
// agent CLIs — fusion-code (source build via bun), claude code, codex and
// atlascode (npm global installs). Nothing downloads until the user clicks
// install; claude/codex/atlascode detection is just LookPath.
package server

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
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
			// The CLI may have arrived outside the console (brew, install.sh)
			// — a fusion-code binary on PATH is still a usable dispatch agent.
			if !agents[i].Installed {
				if p, err := exec.LookPath("fusion-code"); err == nil {
					agents[i].Installed = true
					if v, verr := exec.Command(p, "--version").CombinedOutput(); verr == nil && len(v) > 0 {
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
			}
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
// atlascode go through npm at pinned known-good versions; fusion-code prefers
// its prebuilt distribution channels (Homebrew, then the install.sh script)
// and keeps the bun source build as the last fallback.
func (s *Server) installCodeAgent(ctx context.Context, agent codeAgentID, st *ragInstallState) error {
	switch agent {
	case codeAgentFusion:
		return s.startFusionInstall(ctx, st)
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

// startFusionInstall installs fusion-code through its distribution channels:
// Homebrew first (sha256-pinned release binaries), then the install.sh script
// from GitHub Releases, and the bun source build only when neither tool is
// available. The channel is picked up front so the background job never
// switches mid-run.
func (s *Server) startFusionInstall(ctx context.Context, st *ragInstallState) error {
	if _, err := exec.LookPath("brew"); err == nil {
		go func() {
			st.setPhase("installing")
			st.logf("brew install dahai80/fusion-mlx/fusion-code")
			if err := runStream(ctx, st, s.cfg.Code.InstallDir, "brew", "install", "dahai80/fusion-mlx/fusion-code"); err != nil {
				s.ragInstallFail(st, fmt.Errorf("brew install fusion-code: %v", err))
				return
			}
			s.finishFusionInstall(st)
		}()
		return nil
	}
	if _, err := exec.LookPath("curl"); err == nil {
		dir := s.cfg.Code.InstallDir
		go func() {
			st.setPhase("downloading")
			st.logf("install.sh from GitHub Releases (installs the fusion-code CLI)")
			script := fmt.Sprintf(
				"curl -fsSL https://raw.githubusercontent.com/dahai80/fusion-code/main/install.sh | FUSION_CODE_INSTALL_DIR=%q sh",
				filepath.Join(dir, "bin"),
			)
			if err := runStream(ctx, st, dir, "sh", "-c", script); err != nil {
				s.ragInstallFail(st, fmt.Errorf("install.sh: %v", err))
				return
			}
			s.finishFusionInstall(st)
		}()
		return nil
	}
	s.installCode(ctx, st)
	return nil
}

// finishFusionInstall flips the menu into the configured state — same
// persistence contract as the source build path.
func (s *Server) finishFusionInstall(st *ragInstallState) {
	s.cfg.Code.Installed = true
	s.cfg.Code.Enabled = true
	if err := s.cfg.Save(s.cfgDir); err != nil {
		s.ragInstallFail(st, fmt.Errorf("save config: %v", err))
		return
	}
	st.logf("installed and enabled — ready to start")
	st.setPhase("done")
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
