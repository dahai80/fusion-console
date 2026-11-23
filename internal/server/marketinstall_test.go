package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarFile struct {
	name, body string
	typ        byte
	mode       int64
}

func makeTarGz(t *testing.T, files []tarFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		h := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg}
		if f.mode != 0 {
			h.Mode = f.mode
		}
		if f.typ == tar.TypeXGlobalHeader {
			// GitHub tarballs open with a pax global header carrying the commit.
			h = &tar.Header{Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "abababa"}}
		} else if f.typ != 0 {
			h.Typeflag = f.typ
			h.Size = 0
			if f.typ == tar.TypeSymlink {
				h.Linkname = f.body
			}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(f.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const testSkillMD = "---\nname: demo-skill\ndescription: demo\n---\n\n# Demo\n"

// serveMarket stands in for GitHub: /art.tar.gz (artifact), /api (commit
// lookup), /codeload (repo tarball at that commit).
func serveMarket(t *testing.T, art, repoTar []byte, sha string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/art.tar.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(art) })
	mux.HandleFunc("/api/repos/"+marketRepoSlug+"/commits/main", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github.sha" {
			http.Error(w, "want sha media type", http.StatusBadRequest)
			return
		}
		w.Write([]byte(sha))
	})
	mux.HandleFunc("/codeload/"+marketRepoSlug+"/tar.gz/"+sha, func(w http.ResponseWriter, r *http.Request) { w.Write(repoTar) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	oldAPI, oldCL, oldGH := marketAPIBase, marketCodeloadBase, marketUseGH
	marketAPIBase, marketCodeloadBase, marketUseGH = ts.URL+"/api", ts.URL+"/codeload", false
	t.Cleanup(func() { marketAPIBase, marketCodeloadBase, marketUseGH = oldAPI, oldCL, oldGH })
	t.Setenv("MARKET_TEST_BASE", ts.URL)
}

func sha256hex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestInstallMarketSkillFromArtifact(t *testing.T) {
	art := makeTarGz(t, []tarFile{{name: "./", typ: tar.TypeDir}, {name: "./SKILL.md", body: testSkillMD}, {name: "./scripts/run.sh", body: "echo hi\n", mode: 0o755}})
	serveMarket(t, art, nil, "")
	root := t.TempDir()
	e := &marketEntry{ID: "demo_skill", Name: "demo-skill", Kind: "skill", Version: "1.2.0",
		Artifacts: []marketArtifact{{OS: "any", Arch: "any", URL: os.Getenv("MARKET_TEST_BASE") + "/art.tar.gz", SHA256: sha256hex(art), Size: int64(len(art))}}}
	if !marketInstallable(e) {
		t.Fatal("entry with a tar.gz artifact should be installable")
	}
	res, err := installMarketSkill(context.Background(), e, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Installed.Anchor != "sha256" || res.Installed.Digest != sha256hex(art) {
		t.Fatalf("anchor = %+v", res.Installed)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "demo-skill", "SKILL.md")); string(b) != testSkillMD {
		t.Fatalf("SKILL.md = %q", b)
	}
	fi, err := os.Stat(filepath.Join(root, "demo-skill", "scripts", "run.sh"))
	if err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("script should keep its exec bit: %v %v", fi, err)
	}
	m := readMarketInstalled(filepath.Join(root, "demo-skill"))
	if m == nil || m.ID != "demo_skill" || m.Version != "1.2.0" {
		t.Fatalf("marker = %+v", m)
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, ".market-*"))
	if len(leftovers) != 0 {
		t.Fatalf("staging dirs left behind: %v", leftovers)
	}
}

func TestInstallMarketSkillRejectsChecksumMismatch(t *testing.T) {
	art := makeTarGz(t, []tarFile{{name: "SKILL.md", body: testSkillMD}})
	serveMarket(t, art, nil, "")
	root := t.TempDir()
	e := &marketEntry{ID: "demo", Name: "demo", Kind: "skill",
		Artifacts: []marketArtifact{{OS: "any", Arch: "any", URL: os.Getenv("MARKET_TEST_BASE") + "/art.tar.gz", SHA256: strings.Repeat("0", 64), Size: int64(len(art))}}}
	if _, err := installMarketSkill(context.Background(), e, root, false); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("want sha256 mismatch, got %v", err)
	}
	if ents, _ := os.ReadDir(root); len(ents) != 0 {
		t.Fatalf("nothing should be written on mismatch, found %d entries", len(ents))
	}
}

func TestInstallMarketSkillFromSourcePathPinsCommit(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	repoTar := makeTarGz(t, []tarFile{
		{name: "pax_global_header", typ: tar.TypeXGlobalHeader},
		{name: "dahai80-fusion-skills-abababa/", typ: tar.TypeDir},
		{name: "dahai80-fusion-skills-abababa/registry.json", body: "{}"},
		{name: "dahai80-fusion-skills-abababa/skills/demo/SKILL.md", body: testSkillMD},
		{name: "dahai80-fusion-skills-abababa/skills/demo/references/a.md", body: "ref"},
		{name: "dahai80-fusion-skills-abababa/skills/demo-other/SKILL.md", body: "other"},
	})
	serveMarket(t, nil, repoTar, sha)
	root := t.TempDir()
	e := &marketEntry{ID: "demo", Name: "demo", Kind: "skill", SourcePath: "skills/demo/"}
	res, err := installMarketSkill(context.Background(), e, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Installed.Anchor != "commit" || res.Installed.Digest != sha {
		t.Fatalf("anchor = %+v", res.Installed)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "demo", "references", "a.md")); string(b) != "ref" {
		t.Fatalf("reference file missing: %q", b)
	}
	for _, p := range []string{"registry.json", "demo-other"} {
		if _, err := os.Stat(filepath.Join(root, "demo", p)); err == nil {
			t.Fatalf("%s is outside source_path and must not be extracted", p)
		}
	}
}

