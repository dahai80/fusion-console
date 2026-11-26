package server

import (
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dahai80/fusion-console/internal/config"
)

// artifact.go serves worker deliverables (GET /api/artifact?path=...) so files
// named in chat receipts and brain summaries open straight from the
// conversation. Sandbox: the file must resolve under the brain workspace root
// or one of the configured agent/worker repos and work dirs — exactly the
// trees dispatched agents are allowed to write. Auth-gated like every route.

// maxArtifactBytes caps any single served deliverable.
const maxArtifactBytes = 512 << 20

// artifactRoots lists every directory dispatched agents can write to: the
// brain workspace (agentsRoot / brainBaseDir), each agent's resolved work dir,
// and each worker's explicit work dir or repo binding.
func (s *Server) artifactRoots() []string {
	seen := map[string]bool{}
	var roots []string
	add := func(dir string) {
		if dir == "" {
			return
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		if !seen[dir] {
			seen[dir] = true
			roots = append(roots, dir)
		}
	}
	add(s.agentsRoot())
	add(s.brainBaseDir())
	for _, a := range s.cfg.Agent {
		add(config.ConfiguredWorkDir(a.WorkDir))
	}
	for _, wk := range s.cfg.Orchestrator.Workers {
		add(config.ConfiguredWorkDir(wk.WorkDir))
		if repo := strings.TrimSpace(wk.Repo); repo != "" && !isRepoURL(repo) {
			add(repo)
		}
	}
	return roots
}

func (s *Server) pathInArtifactRoots(path string) bool {
	for _, root := range s.artifactRoots() {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// artifactContentType maps deliverable extensions to a content type. Anything
// the browser could execute inline (html/svg) is forced to attachment with a
// neutral octet-stream type so a crafted artifact cannot run in console origin.
func artifactContentType(path string) (ctype string, inline bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		return "application/pdf", true
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".txt":
		return "text/plain; charset=utf-8", true
	case ".md":
		return "text/plain; charset=utf-8", true
	case ".csv":
		return "text/csv; charset=utf-8", true
	case ".json":
		return "application/json", true
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation", false
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", false
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", false
	case ".key":
		return "application/x-iwork-keynote-sffkey", false
	default:
		return "application/octet-stream", false
	}
}

// handleArtifact serves GET /api/artifact?path=<absolute or ~/ path>.
func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			raw = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(raw, "~"), "/"))
		}
	}
	if !filepath.IsAbs(raw) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be absolute or start with ~/"})
		return
	}
	// EvalSymlinks both resolves worker-worktree indirection and rejects
	// paths that escape via a symlink pointing outside every root.
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found: " + raw})
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found: " + raw})
		return
	}
	if info.Size() > maxArtifactBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "file exceeds the 512 MiB artifact limit"})
		return
	}
	if !s.pathInArtifactRoots(resolved) {
		log.Printf("server: artifact download denied user=%s path=%s (outside worker roots)", s.auth.User(r), raw)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path is outside every agent workspace"})
		return
	}
	ctype, inline := artifactContentType(resolved)
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	name := filepath.Base(resolved)
	w.Header().Set("Content-Type", ctype)
	if cd := mime.FormatMediaType(disposition, map[string]string{"filename": name}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	log.Printf("server: artifact download user=%s path=%s bytes=%d", s.auth.User(r), raw, info.Size())
	f, err := os.Open(resolved)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	io.Copy(w, f)
}
