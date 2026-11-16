package server

import (
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/config"
)

func TestVersionChangeNotice(t *testing.T) {
	// Same script path, changing output: a silent in-place CLI upgrade must
	// produce exactly one change notice, then go quiet again.
	versions := []string{"2.1.325 (Claude Code)", "2.2.0 (Claude Code)", "2.2.0 (Claude Code)"}
	call := 0
	old := versionProbe
	versionProbe = func(string) (string, error) {
		v := versions[call]
		call++
		return v, nil
	}
	defer func() { versionProbe = old }()

	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	if note := s.versionChangeNotice("claude", "claude"); note != "" {
		t.Errorf("first sighting must be quiet, got %q", note)
	}
	note := s.versionChangeNotice("claude", "claude")
	if !strings.Contains(note, "2.1.325") || !strings.Contains(note, "2.2.0") {
		t.Errorf("change notice wrong: %q", note)
	}
	if note := s.versionChangeNotice("claude", "claude"); note != "" {
		t.Errorf("unchanged version must be quiet, got %q", note)
	}
	if call != 3 {
		t.Errorf("probe called %d times, want 3", call)
	}
}
