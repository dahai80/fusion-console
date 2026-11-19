package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/auth"
	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/harness"
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

// TestCreateWorktreeUncommittedSnapshot (issue #45 F6): a worktree created
// from a repo with work-in-progress exposes that diff to the agent — tracked
// changes via a stash snapshot, untracked files copied — while the source
// repo stays untouched (no stash entry, working tree unchanged).
func TestCreateWorktreeUncommittedSnapshot(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	repo := filepath.Join(t.TempDir(), "fusion-demo")
	initRepo(t, repo)
	mod := filepath.Join(repo, "README.md")
	if err := os.WriteFile(mod, []byte("work in progress"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("untracked"), 0o644); err != nil {
		t.Fatalf("untracked: %v", err)
	}

	wt, err := s.createWorktree(repo, "claude")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(wt, "README.md"))
	if err != nil || string(got) != "work in progress" {
		t.Fatalf("tracked change missing in worktree: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "notes.txt")); err != nil {
		t.Fatalf("untracked file not copied: %v", err)
	}
	// Source repo untouched: working tree still dirty, no stash created.
	out, err := exec.Command("git", "-C", repo, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(string(out), "README.md") {
		t.Fatalf("source working tree changed: %s", out)
	}
	if stash, _ := exec.Command("git", "-C", repo, "stash", "list").CombinedOutput(); len(strings.TrimSpace(string(stash))) > 0 {
		t.Fatalf("source stash list not empty: %s", stash)
	}
}

// TestCreateWorktreeHiddenRef (issue #45 F6b): the hidden ref pins the
// worktree's HEAD in the source repo so the commits survive collection.
func TestCreateWorktreeHiddenRef(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	repo := filepath.Join(t.TempDir(), "fusion-demo")
	initRepo(t, repo)

	wt, err := s.createWorktree(repo, "claude")
	if err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	slug := filepath.Base(wt)
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "refs/console-tasks/"+slug).CombinedOutput()
	if err != nil {
		t.Fatalf("hidden ref missing: %v: %s", err, out)
	}
	ref := strings.TrimSpace(string(out))
	wtHead, err := exec.Command("git", "-C", wt, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("worktree head: %v", err)
	}
	if ref != strings.TrimSpace(string(wtHead)) {
		t.Fatalf("hidden ref %s != worktree HEAD %s", ref, wtHead)
	}
	// Cleanup must never remove the hidden ref.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	_ = exec.Command("git", "-C", repo, "worktree", "prune").Run()
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "refs/console-tasks/"+slug).CombinedOutput(); err != nil {
		t.Fatalf("hidden ref lost after cleanup: %v: %s", err, out)
	}
}

