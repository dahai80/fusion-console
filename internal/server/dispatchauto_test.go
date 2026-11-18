package server

import (
	"testing"

	"github.com/dahai80/fusion-console/internal/config"
)

// autoDepthOf reads the chain counter for a conversation.
func autoDepthOf(t *testing.T, s *Server, convID int64) int {
	t.Helper()
	s.autoMu.Lock()
	defer s.autoMu.Unlock()
	return s.autoDepth[convID]
}

// TestAutoContinueGating covers the no-wake paths: dashboard-only runs, mode
// off, and chat-brain mode must not touch the chain counter.
func TestAutoContinueGating(t *testing.T) {
	s := &Server{cfg: &config.Config{Orchestrator: config.DefaultOrchestrator()}, autoDepth: map[int64]int{}}

	// Dashboard-only run (convID 0): never continues.
	s.maybeAutoContinue(&bgRun{ID: 1, ConversationID: 0})
	if got := autoDepthOf(t, s, 0); got != 0 {
		t.Fatalf("convID 0: depth = %d, want 0", got)
	}

	// Orchestration off: never continues.
	s.cfg.Orchestrator.Mode = "off"
	s.maybeAutoContinue(&bgRun{ID: 2, ConversationID: 7})
	if got := autoDepthOf(t, s, 7); got != 0 {
		t.Fatalf("mode off: depth = %d, want 0", got)
	}

	// Chat brain: the planner loop lives in the SSE handler — skip.
	s.cfg.Orchestrator.Mode = "auto"
	s.cfg.Orchestrator.BrainMode = "chat"
	s.maybeAutoContinue(&bgRun{ID: 3, ConversationID: 7})
	if got := autoDepthOf(t, s, 7); got != 0 {
		t.Fatalf("chat brain: depth = %d, want 0", got)
	}
}

// TestAutoContinueCap verifies the chain counter increments per finished run
// and that the wake is withheld once autoContinueMax is exceeded.
func TestAutoContinueCap(t *testing.T) {
	s := &Server{cfg: &config.Config{Orchestrator: config.DefaultOrchestrator()}, autoDepth: map[int64]int{}}
	s.cfg.Orchestrator.Mode = "auto"
	s.cfg.Orchestrator.BrainMode = "agent"

	// Drain to the cap. runAgentBrainTurn would spawn a real CLI, so test
	// only the withheld branch: a chain already at the cap must not wake the
	// brain again (the counter still records the attempt).
	for i := 0; i < autoContinueMax; i++ {
		s.autoMu.Lock()
		s.autoDepth[9]++
		s.autoMu.Unlock()
	}
	s.maybeAutoContinue(&bgRun{ID: 10, ConversationID: 9})
	if got := autoDepthOf(t, s, 9); got != autoContinueMax+1 {
		t.Fatalf("depth after over-cap run = %d, want %d", got, autoContinueMax+1)
	}
}
