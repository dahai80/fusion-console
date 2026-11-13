package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/auth"
	"github.com/dahai80/fusion-console/internal/config"
)

// initRepo creates a minimal git repo with one commit at dir.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test repo"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
}

func TestCreateWorktree(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	repo := filepath.Join(t.TempDir(), "fusion-demo")
	initRepo(t, repo)

	wt, err := s.createWorktree(repo, "claude")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
		t.Fatalf("worktree missing files: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(wt), "claude-wt-") {
		t.Errorf("unexpected worktree name %s", wt)
	}
	// whitelist: outside paths must fail
	s.cfg.Orchestrator.Repos = []string{filepath.Join(t.TempDir(), "other")}
	if _, err := s.createWorktree(repo, "claude"); err == nil {
		t.Error("non-whitelisted repo accepted")
	}
	// relative paths must fail
	s.cfg.Orchestrator.Repos = nil
	if _, err := s.createWorktree("relative/path", "claude"); err == nil {
		t.Error("relative repo path accepted")
	}
}

func TestWorkerConfigGuards(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	s.cfg.Orchestrator.Workers = []config.WorkerConfig{
		{Agent: "claude", Enabled: false, Isolation: "worktree", MaxBudgetUSD: 3},
	}
	if wk := s.workerConfig("claude"); wk == nil || wk.Enabled {
		t.Fatalf("workerConfig lookup wrong: %+v", wk)
	}
	if wk := s.workerConfig("codex"); wk != nil {
		t.Fatalf("unconfigured agent returned entry: %+v", wk)
	}
}

func TestApprovalEndpoint(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	cookie := loginCookie(t, s)
	ch := make(chan bool, 1)
	s.approvals.Store("appr-1-2", ch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/approvals/appr-1-2", strings.NewReader(`{"approve":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("approval POST status %d", rec.Code)
	}
	if ok := <-ch; !ok {
		t.Error("channel did not receive approve=true")
	}
	// second resolve must 404 (single-use gate)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/approvals/appr-1-2", strings.NewReader(`{"approve":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("replayed approval status = %d, want 404", rec.Code)
	}
}

func TestAuthHintLoopbackOnly(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	hash, err := auth.HashPassword("fusion-admin-2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	s.cfg.Users = []config.UserEntry{{Name: "admin", PasswordHash: hash}}

	// loopback: factory default active -> prefill allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/auth/hint", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	s.handleAuthHint(rec, req)
	var hint struct {
		FactoryDefault bool `json:"factory_default"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&hint)
	if !hint.FactoryDefault {
		t.Error("loopback + factory password: expected factory_default=true")
	}

	// remote: never prefill, even with factory password unchanged
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/auth/hint", nil)
	req.RemoteAddr = "10.1.2.3:5000"
	s.handleAuthHint(rec, req)
	_ = json.NewDecoder(rec.Body).Decode(&hint)
	if hint.FactoryDefault {
		t.Error("remote request must not reveal factory password state")
	}
}
