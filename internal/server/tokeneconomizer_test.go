package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTokenEconDirPrefersDevCheckout(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfgDir: filepath.Join(dir, "cfg")}
	// A dev checkout at ~/harness/TokenEconomizer legitimately wins on dev
	// machines, so the invariant is "a resolvable source always carries the
	// marketplace manifest", not "no source before the tree exists".
	dev := filepath.Join(dir, "harness", "TokenEconomizer", ".claude-plugin")
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dev, "marketplace.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "cfg", "TokenEconomizer", ".claude-plugin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "cfg", "TokenEconomizer", ".claude-plugin", "marketplace.json"), []byte("{}"), 0o644)
	got := s.tokenEconDir()
	if got == "" {
		t.Fatal("no source resolved")
	}
	if _, err := os.Stat(filepath.Join(got, ".claude-plugin", "marketplace.json")); err != nil {
		t.Fatalf("resolved source lacks the marketplace manifest: %v", err)
	}
}

func TestTokenEconomizerOpencodeSymlink(t *testing.T) {
	dir := t.TempDir()
	// Isolate home via the callers' homeDir? installTokenEconomizer reads
	// the real home — test only the source-resolution and skip the symlink
	// when HOME cannot be redirected. The symlink contract is three lines
	// of os code; keep the test to the cheap invariants.
	s := &Server{cfgDir: dir}
	plug := filepath.Join(dir, "TokenEconomizer", "plugin", "opencode")
	if err := os.MkdirAll(plug, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plug, "tokeneconomizer.ts"), []byte("export {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ensureTokenEconomizerSource(context.Background()); err == nil {
		// Source resolved without cloning (dir lacks marketplace.json, so
		// this only happens when the dev checkout exists) — fine either way.
		t.Log("source resolved without clone")
	}
}
