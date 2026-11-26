package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/store"
)

func TestCreateAgentConversationWorkdir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workerDir := filepath.Join(home, "workspace")
	if err := os.Mkdir(workerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A leftover literal directory must never shadow the home workspace.
	t.Chdir(t.TempDir())
	tilde := "~/workspace"
	if err := os.MkdirAll(tilde, 0o700); err != nil {
		t.Fatal(err)
	}
	explicit := t.TempDir()
	empty := ""
	blank := " \t"
	missing := filepath.Join(home, "missing")
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	locked := t.TempDir()
	if err := os.Chmod(locked, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{
		Agent: []config.AgentSettings{
			{Name: "codex", Enabled: true, WorkDir: explicit},
			{Name: "claude", Enabled: true},
			{Name: "opencode", Enabled: true},
		},
		Orchestrator: config.OrchestratorConfig{WorkerMemory: "off", Workers: []config.WorkerConfig{
			{Name: "codex-1", Agent: "codex", Enabled: true, WorkDir: "~/workspace"},
			{Agent: "claude", Enabled: true, WorkDir: workerDir},
			{Name: "unconfigured", Agent: "codex", Enabled: true},
			{Name: "disabled", Agent: "codex", WorkDir: workerDir},
			{Name: "missing", Agent: "codex", Enabled: true, WorkDir: missing},
			{Name: "locked", Agent: "codex", Enabled: true, WorkDir: locked},
		}},
	}
	s := New(cfg, t.TempDir(), st)
	cookie := loginCookie(t, s)
	t.Run("manual run checks the expanded directory before launch", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/agent/run", bytes.NewBufferString(`{"agent":"codex","workdir":"~/missing"}`))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		var reply struct{ Error string }
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusBadRequest || reply.Error != "workdir not accessible: "+missing {
			t.Fatalf("got %d %s; expected rejection of %q", rec.Code, rec.Body.String(), missing)
		}
	})
	for _, tc := range []struct {
		name, target string
		workdir      *string // nil omits the field, as distinct from an explicit empty string
		wantDir      string
		wantError    string
	}{
		{"alias omitted", "codex-1", nil, workerDir, ""},
		{"alias empty", "codex-1", &empty, workerDir, ""},
		{"alias whitespace", "codex-1", &blank, workerDir, ""},
		{"legacy worker", "claude", nil, workerDir, ""},
		{"bare CLI configured", "codex", nil, explicit, ""},
		{"explicit overrides config", "codex-1", &explicit, explicit, ""},
		{"explicit tilde ignores cwd shadow", "codex", &tilde, workerDir, ""},
		{"unconfigured with explicit", "unconfigured", &explicit, explicit, ""},
		{"unconfigured", "unconfigured", nil, "", "agent session requires workdir"},
		{"bare CLI unconfigured", "opencode", nil, "", "agent session requires workdir"},
		{"unknown", "unknown", &explicit, "", "unknown agent unknown"},
		{"disabled with fallback", "disabled", nil, "", "worker disabled is disabled in settings"},
		{"disabled with explicit", "disabled", &explicit, "", "worker disabled is disabled in settings"},
		{"missing configured directory", "missing", nil, "", "workdir not accessible: " + missing},
		{"missing explicit directory", "codex-1", &missing, "", "workdir not accessible: " + missing},
		{"regular file", "codex-1", &file, "", "workdir not accessible: " + file},
		{"inaccessible configured directory", "locked", nil, "", "workdir not accessible: " + locked},
		{"inaccessible explicit directory", "codex-1", &locked, "", "workdir not accessible: " + locked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (tc.target == "locked" || tc.workdir == &locked) && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("requires POSIX directory search permissions and an unprivileged user")
			}
			body := map[string]string{"kind": "agent", "agent_name": tc.target}
			if tc.workdir != nil {
				body["workdir"] = *tc.workdir
			}
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/conversations", bytes.NewReader(data))
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			var reply struct {
				ID    int64
				Error string
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if tc.wantError != "" {
				if rec.Code != http.StatusBadRequest || reply.Error != tc.wantError {
					t.Fatalf("got %d %s, want 400 %q", rec.Code, rec.Body.String(), tc.wantError)
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
			conv, err := st.Conversation("admin", reply.ID)
			if err != nil {
				t.Fatal(err)
			}
			if conv.AgentName != tc.target || conv.Workdir != tc.wantDir {
				t.Fatalf("stored agent %q, dir %q; want %q, %q", conv.AgentName, conv.Workdir, tc.target, tc.wantDir)
			}
		})
	}
}
