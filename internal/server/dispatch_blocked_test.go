package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/store"
)

type dispatchTestAgent struct {
	starts chan agent.RunOpts
	events []agent.Event
}

func (*dispatchTestAgent) Name() string { return "fixture" }
func (*dispatchTestAgent) Detect() bool { return true }
func (a *dispatchTestAgent) Run(_ context.Context, opts agent.RunOpts) (<-chan agent.Event, error) {
	a.starts <- opts
	stream := make(chan agent.Event, len(a.events)+1)
	for _, ev := range a.events {
		stream <- ev
	}
	stream <- agent.Event{Type: agent.EventDone}
	close(stream)
	return stream, nil
}

func TestDispatchRequiresRepo(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_DIR", t.TempDir())
	repo := t.TempDir()
	initRepo(t, repo)
	wt := filepath.Join(t.TempDir(), "linked")
	runGit(t, repo, "worktree", "add", "--detach", wt, "HEAD")
	sub := filepath.Join(wt, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare")
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: missing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{
		Agent:        []config.AgentSettings{{Name: "fixture", WorkDir: repo, Enabled: true}},
		Orchestrator: config.OrchestratorConfig{WorkerMemory: "off", BrainWorkDir: t.TempDir()},
	}
	s := New(cfg, t.TempDir(), st)
	for _, tc := range []struct {
		name, dir, isolation, source string
		required, starts             bool
	}{
		{"checkout", repo, "standalone", "", true, true},
		{"linked worktree", wt, "standalone", "", true, true},
		{"linked worktree subdirectory", sub, "standalone", "", true, true},
		{"empty worker with valid CLI dir", empty, "standalone", "", true, false},
		{"ordinary task in empty dir", empty, "standalone", "", false, true},
		{"broken gitfile", broken, "standalone", "", true, false},
		{"bare repo", bare, "standalone", "", true, false},
		{"unbound worktree worker", empty, "worktree", "", true, false},
		{"unbound worktree worker with existing checkout", repo, "worktree", "", true, false},
		{"worktree created before check", empty, "worktree", repo, true, true},
		{"invalid worktree source", repo, "worktree", empty, true, false},
		{"github clone before check", t.TempDir(), "github", repo, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wk := config.WorkerConfig{Name: "worker", Agent: "fixture", Enabled: true, WorkDir: tc.dir, Isolation: tc.isolation, Repo: tc.source}
			s.cfg.Orchestrator.Workers = []config.WorkerConfig{wk}
			a := &dispatchTestAgent{starts: make(chan agent.RunOpts, 1), events: []agent.Event{{Type: agent.EventResult, Text: "ok"}}}
			s.agents["fixture"] = a
			args, _ := json.Marshal(dispatchArgs{Agent: "worker", RequiresRepo: tc.required, Task: "Explain Git repositories and the word blocked"})
			out, err := s.executeToolCall(t.Context(), chat.ToolCall{Name: "run_agent", Arguments: string(args)}, "admin", 0, nil)
			if tc.starts {
				if err != nil || out != "ok" || len(a.starts) != 1 {
					t.Fatalf("result %q, err %v, starts %d", out, err, len(a.starts))
				}
				opts := <-a.starts
				want := tc.dir
				switch tc.isolation {
				case "worktree":
					if filepath.Dir(opts.Workdir) != cfg.Orchestrator.BrainWorkDir {
						t.Fatalf("worktree in %s", opts.Workdir)
					}
					want = opts.Workdir
				case "github":
					want = filepath.Join(tc.dir, "repo")
				}
				if opts.Workdir != want || !strings.Contains(opts.Task, workerResultContract) {
					t.Fatalf("wrong dispatch workspace or missing result contract in %+v", opts)
				}
			} else {
				b, ok := err.(*dispatchBlocked)
				if !ok || b.Status != "blocked" || !strings.Contains(b.Reason, "Workers") || len(a.starts) != 0 {
					t.Fatalf("err %v, starts %d", err, len(a.starts))
				}
			}
		})
	}
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	t.Setenv("GIT_WORK_TREE", empty)
	if err := requireGitWorkspace(t.Context(), empty); err == nil {
		t.Fatal("accepted empty directory via unrelated environment checkout")
	}
}

