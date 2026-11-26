package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/config"
)

func artifactTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{cfg: &config.Config{Orchestrator: config.DefaultOrchestrator()}, cfgDir: t.TempDir()}
}

func TestArtifactServesFileInsideRoot(t *testing.T) {
	s := artifactTestServer(t)
	root := s.brainBaseDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "docs", "report.pptx")
	if err := os.WriteFile(file, []byte("PK-fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.handleArtifact(rec, httptest.NewRequest("GET", "/api/artifact?path="+file, nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "report.pptx") {
		t.Fatalf("disposition wrong: %q", cd)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "presentationml") {
		t.Fatalf("content type wrong: %q", ct)
	}
	if rec.Body.String() != "PK-fake" {
		t.Fatalf("body wrong: %q", rec.Body.String())
	}
}

func TestArtifactTildePathServes(t *testing.T) {
	s := artifactTestServer(t)
	root := s.brainBaseDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "notes.md")
	if err := os.WriteFile(file, []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.handleArtifact(rec, httptest.NewRequest("GET", "/api/artifact?path="+file, nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "inline") {
		t.Fatalf("markdown should serve inline, got %q", cd)
	}
}

func TestArtifactDeniesOutsideRoots(t *testing.T) {
	s := artifactTestServer(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.handleArtifact(rec, httptest.NewRequest("GET", "/api/artifact?path="+outside, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestArtifactDeniesSymlinkEscape(t *testing.T) {
	s := artifactTestServer(t)
	root := s.brainBaseDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.handleArtifact(rec, httptest.NewRequest("GET", "/api/artifact?path="+link, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestArtifactWorkerRepoAllowed(t *testing.T) {
	s := artifactTestServer(t)
	repo := t.TempDir()
	s.cfg.Orchestrator.Workers = []config.WorkerConfig{{Name: "code-research", Repo: repo}}
	file := filepath.Join(repo, "out.pptx")
	if err := os.WriteFile(file, []byte("PK"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.handleArtifact(rec, httptest.NewRequest("GET", "/api/artifact?path="+file, nil))
	if rec.Code != 200 {
		t.Fatalf("worker repo file must serve, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestArtifactMissingAndRelative(t *testing.T) {
	s := artifactTestServer(t)
	rec := httptest.NewRecorder()
	s.handleArtifact(rec, httptest.NewRequest("GET", "/api/artifact", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing path want 400, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.handleArtifact(rec, httptest.NewRequest("GET", "/api/artifact?path=relative/file.txt", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("relative path want 400, got %d", rec.Code)
	}
}
