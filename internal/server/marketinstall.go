// One-click skill install from the Skills Market. The console resolves the
// entry's payload, verifies it, and unpacks it into ~/.claude/skills/<name>.
// Two integrity anchors, matching fusion-skills CONTRIBUTING:
//
//   - pinned artifacts[] (url + sha256 + size): download, check sha256 and
//     size, then unpack the tar.gz;
//   - in-repo source_path: resolve main to a commit SHA, fetch that commit's
//     tarball and extract only source_path (content addressed by the SHA).
//
// Only kind=skill installs in one click; plugins, connectors and apps keep the
// Releases link because their install targets are host-specific.
package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Network seams (overridden in tests).
var (
	marketAPIBase      = "https://api.github.com"
	marketCodeloadBase = "https://codeload.github.com"
	marketHTTP         = &http.Client{Timeout: 90 * time.Second}
	marketUseGH        = true // fall back to the gh CLI when GitHub hosts are unreachable
)

const (
	marketMaxDownload = 64 << 20 // per archive
	marketMaxUnpacked = 64 << 20 // total extracted bytes
	marketMaxFiles    = 5000
	marketMarkerFile  = ".fusion-market.json"
	marketInstallTTL  = 3 * time.Minute
)

var (
	errSkillExists   = errors.New("a skill with this name is already installed")
	errSkillIsLink   = errors.New("the installed skill is a symlink")
	marketInstallMu  sync.Mutex // one install at a time: installs swap directories
	marketCommitRe   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	marketSHA256Re   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	marketTarGzRe    = regexp.MustCompile(`(?i)\.(tar\.gz|tgz)$`)
	marketSourcePath = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*/?$`)
)

// marketInstalled is written next to SKILL.md as .fusion-market.json and
// echoed back in the registry response.
type marketInstalled struct {
	ID          string `json:"id,omitempty"`
	Version     string `json:"version,omitempty"`
	Anchor      string `json:"anchor"`                 // sha256 | commit | local
	Digest      string `json:"digest,omitempty"`       // sha256 hex or commit SHA
	InstalledAt string `json:"installed_at,omitempty"` // RFC 3339, UTC
	Link        bool   `json:"link,omitempty"`         // symlinked skill: never replaced
}

// marketSkillName maps a registry entry to its skill directory name.
func marketSkillName(e *marketEntry) (string, error) {
	n := e.Name
	if n == "" {
		n = e.ID
	}
	return sanitizeSkillName(strings.ReplaceAll(n, "_", "-"))
}

// pickMarketArtifact prefers an exact os/arch match, then any wildcard match.
func pickMarketArtifact(arts []marketArtifact, goos, goarch string) *marketArtifact {
	var wild *marketArtifact
	for i := range arts {
		a := &arts[i]
		if (a.OS != goos && a.OS != "any") || (a.Arch != goarch && a.Arch != "any") {
			continue
		}
		if a.OS == goos && a.Arch == goarch {
			return a
		}
		if wild == nil {
			wild = a
		}
	}
	return wild
}

// marketSkillArtifact returns the host's tar.gz artifact for a skill, if any.
func marketSkillArtifact(e *marketEntry) *marketArtifact {
	a := pickMarketArtifact(e.Artifacts, runtime.GOOS, runtime.GOARCH)
	if a == nil || !marketTarGzRe.MatchString(a.URL) || !marketSHA256Re.MatchString(a.SHA256) {
		return nil
	}
	return a
}

// marketSkillSource is the in-repo directory of a skill: source_path, or the
// directory of a skills/<dir>/SKILL.md doc for entries that predate the field.
func marketSkillSource(e *marketEntry) string {
	if marketSourcePath.MatchString(e.SourcePath) {
		return e.SourcePath
	}
	if dir, file := path.Split(e.Doc); file == "SKILL.md" && strings.HasPrefix(dir, "skills/") && marketSourcePath.MatchString(dir) {
		return dir
	}
	return ""
}

func marketInstallable(e *marketEntry) bool {
	if e.Kind != "skill" {
		return false
	}
	if _, err := marketSkillName(e); err != nil {
		return false
	}
	return marketSkillArtifact(e) != nil || marketSkillSource(e) != ""
}

// annotateMarketInstalled fills Installed for skill entries from what is on
// disk under root; entries are a per-request copy.
func annotateMarketInstalled(entries []marketEntry, root string) {
	for i := range entries {
		e := &entries[i]
		e.Installable = marketInstallable(e)
		e.Installed = nil
		if e.Kind != "skill" {
			continue
		}
		name, err := marketSkillName(e)
		if err != nil {
			continue
		}
		e.Installed = readMarketInstalled(filepath.Join(root, name))
	}
}

// readMarketInstalled reports what occupies a skill directory: nil when
// nothing, the marker when the market installed it, "local" otherwise.
func readMarketInstalled(dir string) *marketInstalled {
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return &marketInstalled{Anchor: "local", Link: true}
	}
	if !fi.IsDir() {
		return nil
	}
	if data, err := os.ReadFile(filepath.Join(dir, marketMarkerFile)); err == nil {
		var m marketInstalled
		if json.Unmarshal(data, &m) == nil && m.Anchor != "" {
			return &m
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
		return &marketInstalled{Anchor: "local"}
	}
	return nil
}

// handleMarketInstall serves POST /api/market/install {id, overwrite}.
func (s *Server) handleMarketInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string `json:"id"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body needs an entry id"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), marketInstallTTL)
	defer cancel()
	s.marketMu.Lock()
	reg, _, err := s.marketRegistryLocked(ctx)
	s.marketMu.Unlock()
	if err != nil {
		serverError(w, err, http.StatusBadGateway)
		return
	}
	var entry *marketEntry
	for i := range reg.Entries {
		if reg.Entries[i].ID == req.ID {
			e := reg.Entries[i]
			entry = &e
			break
		}
	}
	if entry == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no market entry " + req.ID})
		return
	}
	if entry.Kind != "skill" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "one-click install covers skills; install this " + entry.Kind + " from its Releases page"})
		return
	}
	res, err := installMarketSkill(ctx, entry, globalSkillsDir(), req.Overwrite)
	switch {
	case errors.Is(err, errSkillExists):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "exists": true})
	case errors.Is(err, errSkillIsLink):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error() + " — it points elsewhere and is left untouched", "link": true})
	case err != nil:
		serverError(w, fmt.Errorf("install %s: %v", entry.ID, err), http.StatusBadGateway)
	default:
		log.Printf("server: market installed %s -> %s (%s %s)", entry.ID, res.Path, res.Installed.Anchor, res.Installed.Digest)
		writeJSON(w, http.StatusOK, res)
	}
}

