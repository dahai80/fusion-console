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
	"strconv"
	"strings"
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

// knownGoodNpmVersions is the minimum-known-good reference table (design v2
// §4.3): console-installed CLIs are verified against it after install —
// below-known-good logs a warning (e.g. npmmirror sync lag) but never fails;
// the sentinel guardrails cover behavior drift. atlascode installs @latest
// per user decision 2026-11-17; claude/codex stay pinned (reference
// protocol implementations; flipping them is a one-line change).
var knownGoodNpmVersions = map[codeAgentID]string{
	codeAgentClaude:    "2.1.325",
	codeAgentCodex:     "0.167.0",
	codeAgentAtlascode: "0.1.121",
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
		return s.startNpmInstall(ctx, st, "atlascode", "@atlasharness/atlascode@latest")
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
// runs the registry ladder in the background with streamed logs (design v2
// §4.1: npmmirror → official, 1 retry each, 5-minute budget), then verifies
// the installed version against the minimum-known-good table.
func (s *Server) startNpmInstall(ctx context.Context, st *ragInstallState, name, pkg string) error {
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("npm is required to install %s but was not found on PATH", name)
	}
	// 5-minute budget for the whole ladder (design §4.1).
	lctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	go func() {
		defer cancel()
		st.setPhase("downloading")
		done := false
		for i, reg := range []struct {
			name string
			flag string
		}{
			{"npmmirror", "--registry=https://registry.npmmirror.com"},
			{"npmmirror retry", "--registry=https://registry.npmmirror.com"},
			{"official npm", "--registry=https://registry.npmjs.org"},
			{"official npm retry", "--registry=https://registry.npmjs.org"},
		} {
			if lctx.Err() != nil {
				break
			}
			st.logf("npm install -g %s (%s)", pkg, reg.name)
			st.setPhase("installing")
			if err := runStream(lctx, st, s.cfg.Code.InstallDir, "npm", "install", "-g", pkg, reg.flag); err == nil {
				st.logf("install succeeded via %s", reg.name)
				done = true
				break
			} else if i < 3 {
				st.logf("%s attempt failed: %v — falling back", reg.name, err)
			} else {
				st.logf("%s attempt failed: %v", reg.name, err)
			}
		}
		if !done {
			s.ragInstallFail(st, fmt.Errorf("npm install %s failed on all registries (npmmirror + official) — check network access", pkg))
			return
		}
		out, err := exec.Command(name, "--version").CombinedOutput()
		if err != nil {
			s.ragInstallFail(st, fmt.Errorf("%s still not runnable after install — check npm global bin on PATH: %v", name, err))
			return
		}
		version := firstLine(string(out))
		st.logf("%s installed, version %s", name, version)
		if want, ok := knownGoodNpmVersions[codeAgentID(nameForKnownGood(name))]; ok && versionLess(version, want) {
			st.logf("warning: %s %s is below the verified minimum %s (possible mirror sync lag) — it will work, but consider upgrading", name, version, want)
		}
		st.setPhase("done")
	}()
	return nil
}

// nameForKnownGood maps CLI names to the codeAgentID keys of the
// known-good table.
func nameForKnownGood(cli string) string {
	switch cli {
	case "atlascode", "atlas":
		return "atlascode"
	default:
		return cli
	}
}

// firstLine returns the first non-empty line, trimmed (version output).
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			return l
		}
	}
	return ""
}

// versionLess compares dotted numeric version strings (v-prefix tolerated);
// non-numeric segments compare lexically as a fallback. Best-effort: the
// known-good check only gates a warning.
func versionLess(a, b string) bool {
	av, bv := normalizeVersion(a), normalizeVersion(b)
	parts := strings.Split(av, ".")
	want := strings.Split(bv, ".")
	for i := 0; i < len(parts) && i < len(want); i++ {
		pi, perr := strconv.Atoi(parts[i])
		wi, werr := strconv.Atoi(want[i])
		if perr == nil && werr == nil {
			if pi != wi {
				return pi < wi
			}
			continue
		}
		if parts[i] != want[i] {
			return parts[i] < want[i]
		}
	}
	return len(parts) < len(want)
}

// normalizeVersion strips a leading v/V and any trailing build metadata.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		v = v[:i]
	}
	return v
}
