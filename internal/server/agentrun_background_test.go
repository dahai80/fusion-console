package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/store"
)

// TestAgentRunBackgroundValidation covers the direct background-dispatch
// branch of POST /api/agent/run: unknown agent and empty task must fail
// loudly before anything is enqueued.
func TestAgentRunBackgroundValidation(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	// Register the claude adapter so the empty-task check is exercised for
	// its own sake, not masked by the unknown-agent rejection.
	s.agents["claude"] = agent.NewFor("claude", "", "claude")
	cookie := loginCookie(t, s)
	do := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/agent/run", strings.NewReader(body))
		req.AddCookie(cookie)
		s.mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := do(`{"agent":"no-such-agent","task":"x","background":true}`); rec.Code != 400 {
		t.Errorf("unknown agent: status %d body %s", rec.Code, rec.Body.String())
	}
	if rec := do(`{"agent":"claude","task":"  ","background":true}`); rec.Code != 400 {
		t.Errorf("empty task: status %d body %s", rec.Code, rec.Body.String())
	}
}

// TestWorkerSlotsBounded verifies the semaphore shape: capacity matches the
// constant and a slot can be taken and released (no deadlock on the path the
// background goroutines use).
func TestWorkerSlotsBounded(t *testing.T) {
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), nil)
	if cap(s.agentSema) != maxConcurrentAgentRuns {
		t.Fatalf("slot capacity %d, want %d", cap(s.agentSema), maxConcurrentAgentRuns)
	}
	for i := 0; i < maxConcurrentAgentRuns; i++ {
		s.agentSema <- struct{}{}
	}
	select {
	case s.agentSema <- struct{}{}:
		t.Error("slot acquired beyond capacity")
	default:
	}
	<-s.agentSema
	select {
	case s.agentSema <- struct{}{}:
	default:
		t.Error("released slot not reacquirable")
	}
}

// TestAgentRunBackgroundPersistsUserTask verifies the direct background
// dispatch records the user task as a conversation message before the run
// starts, so the @-mention path has a transcript on par with interactive runs.
func TestAgentRunBackgroundPersistsUserTask(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_DIR", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "agentrun.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{Orchestrator: config.OrchestratorConfig{Mode: "off", WorkerMemory: "off", Workers: []config.WorkerConfig{{Name: "worker", Agent: "fixture", Enabled: true, Isolation: "standalone", WorkDir: t.TempDir()}}}}
	s := New(cfg, t.TempDir(), st)
	cookie := loginCookie(t, s)
	id, err := st.CreateConversation("admin", "persist-user-task", "chat", "", "")
	if err != nil {
		t.Fatal(err)
	}
	a := &dispatchTestAgent{starts: make(chan agent.RunOpts, 1), events: []agent.Event{{Type: agent.EventResult, Text: "done"}}}
	s.agents["fixture"] = a
	body := fmt.Sprintf(`{"agent":"worker","task":"explain Git and blocked","background":true,"conversation_id":%d}`, id)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/agent/run", strings.NewReader(body))
	req.AddCookie(cookie)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/agent/run: %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	var msgs []store.Message
	for {
		msgs, err = st.ListMessages(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for user task + receipt: %+v", msgs)
		}
		time.Sleep(time.Millisecond * 5)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "explain Git and blocked" {
		t.Fatalf("first message = role %q content %q, want user task", msgs[0].Role, msgs[0].Content)
	}
}
