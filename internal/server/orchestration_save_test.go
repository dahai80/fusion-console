package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/config"
)

func saveOrchestration(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/settings/orchestration", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveOrchestration(rec, req)
	return rec
}

// TestSaveOrchestrationBrainAgentPinned: brain_agent is not free-form — only
// claude-compatible CLIs can run the agent brain — so unknown values are
// stored as "claude" instead of erroring (a hard error rejected every
// orchestration save once the chosen agent was disabled).
func TestSaveOrchestrationBrainAgentPinned(t *testing.T) {
	s := &Server{
		cfg:    &config.Config{Orchestrator: config.DefaultOrchestrator()},
		cfgDir: t.TempDir(),
		// claude disabled in Agent tools: only codex is registered.
		agents: map[string]agent.Adapter{"codex": agent.NewFor("codex", "", "codex")},
	}
	body := `{"mode":"confirm","brain_mode":"agent","brain_agent":"codex","workers":[{"name":"c1","agent":"codex","enabled":true}]}`
	if rec := saveOrchestration(t, s, body); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := s.cfg.Orchestrator.BrainAgent; got != agentBrainCLI {
		t.Fatalf("brain_agent = %q, want %q", got, agentBrainCLI)
	}
	if got := s.cfg.Orchestrator.BrainMode; got != "agent" {
		t.Fatalf("brain_mode = %q, want agent", got)
	}
	if len(s.cfg.Orchestrator.Workers) != 1 {
		t.Fatalf("workers not saved: %+v", s.cfg.Orchestrator.Workers)
	}
}

// TestSaveOrchestrationBrainAgentFusionCode: fusion-code speaks claude's
// protocol (same MCP flags + session resume), so it is accepted as the
// agent brain and stored verbatim.
func TestSaveOrchestrationBrainAgentFusionCode(t *testing.T) {
	s := &Server{
		cfg:    &config.Config{Orchestrator: config.DefaultOrchestrator()},
		cfgDir: t.TempDir(),
		agents: map[string]agent.Adapter{"fusion-code": agent.NewFor("fusion-code", "", "fusion-code")},
	}
	body := `{"mode":"confirm","brain_mode":"agent","brain_agent":"fusion-code","workers":[]}`
	if rec := saveOrchestration(t, s, body); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := s.cfg.Orchestrator.BrainAgent; got != "fusion-code" {
		t.Fatalf("brain_agent = %q, want fusion-code", got)
	}
}

func TestSaveOrchestrationBrainModeValidated(t *testing.T) {
	s := &Server{cfg: &config.Config{Orchestrator: config.DefaultOrchestrator()}, cfgDir: t.TempDir(), agents: map[string]agent.Adapter{}}
	for _, mode := range []string{"", "chat", "agent"} {
		if rec := saveOrchestration(t, s, `{"mode":"confirm","brain_mode":"`+mode+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("brain_mode %q: status = %d, body %s", mode, rec.Code, rec.Body.String())
		}
	}
	before := s.cfg.Orchestrator
	rec := saveOrchestration(t, s, `{"mode":"confirm","brain_mode":"codex"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "brain_mode") {
		t.Fatalf("bad brain_mode: status = %d, body %s", rec.Code, rec.Body.String())
	}
	if s.cfg.Orchestrator.BrainMode != before.BrainMode {
		t.Fatalf("rejected save changed brain_mode to %q", s.cfg.Orchestrator.BrainMode)
	}
}
