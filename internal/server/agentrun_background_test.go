package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/config"
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