// TestPreserveFailedWorktree (issue #45 F6c): the marker records the failure
// and nothing is deleted.
func TestPreserveFailedWorktree(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	wt := t.TempDir()
	s.preserveFailedWorktree(wt, fmt.Errorf("agent claude failed: budget exceeded"))
	body, err := os.ReadFile(filepath.Join(wt, ".console-preserved"))
	if err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if !strings.Contains(string(body), "budget exceeded") {
		t.Fatalf("marker body = %s", body)
	}
	entries, err := os.ReadDir(wt)
	if err != nil || len(entries) == 0 {
		t.Fatalf("worktree contents lost: %v", err)
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

func TestProviderEnv(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_APIKEY_KEYANTH", "sk-test-anthropic")
	t.Setenv("FUSION_CONSOLE_APIKEY_KEYOAI", "sk-test-openai")
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	s.cfg.Provider = []config.Provider{
		{ID: "p-anth", Name: "anth", Type: "anthropic_compat", BaseURL: "https://anth.example", Model: "claude-sonnet-5-5", APIKeyID: "keyanth", Enabled: true},
		{ID: "p-oai", Name: "oai", Type: "openai_compat", BaseURL: "https://oai.example/v1", Model: "gpt-test", APIKeyID: "keyoai", Enabled: true},
		{ID: "p-off", Name: "off", Type: "openai_compat", BaseURL: "https://x.example", APIKeyID: "keyoai", Enabled: false},
	}
	s.cfg.Orchestrator.Workers = []config.WorkerConfig{
		{Agent: "claude", Enabled: true, ProviderID: "p-anth"},
		{Agent: "codex", Enabled: true, ProviderID: "p-oai"},
		{Agent: "opencode", Enabled: true, ProviderID: "p-off"},
		{Agent: "fusion-code", Enabled: true},
	}

	env, model, _ := s.providerEnv("claude", s.workerConfig("claude"))
	if env == nil || env["ANTHROPIC_BASE_URL"] != "https://anth.example" || env["ANTHROPIC_AUTH_TOKEN"] != "sk-test-anthropic" || env["ANTHROPIC_MODEL"] != "claude-sonnet-5-5" {
		t.Fatalf("anthropic env wrong: %v", env)
	}
	if model != "" {
		t.Fatalf("anthropic path must not force --model, got %q", model)
	}

	env, model, _ = s.providerEnv("codex", s.workerConfig("codex"))
	if env == nil || env["OPENAI_BASE_URL"] != "https://oai.example/v1" || env["OPENAI_API_KEY"] != "sk-test-openai" || model != "gpt-test" {
		t.Fatalf("openai env wrong: %v model=%q", env, model)
	}

	if env, _, _ := s.providerEnv("opencode", s.workerConfig("opencode")); env != nil {
		t.Fatalf("disabled provider must fall back to CLI default: %v", env)
	}
	if env, model, _ := s.providerEnv("fusion-code", s.workerConfig("fusion-code")); env != nil || model != "" {
		t.Fatalf("no provider configured must be empty: %v %q", env, model)
	}

	// Agent-settings override beats the wizard worker config.
	s.cfg.Agent = []config.AgentSettings{{Name: "claude", ProviderID: "p-oai"}}
	env, model, _ = s.providerEnv("claude", s.workerConfig("claude"))
	if env == nil || env["OPENAI_BASE_URL"] == "" || model != "gpt-test" {
		t.Fatalf("agent settings override not honored: %v %q", env, model)
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

func TestNormalizeRepoMatchesURLAndCheckoutPath(t *testing.T) {
	// A GitHub URL and the local checkout of the same repo must compare
	// equal — the brain dispatches with the checkout path it sees.
	if normalizeRepo("https://github.com/dahai80/fusion-web-shell") != normalizeRepo("/Users/dahai/fusion/fusion-web-shell") {
		t.Fatal("URL and checkout path of the same repo should match")
	}
	if normalizeRepo("https://github.com/dahai80/fusion-web-shell.git/") != normalizeRepo("https://github.com/dahai80/fusion-web-shell") {
		t.Fatal(".git suffix and trailing slash should not matter")
	}
	if normalizeRepo("https://github.com/dahai80/fusion-health") == normalizeRepo("/Users/dahai/fusion/fusion-web-shell") {
		t.Fatal("different repos must not match")
	}
}

func TestProviderEnvFallbackNotice(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_APIKEY_KEYOAI", "sk-test-openai")
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	s.cfg.Provider = []config.Provider{
		{ID: "p-off", Name: "off", Type: "openai_compat", BaseURL: "https://x.example", APIKeyID: "keyoai", Enabled: false},
		{ID: "p-nokey", Name: "nokey", Type: "openai_compat", BaseURL: "https://x.example", APIKeyID: "missing-key", Enabled: true},
		{ID: "p-oai", Name: "oai", Type: "openai_compat", BaseURL: "https://oai.example/v1", Model: "gpt-test", APIKeyID: "keyoai", Enabled: true},
	}
	s.cfg.Orchestrator.Workers = []config.WorkerConfig{
		{Agent: "claude", Enabled: true, ProviderID: "p-off"},
		{Agent: "codex", Enabled: true, ProviderID: "p-nokey"},
		{Agent: "opencode", Enabled: true, ProviderID: "p-oai"},
	}

	env, model, notice := s.providerEnv("claude", s.workerConfig("claude"))
	if env != nil || model != "" || notice == "" || !strings.Contains(notice, "p-off") {
		t.Fatalf("disabled provider: env=%v model=%q notice=%q", env, model, notice)
	}

	env, model, notice = s.providerEnv("codex", s.workerConfig("codex"))
	if env != nil || model != "" || notice == "" || !strings.Contains(notice, "nokey") {
		t.Fatalf("missing key: env=%v model=%q notice=%q", env, model, notice)
	}

	env, model, notice = s.providerEnv("opencode", s.workerConfig("opencode"))
	if env == nil || notice != "" {
		t.Fatalf("clean binding must have no notice: env=%v notice=%q", env, notice)
	}
}

func TestPlannerToolParameters(t *testing.T) {
	for _, tool := range orchestratorTools() {
		t.Run(tool.Name, func(t *testing.T) {
			if _, err := json.Marshal(tool); err != nil {
				t.Fatalf("encode planner tool: %v", err)
			}
			if tool.Name != "create_team" {
				return
			}
			var schema struct {
				Properties map[string]json.RawMessage
			}
			if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"team", "spec", "tasks", "lead_start", "lead_goal"} {
				if _, ok := schema.Properties[name]; !ok {
					t.Errorf("missing top-level property %q", name)
				}
			}
		})
	}
}

// TestAdmitDecision (issue #66): kernel needs_approval routes through the
// approval flow for any declared tool; undeclared tools never execute;
// sidecar errors deny closed; confirm mode gates run_agent when the kernel
// did not ask.
func TestAdmitDecision(t *testing.T) {
	tools := []chat.ToolDecl{{Name: "run_agent"}, {Name: "read_artifact"}}

	allowed, needsApproval, reason, _ := admitDecision(
		harness.Admit{Allowed: true, NeedsApproval: true}, "read_artifact", tools, false)
	if !allowed || !needsApproval {
		t.Fatalf("kernel approval request ignored: allowed=%v needs=%v", allowed, needsApproval)
	}

	// Kernel did not ask + confirm mode off: declared tool runs outright.
	allowed, needsApproval, _, _ = admitDecision(
		harness.Admit{Allowed: true}, "read_artifact", tools, false)
	if !allowed || needsApproval {
		t.Fatalf("plain allow must run: allowed=%v needs=%v", allowed, needsApproval)
	}

	// Kernel did not ask + confirm mode on: run_agent gates, others run.
	_, needsApproval, _, _ = admitDecision(harness.Admit{Allowed: true}, "run_agent", tools, true)
	if !needsApproval {
		t.Fatal("confirm mode must gate run_agent")
	}
	_, needsApproval, _, _ = admitDecision(harness.Admit{Allowed: true}, "read_artifact", tools, true)
	if needsApproval {
		t.Fatal("confirm mode must not gate non-dispatch tools")
	}

	// Kernel ask loses to undeclared tool — never execute foreign tools.
	allowed, _, reason, _ = admitDecision(
		harness.Admit{Allowed: true, NeedsApproval: true}, "rm_rf", tools, false)
	if allowed || reason != "tool not in planner tool set" {
		t.Fatalf("undeclared tool leaked: allowed=%v reason=%q", allowed, reason)
	}

	// Plain deny keeps the kernel reason and blocked_by.
	blockedBy := ""
	allowed, needsApproval, reason, blockedBy = admitDecision(
		harness.Admit{Reason: "destructive tool", BlockedBy: "permission_engine"}, "rm_rf", tools, false)
	if allowed || needsApproval || reason != "destructive tool" || blockedBy != "permission_engine" {
		t.Fatalf("deny mangled: allowed=%v needs=%v reason=%q blocked_by=%q", allowed, needsApproval, reason, blockedBy)
	}
}
