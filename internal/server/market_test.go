package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const fixtureRegistry = `{"version":1,"repo":"https://github.com/dahai80/fusion-skills","entries":[
 {"id":"file_access","name":"file-access","kind":"plugin","category":"file","source":"fusion-plugins-ecosystem","description":{"zh":"文件","en":"file"},"doc":"plugins/file_access.md"},
 {"id":"fusion-console","name":"fusion-console","kind":"app","source":"fusion-console","releases":"https://github.com/dahai80/fusion-console/releases","doc":"apps/fusion-console.md"}
]}`

func TestParseMarketRegistryNormalizesURLs(t *testing.T) {
	reg, err := parseMarketRegistry([]byte(fixtureRegistry))
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Entries) != 2 {
		t.Fatalf("entries = %d", len(reg.Entries))
	}
	p := reg.Entries[0]
	if p.ReleasesURL != "https://github.com/dahai80/fusion-plugins-ecosystem/releases" {
		t.Fatalf("plugin releases_url = %q", p.ReleasesURL)
	}
	if p.DocURL != "https://github.com/dahai80/fusion-skills/blob/main/plugins/file_access.md" {
		t.Fatalf("doc_url = %q", p.DocURL)
	}
	a := reg.Entries[1]
	if a.ReleasesURL != "https://github.com/dahai80/fusion-console/releases" {
		t.Fatalf("app releases_url should honor upstream field, got %q", a.ReleasesURL)
	}
}

func TestParseMarketRegistryRejectsEmpty(t *testing.T) {
	if _, err := parseMarketRegistry([]byte(`{"version":1,"entries":[]}`)); err == nil {
		t.Fatal("expected error for empty entries")
	}
}

func TestMarketDiskCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfgDir: dir}
	if s.loadMarketDiskCache() != nil {
		t.Fatal("expected nil cache on empty dir")
	}
	reg, err := parseMarketRegistry([]byte(fixtureRegistry))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.saveMarketDiskCache(reg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "market-registry.json")); err != nil {
		t.Fatalf("cache file missing: %v", err)
	}
	got := s.loadMarketDiskCache()
	if got == nil || len(got.Entries) != 2 {
		t.Fatalf("roundtrip lost entries: %+v", got)
	}
}

func TestMarketStaleOnlyForDiskFallback(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfgDir: dir}
	reg, err := parseMarketRegistry([]byte(fixtureRegistry))
	if err != nil {
		t.Fatal(err)
	}
	s.marketCache = reg
	got, cached, err := s.marketRegistryLocked(context.Background())
	if err != nil || !cached || got.Stale {
		t.Fatalf("memory cache hit must not read as stale: cached=%v stale=%v err=%v", cached, got.Stale, err)
	}
}
