// TokenEconomizer default install: the console dispatches claude, codex and
// opencode headlessly, so their plugin surfaces get TokenEconomizer
// (microsoft/TokenEconomizer — experience-driven cost control) registered
// automatically. Source of truth is a local clone: ~/harness/TokenEconomizer
// when present (dev), else a shallow clone into the console data dir.
// Everything here is best-effort and fail-soft: a failure logs and the
// agents carry on exactly as if the plugin were absent (its own contract).
package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	teRepoURL    = "https://github.com/microsoft/TokenEconomizer.git"
	teMarketName = "tokeneconomizer"
	tePluginRef  = "tokeneconomizer@tokeneconomizer"
)

// tokenEconDir resolves the local plugin clone: the dev checkout first
// (explicitly requested default source), then a console-managed clone in the
// data dir. Returns "" when neither exists and cloning is not possible here —
// the caller just skips.
func (s *Server) tokenEconDir() string {
	for _, p := range []string{filepath.Join(homeDir(), "harness", "TokenEconomizer"), filepath.Join(s.cfgDir, "TokenEconomizer")} {
		if fileExists(filepath.Join(p, ".claude-plugin", "marketplace.json")) {
			return p
		}
	}
	return ""
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// ensureTokenEconomizerSource guarantees a usable clone, shallow-cloning the
// upstream repo into the console data dir when the dev checkout is absent.
func (s *Server) ensureTokenEconomizerSource(ctx context.Context) (string, error) {
	if dir := s.tokenEconDir(); dir != "" {
		return dir, nil
	}
	dir := filepath.Join(s.cfgDir, "TokenEconomizer")
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return dir, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is required to clone TokenEconomizer")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := exec.CommandContext(cctx, "git", "clone", "--depth", "1", teRepoURL, dir).CombinedOutput(); err != nil {
		return "", fmt.Errorf("clone TokenEconomizer: %v", err)
	}
	log.Printf("server: tokeneconomizer source cloned to %s", dir)
	return dir, nil
}

// tePluginInstalled asks the CLI whether the plugin is already registered, so
// a console restart does not re-run the installer every boot.
func tePluginInstalled(bin string) bool {
	out, err := exec.Command(bin, "plugin", "list").CombinedOutput()
	return err == nil && strings.Contains(string(out), teMarketName)
}

// installTokenEconomizer registers the plugin with one CLI. claude and codex
// use the plugin marketplace flow; opencode (experimental upstream) uses a
// symlinked plugin file. Idempotent: each step skips when already present.
func (s *Server) installTokenEconomizer(ctx context.Context, agent string) error {
	if _, err := exec.LookPath(agent); err != nil {
		return fmt.Errorf("%s not on PATH", agent)
	}
	dir, err := s.ensureTokenEconomizerSource(ctx)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	switch agent {
	case "claude":
		if _, err := exec.CommandContext(cctx, "claude", "plugin", "marketplace", "add", dir).CombinedOutput(); err != nil && !strings.Contains(err.Error(), "already") {
			// marketplace add may report already-registered via output, not
			// exit code — treat any failure as non-fatal and check install.
			log.Printf("server: te marketplace add (claude) noted: %v", err)
		}
		if tePluginInstalled("claude") {
			return nil
		}
		out, err := exec.CommandContext(cctx, "claude", "plugin", "install", tePluginRef).CombinedOutput()
		if err != nil {
			return fmt.Errorf("claude plugin install: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "codex":
		if _, err := exec.CommandContext(cctx, "codex", "plugin", "marketplace", "add", dir).CombinedOutput(); err != nil {
			log.Printf("server: te marketplace add (codex) noted: %v", err)
		}
		if tePluginInstalled("codex") {
			return nil
		}
		out, err := exec.CommandContext(cctx, "codex", "plugin", "add", tePluginRef).CombinedOutput()
		if err != nil {
			return fmt.Errorf("codex plugin add: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "opencode":
		// Experimental upstream contract: one symlinked plugin file serves
		// opencode 1.x (server entry) and 2.x (setup).
		home := homeDir()
		if home == "" {
			return fmt.Errorf("no home directory for opencode plugins")
		}
		plugDir := filepath.Join(home, ".config", "opencode", "plugins")
		if err := os.MkdirAll(plugDir, 0o755); err != nil {
			return err
		}
		link := filepath.Join(plugDir, "tokeneconomizer.ts")
		target := filepath.Join(dir, "plugin", "opencode", "tokeneconomizer.ts")
		if cur, err := os.Readlink(link); err == nil && cur == target {
			return nil
		}
		_ = os.Remove(link)
		return os.Symlink(target, link)
	default:
		return fmt.Errorf("tokeneconomizer has no install path for agent %q", agent)
	}
}

// ensureTokenEconomizerStartup runs once per boot in the background: install
// the plugin for every supported dispatch CLI already on PATH. Fail-soft —
// the worst case is a log line.
func (s *Server) ensureTokenEconomizerStartup() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	dir := s.tokenEconDir()
	if dir == "" {
		if _, err := exec.LookPath("git"); err != nil {
			log.Printf("server: tokeneconomizer skipped (no local clone, no git)")
			return
		}
	}
	for _, agent := range []string{"claude", "codex", "opencode"} {
		if _, err := exec.LookPath(agent); err != nil {
			continue
		}
		if err := s.installTokenEconomizer(ctx, agent); err != nil {
			log.Printf("server: tokeneconomizer install for %s failed: %v", agent, err)
			continue
		}
		log.Printf("server: tokeneconomizer ready for %s", agent)
	}
}