func TestDispatchBlockedReceipt(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_DIR", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "dispatch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{Orchestrator: config.OrchestratorConfig{Mode: "off", WorkerMemory: "off", Workers: []config.WorkerConfig{{Name: "worker", Agent: "fixture", Enabled: true, Isolation: "standalone", WorkDir: t.TempDir()}}}}
	s := New(cfg, t.TempDir(), st)
	cookie := loginCookie(t, s)
	request := func(srv *Server, method, path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	const envelope = `{"status":"blocked","reason":"Need project approval"}`
	for _, tc := range []struct {
		name, result, status string
		requiresRepo, mcp    bool
	}{
		{"direct preflight", "", "blocked", true, false},
		{"MCP handoff preflight", "", "blocked", true, true},
		{"worker returned blocked normally", envelope, "blocked", false, false},
		{"empty completion", "   ", "blocked", false, false},
		{"ordinary prose", "A missing repo blocked the previous task", "done", false, false},
		{"quoted envelope", "Example: " + envelope, "done", false, false},
		{"incomplete envelope", `{"status":"blocked"}`, "done", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := st.CreateConversation("admin", tc.name, "chat", "", "")
			if err != nil {
				t.Fatal(err)
			}
			a := &dispatchTestAgent{starts: make(chan agent.RunOpts, 1), events: []agent.Event{{Type: agent.EventText, Text: envelope}, {Type: agent.EventResult, Text: tc.result}}}
			s.agents["fixture"] = a
			args := dispatchArgs{Agent: "worker", Task: "explain Git and blocked", Background: true, RequiresRepo: tc.requiresRepo}
			if tc.mcp {
				h := httptest.NewServer(s.mux)
				defer h.Close()
				cfg.Server.Port = h.Listener.Addr().(*net.TCPAddr).Port
				sidecar := &Server{cfg: cfg, mcpMode: true, dispatchTok: s.dispatchTok, agents: s.agents, store: st}
				data, _ := json.Marshal(args)
				if _, err := sidecar.executeToolCall(t.Context(), chat.ToolCall{Name: "run_agent", Arguments: string(data)}, "admin", id, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				data, _ := json.Marshal(dispatchHandoff{args, id})
				request(s, "POST", "/api/agent/run", data)
			}
			deadline := time.Now().Add(5 * time.Second)
			var msgs []store.Message
			for {
				msgs, err = st.ListMessages(id)
				if err != nil {
					t.Fatal(err)
				}
				// The user task is persisted up front; wait for the assistant
				// receipt, not just any message.
				hasReceipt := false
				for _, m := range msgs {
					if m.Role == "assistant" {
						hasReceipt = true
						break
					}
				}
				if hasReceipt {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("timed out waiting for chat receipt")
				}
				time.Sleep(time.Millisecond * 5)
			}
			var receipt []store.Message
			for _, m := range msgs {
				if m.Role == "assistant" {
					receipt = append(receipt, m)
				}
			}
			if len(receipt) != 1 || !strings.Contains(receipt[0].Meta, `"status":"`+tc.status+`"`) {
				t.Fatalf("wrong receipt: %+v", msgs)
			}
			if tc.requiresRepo != (len(a.starts) == 0) {
				t.Fatalf("starts = %d", len(a.starts))
			}
			checkCard := func(srv *Server, read bool) {
				w := request(srv, "GET", fmt.Sprintf("/api/dispatches?conversation_id=%d", id), nil)
				var cards struct {
					Runs   []bgRun       `json:"dispatches"`
					Unread map[int64]int `json:"unread_by_conv"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &cards); err != nil || len(cards.Runs) != 1 {
					t.Fatalf("cards: %s", w.Body.String())
				}
				run := cards.Runs[0]
				if run.Status != tc.status || run.RequiresRepo != tc.requiresRepo || run.Read != read || (cards.Unread[id] == 1) == read {
					t.Fatalf("wrong card: %s", w.Body.String())
				}
				if tc.status == "blocked" {
					if run.Detail == "" || !strings.Contains(receipt[0].Content, " blocked —") || !strings.Contains(receipt[0].Content, run.Detail) {
						t.Fatalf("card/receipt mismatch: %+v %q", run, receipt[0].Content)
					}
					if tc.result == envelope && run.Detail != "Need project approval" {
						t.Fatalf("reason = %q", run.Detail)
					}
				} else if run.Result != tc.result || !strings.Contains(receipt[0].Content, " finished —") {
					t.Fatalf("wrong successful result: %+v", run)
				}
			}
			checkCard(s, false)
			reloaded := &Server{store: st, auth: s.auth, mux: http.NewServeMux()}
			reloaded.routes()
			reloaded.loadPersistedRuns()
			checkCard(reloaded, false)
			request(reloaded, "POST", "/api/dispatches/ack", []byte(fmt.Sprintf(`{"conversation_id":%d}`, id)))
			checkCard(reloaded, true)
			request(reloaded, "POST", "/api/dispatches/clear", nil)
			rows, err := st.ListDispatchRuns(10)
			if err != nil || len(rows) != 0 || len(reloaded.bg.list(0)) != 0 {
				t.Fatalf("clear: rows %+v err %v", rows, err)
			}
		})
	}
}
