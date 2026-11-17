package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CLI version fingerprinting: capture `--version` per run (short-TTL cached),
// compare with the last version seen for the same agent, and surface a
// notice on change. A silent CLI upgrade is the most common trigger for
// wire-format drift, so the change itself must be visible.

const (
	versionCacheTTL = 10 * time.Minute
	versionTimeout  = 5 * time.Second
)

// versionProbe runs `--version` for one command; a package var so tests can
// inject fake CLI versions.
var versionProbe = func(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, command, "--version").CombinedOutput()
	return string(out), err
}

// cliVersion returns the CLI's --version line (single line, trimmed), cached
// per command for versionCacheTTL. Empty string on any failure — versioning
// is best-effort and must never block a run.
func (s *Server) cliVersion(command string) string {
	s.verMu.Lock()
	if e, ok := s.verCache[command]; ok && time.Since(e.at) < versionCacheTTL {
		s.verMu.Unlock()
		return e.version
	}
	s.verMu.Unlock()

	out, err := versionProbe(command)
	line := strings.TrimSpace(out)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	if err != nil || line == "" {
		return ""
	}
	if len(line) > 80 {
		line = line[:80]
	}
	s.verMu.Lock()
	s.verCache[command] = verCacheEntry{version: line, at: time.Now()}
	s.verMu.Unlock()
	return line
}

// versionChangeNotice compares the CLI's current version with the last one
// recorded for this agent and returns a human-readable notice when they
// differ (empty when unchanged, first sighting, or version unknown). It
// probes fresh on every call — a cached value would hide in-place upgrades
// within the TTL — and records the current version for the next comparison.
func (s *Server) versionChangeNotice(agentName, command string) string {
	out, err := versionProbe(command)
	line := strings.TrimSpace(out)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	if err != nil || line == "" {
		return ""
	}
	cur := line
	if len(cur) > 80 {
		cur = cur[:80]
	}
	s.verMu.Lock()
	defer s.verMu.Unlock()
	prev := s.verLast[agentName]
	s.verLast[agentName] = cur
	if prev == "" || prev == cur {
		return ""
	}
	return fmt.Sprintf("%s CLI version changed: %s -> %s — consider a smoke probe before dispatching real work", agentName, prev, cur)
}