type marketInstallResult struct {
	Name      string          `json:"name"`
	Path      string          `json:"path"`
	Installed marketInstalled `json:"installed"`
	Warnings  []string        `json:"warnings"`
}

// installMarketSkill downloads, verifies and unpacks a skill into
// root/<name>. The new tree is staged in a temp dir under root and swapped in
// only after it contains a SKILL.md; a symlinked skill is never replaced.
func installMarketSkill(ctx context.Context, e *marketEntry, root string, overwrite bool) (*marketInstallResult, error) {
	name, err := marketSkillName(e)
	if err != nil {
		return nil, err
	}
	marketInstallMu.Lock()
	defer marketInstallMu.Unlock()
	target := filepath.Join(root, name)
	exists := false
	if fi, err := os.Lstat(target); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, errSkillIsLink
		}
		if !overwrite {
			return nil, errSkillExists
		}
		exists = true
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(root, ".market-"+name+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	inst := marketInstalled{ID: e.ID, Version: e.Version, InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	if a := marketSkillArtifact(e); a != nil {
		data, err := marketFetch(ctx, a.URL, "")
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, a.SHA256) {
			return nil, fmt.Errorf("sha256 mismatch: registry pins %s, download is %s", strings.ToLower(a.SHA256), got)
		}
		if a.Size > 0 && int64(len(data)) != a.Size {
			return nil, fmt.Errorf("size mismatch: registry pins %d bytes, download is %d", a.Size, len(data))
		}
		if _, err := extractSkillTarGz(data, tmp, "", false); err != nil {
			return nil, err
		}
		inst.Anchor, inst.Digest = "sha256", got
	} else if src := marketSkillSource(e); src != "" {
		sha, err := marketResolveCommit(ctx)
		if err != nil {
			return nil, err
		}
		data, err := marketFetch(ctx, marketCodeloadBase+"/"+marketRepoSlug+"/tar.gz/"+sha, "repos/"+marketRepoSlug+"/tarball/"+sha)
		if err != nil {
			return nil, err
		}
		prefix := strings.TrimSuffix(path.Clean(src), "/") + "/"
		n, err := extractSkillTarGz(data, tmp, prefix, true)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("%s not found in %s@%s", src, marketRepoSlug, sha[:7])
		}
		inst.Anchor, inst.Digest = "commit", sha
	} else {
		return nil, fmt.Errorf("entry %s has neither a tar.gz artifact for %s/%s nor a source_path", e.ID, runtime.GOOS, runtime.GOARCH)
	}

	skillRoot, err := locateSkillRoot(tmp)
	if err != nil {
		return nil, err
	}
	marker, _ := json.MarshalIndent(inst, "", "  ")
	if err := os.WriteFile(filepath.Join(skillRoot, marketMarkerFile), append(marker, '\n'), 0o644); err != nil {
		return nil, err
	}
	if exists {
		// Park the old tree outside the staging dir so it can never end up
		// inside the new skill, and restore it if the swap fails.
		park, err := os.MkdirTemp(root, ".market-old-"+name+"-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(park)
		old := filepath.Join(park, name)
		if err := os.Rename(target, old); err != nil {
			return nil, fmt.Errorf("move existing skill aside: %v", err)
		}
		if err := os.Rename(skillRoot, target); err != nil {
			_ = os.Rename(old, target)
			return nil, err
		}
	} else if err := os.Rename(skillRoot, target); err != nil {
		return nil, err
	}
	content, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	warnings := auditSkillContent(string(content))
	if warnings == nil {
		warnings = []string{}
	}
	return &marketInstallResult{Name: name, Path: filepath.Join(target, "SKILL.md"), Installed: inst, Warnings: warnings}, nil
}