func TestExtractSkillTarGzRejectsUnsafeEntries(t *testing.T) {
	for name, files := range map[string][]tarFile{
		"dotdot":  {{name: "../evil.md", body: "x"}},
		"nested":  {{name: "a/../../evil.md", body: "x"}},
		"symlink": {{name: "SKILL.md", body: testSkillMD}, {name: "link", typ: tar.TypeSymlink, body: "/etc/passwd"}},
	} {
		dst := t.TempDir()
		if _, err := extractSkillTarGz(makeTarGz(t, files), dst, "", false); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "evil.md")); err == nil {
			t.Fatalf("%s: file escaped the destination", name)
		}
	}
}

func TestInstallMarketSkillOverwriteAndLinks(t *testing.T) {
	art := makeTarGz(t, []tarFile{{name: "SKILL.md", body: testSkillMD}})
	serveMarket(t, art, nil, "")
	root := t.TempDir()
	e := &marketEntry{ID: "demo", Name: "demo", Kind: "skill",
		Artifacts: []marketArtifact{{OS: "any", Arch: "any", URL: os.Getenv("MARKET_TEST_BASE") + "/art.tar.gz", SHA256: sha256hex(art), Size: int64(len(art))}}}

	// A hand-made skill with the same name: refuse unless overwrite.
	if err := os.MkdirAll(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "demo", "SKILL.md"), []byte("local"), 0o644)
	os.WriteFile(filepath.Join(root, "demo", "stale.txt"), []byte("old"), 0o644)
	if got := readMarketInstalled(filepath.Join(root, "demo")); got == nil || got.Anchor != "local" {
		t.Fatalf("hand-made skill should read as local, got %+v", got)
	}
	if _, err := installMarketSkill(context.Background(), e, root, false); !errors.Is(err, errSkillExists) {
		t.Fatalf("want errSkillExists, got %v", err)
	}
	if _, err := installMarketSkill(context.Background(), e, root, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", "stale.txt")); err == nil {
		t.Fatal("overwrite should replace the whole tree")
	}
	if ents, _ := filepath.Glob(filepath.Join(root, ".market-*")); len(ents) != 0 {
		t.Fatalf("staging or parked dirs left behind: %v", ents)
	}

	// A symlinked skill (shared single source of truth) is never replaced.
	real := t.TempDir()
	os.WriteFile(filepath.Join(real, "SKILL.md"), []byte("truth"), 0o644)
	if err := os.Symlink(real, filepath.Join(root, "linked")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	le := *e
	le.ID, le.Name = "linked", "linked"
	if _, err := installMarketSkill(context.Background(), &le, root, true); !errors.Is(err, errSkillIsLink) {
		t.Fatalf("want errSkillIsLink, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(real, "SKILL.md")); string(b) != "truth" {
		t.Fatal("symlink target must stay untouched")
	}
}

func TestAnnotateMarketInstalled(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "web-search"), 0o755)
	os.WriteFile(filepath.Join(root, "web-search", "SKILL.md"), []byte(testSkillMD), 0o644)
	entries := []marketEntry{
		{ID: "web_search", Name: "web-search", Kind: "skill", SourcePath: "skills/web_search/"},
		{ID: "doc_writer", Name: "doc-writer", Kind: "skill", SourcePath: "skills/doc_writer/"},
		{ID: "file_access", Name: "file-access", Kind: "plugin"},
		{ID: "no_payload", Name: "no-payload", Kind: "skill"},
		{ID: "doc_only", Name: "doc-only", Kind: "skill", Doc: "skills/doc_only/SKILL.md"},
	}
	annotateMarketInstalled(entries, root)
	if entries[0].Installed == nil || entries[0].Installed.Anchor != "local" || !entries[0].Installable {
		t.Fatalf("web_search = %+v", entries[0])
	}
	if entries[1].Installed != nil || !entries[1].Installable {
		t.Fatalf("doc_writer = %+v", entries[1])
	}
	if entries[2].Installable || entries[2].Installed != nil {
		t.Fatalf("plugins are not one-click installable: %+v", entries[2])
	}
	if entries[3].Installable {
		t.Fatal("a skill without artifacts or source_path is not installable")
	}
	if !entries[4].Installable || marketSkillSource(&entries[4]) != "skills/doc_only/" {
		t.Fatalf("source dir should fall back to the SKILL.md doc dir: %+v", entries[4])
	}
}

func TestPickMarketArtifactPrefersExactMatch(t *testing.T) {
	arts := []marketArtifact{{OS: "any", Arch: "any", URL: "any"}, {OS: "darwin", Arch: "arm64", URL: "exact"}}
	if a := pickMarketArtifact(arts, "darwin", "arm64"); a == nil || a.URL != "exact" {
		t.Fatalf("got %+v", a)
	}
	if a := pickMarketArtifact(arts, "linux", "amd64"); a == nil || a.URL != "any" {
		t.Fatalf("got %+v", a)
	}
	if a := pickMarketArtifact(arts[1:], "linux", "amd64"); a != nil {
		t.Fatalf("no match expected, got %+v", a)
	}
}
