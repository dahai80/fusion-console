// Command skillsync re-vendors internal/skillcatalog/skills/ from the pinned
// upstream commits listed in internal/skillcatalog/catalog.json.
//
//	go run ./cmd/skillsync        (or: make skills-sync)
//
// Each skill directory is copied verbatim from the commit tarball. Sources
// whose license lives at the repo root (repo_license_file) get that file
// copied into every skill as LICENSE, so each vendored directory carries its
// own license. Directories not listed in the manifest are removed. The result
// is deterministic for a given manifest: review it with git diff.
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/skillcatalog"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	log.SetFlags(0)

	catalogDir := filepath.Join(*root, "internal", "skillcatalog")
	data, err := os.ReadFile(filepath.Join(catalogDir, "catalog.json"))
	if err != nil {
		log.Fatal(err)
	}
	var m skillcatalog.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		log.Fatalf("catalog.json: %v", err)
	}
	outDir := filepath.Join(catalogDir, "skills")

	wanted := map[string]bool{}
	for _, src := range m.Sources {
		var names []string
		for _, s := range m.Skills {
			if s.Source == src.ID {
				names = append(names, s.Name)
				wanted[s.Name] = true
			}
		}
		if len(names) == 0 {
			continue
		}
		if err := syncSource(src, names, outDir); err != nil {
			log.Fatalf("%s: %v", src.Repo, err)
		}
	}
	for _, s := range m.Skills {
		if _, ok := m.Source(s.Source); !ok {
			log.Fatalf("skill %s: unknown source %q", s.Name, s.Source)
		}
	}

	// Drop vendored directories that are no longer in the manifest.
	entries, _ := os.ReadDir(outDir)
	for _, e := range entries {
		if e.IsDir() && !wanted[e.Name()] {
			log.Printf("removing %s (not in catalog.json)", e.Name())
			if err := os.RemoveAll(filepath.Join(outDir, e.Name())); err != nil {
				log.Fatal(err)
			}
		}
	}
	if err := writeExecutables(outDir, filepath.Join(catalogDir, "executables.json")); err != nil {
		log.Fatal(err)
	}
	report(outDir)
}

// writeExecutables records the files that carry an executable bit, because
// go:embed does not preserve file modes.
func writeExecutables(outDir, dst string) error {
	list := []string{}
	err := filepath.WalkDir(outDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&0o111 != 0 {
			rel, _ := filepath.Rel(outDir, p)
			list = append(list, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(list)
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dst, append(data, '\n'), 0o644)
}

// syncSource downloads one pinned commit and replaces the listed skills.
func syncSource(src skillcatalog.Source, names []string, outDir string) error {
	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/%s", src.Repo, src.Commit)
	log.Printf("fetching %s@%s", src.Repo, src.Commit[:12])
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}

	staging, err := os.MkdirTemp(filepath.Dir(outDir), ".skillsync-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	var repoLicense []byte
	found := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Strip the "<repo>-<sha>/" prefix codeload adds.
		_, rel, ok := strings.Cut(hdr.Name, "/")
		if !ok || rel == "" {
			continue
		}
		rel = path.Clean(rel)
		if src.RepoLicenseFile != "" && rel == src.RepoLicenseFile && hdr.Typeflag == tar.TypeReg {
			if repoLicense, err = io.ReadAll(tr); err != nil {
				return err
			}
			continue
		}
		after, ok := strings.CutPrefix(rel, src.Base+"/")
		if !ok {
			continue
		}
		skill, inner, _ := strings.Cut(after, "/")
		if !want[skill] || inner == "" {
			continue
		}
		if strings.Contains(inner, "..") {
			return fmt.Errorf("%s: unsafe path %q", skill, inner)
		}
		switch hdr.Typeflag {
		case tar.TypeReg:
		case tar.TypeDir:
			continue
		default:
			log.Printf("  skip %s/%s (not a regular file)", skill, inner)
			continue
		}
		dst := filepath.Join(staging, skill, filepath.FromSlash(inner))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if hdr.Mode&0o111 != 0 {
			mode = 0o755
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		found[skill] = true
	}

	for _, n := range names {
		if !found[n] {
			return fmt.Errorf("skill %s not found under %s/ at %s", n, src.Base, src.Commit)
		}
		dir := filepath.Join(staging, n)
		if src.RepoLicenseFile != "" {
			if repoLicense == nil {
				return fmt.Errorf("repo license file %s not found", src.RepoLicenseFile)
			}
			if err := os.WriteFile(filepath.Join(dir, "LICENSE"), repoLicense, 0o644); err != nil {
				return err
			}
		}
		if _, err := os.Stat(filepath.Join(dir, src.LicenseFileName())); err != nil {
			return fmt.Errorf("skill %s has no %s — not redistributable as vendored", n, src.LicenseFileName())
		}
		final := filepath.Join(outDir, n)
		if err := os.RemoveAll(final); err != nil {
			return err
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
		if err := os.Rename(dir, final); err != nil {
			return err
		}
		log.Printf("  %s", n)
	}
	return nil
}

func report(outDir string) {
	type row struct {
		name  string
		files int
		bytes int64
	}
	var rows []row
	var total int64
	entries, _ := os.ReadDir(outDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		r := row{name: e.Name()}
		_ = filepath.WalkDir(filepath.Join(outDir, e.Name()), func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if info, err := d.Info(); err == nil {
					r.files++
					r.bytes += info.Size()
				}
			}
			return nil
		})
		total += r.bytes
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for _, r := range rows {
		log.Printf("%-32s %4d files %8.1f KB", r.name, r.files, float64(r.bytes)/1024)
	}
	log.Printf("total: %d skills, %.1f KB", len(rows), float64(total)/1024)
}