// locateSkillRoot accepts SKILL.md at the top of dir or inside a single
// wrapping directory (archives packed with or without a top folder).
func locateSkillRoot(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
		return dir, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var only string
	for _, de := range ents {
		if !de.IsDir() || only != "" {
			return "", errors.New("archive has no SKILL.md at its root")
		}
		only = filepath.Join(dir, de.Name())
	}
	if only != "" {
		if _, err := os.Stat(filepath.Join(only, "SKILL.md")); err == nil {
			return only, nil
		}
	}
	return "", errors.New("archive has no SKILL.md at its root")
}

// extractSkillTarGz writes the regular files of a gzip'd tar into dst and
// returns how many it wrote. strip drops the first path component (GitHub
// tarballs wrap everything in <owner>-<repo>-<sha>/); prefix, when set, keeps
// only that subtree and re-roots it at dst. Absolute paths, "..", links and
// special files inside the kept tree are rejected; file count and total size
// are capped.
func extractSkillTarGz(data []byte, dst, prefix string, strip bool) (int, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return 0, fmt.Errorf("not a gzip archive: %v", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	files := 0
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return files, fmt.Errorf("read archive: %v", err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeXHeader {
			continue
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if strings.Contains(name, "\\") || path.IsAbs(name) {
			return files, fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		if strip {
			i := strings.Index(name, "/")
			if i < 0 {
				continue // the wrapping directory itself
			}
			name = name[i+1:]
		}
		if prefix != "" {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			name = name[len(prefix):]
		}
		if name == "" || name == "." {
			continue
		}
		clean := path.Clean(name)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return files, fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		out := filepath.Join(dst, filepath.FromSlash(clean))
		if !strings.HasPrefix(out, filepath.Clean(dst)+string(os.PathSeparator)) {
			return files, fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(out, 0o755); err != nil {
				return files, err
			}
		case tar.TypeReg:
			files++
			total += hdr.Size
			if files > marketMaxFiles || total > marketMaxUnpacked {
				return files, fmt.Errorf("archive too large (over %d files or %d MB)", marketMaxFiles, marketMaxUnpacked>>20)
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return files, err
			}
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return files, err
			}
			_, cerr := io.CopyN(f, tr, hdr.Size)
			if err := f.Close(); cerr == nil {
				cerr = err
			}
			if cerr != nil {
				return files, cerr
			}
		default:
			return files, fmt.Errorf("archive entry %q is a link or special file; refusing to install", hdr.Name)
		}
	}
	return files, nil
}

// marketFetch downloads url with a size cap; when ghPath is set and the host
// is unreachable, retries through `gh api <ghPath>` (authenticated API access
// survives DNS/TLS blocks on GitHub's download hosts, common on CN networks).
func marketFetch(ctx context.Context, url, ghPath string) ([]byte, error) {
	data, err := marketGet(ctx, url, "")
	if err == nil || ghPath == "" || !marketUseGH {
		return data, err
	}
	log.Printf("server: market fetch %s failed (%v) — falling back to gh api", url, err)
	out, gerr := exec.CommandContext(ctx, "gh", "api", ghPath).Output()
	if gerr != nil {
		return nil, fmt.Errorf("%v; gh api: %v", err, gerr)
	}
	if len(out) > marketMaxDownload {
		return nil, fmt.Errorf("download over %d MB", marketMaxDownload>>20)
	}
	return out, nil
}

func marketGet(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := marketHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d from %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, marketMaxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > marketMaxDownload {
		return nil, fmt.Errorf("download over %d MB: %s", marketMaxDownload>>20, url)
	}
	return data, nil
}

// marketResolveCommit pins fusion-skills main to a commit SHA so the
// extracted tree is exactly that commit's content.
func marketResolveCommit(ctx context.Context) (string, error) {
	body, err := marketGet(ctx, marketAPIBase+"/repos/"+marketRepoSlug+"/commits/main", "application/vnd.github.sha")
	if err == nil {
		if sha := strings.TrimSpace(string(body)); marketCommitRe.MatchString(sha) {
			return sha, nil
		}
		err = fmt.Errorf("unexpected commit response %q", truncateUTF8(string(body), 80))
	}
	if !marketUseGH {
		return "", fmt.Errorf("resolve %s main: %v", marketRepoSlug, err)
	}
	out, gerr := exec.CommandContext(ctx, "gh", "api", "repos/"+marketRepoSlug+"/commits/main", "--jq", ".sha").Output()
	if sha := strings.TrimSpace(string(out)); gerr == nil && marketCommitRe.MatchString(sha) {
		return sha, nil
	}
	return "", fmt.Errorf("resolve %s main: %v; gh api: %v", marketRepoSlug, err, gerr)
}
