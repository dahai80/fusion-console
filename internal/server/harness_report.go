package server

import (
	"log"
	"net/http"
	"strconv"
)

// handleHarnessReport (issue #78) exposes the harness sidecar's report RPC:
// funnel snapshot (llm meter, budget, recent lifecycle events) plus failure
// attribution for one conversation. Read-only; 503 when the guard is off.
func (s *Server) handleHarnessReport(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Harness.Enabled || s.harness == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "harness guard unavailable"})
		return
	}
	convID, err := strconv.ParseInt(r.URL.Query().Get("conv_id"), 10, 64)
	if err != nil || convID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conv_id query param required"})
		return
	}
	rep, err := s.harness.Report(convID, s.cfg.Harness.BudgetTokens)
	if err != nil {
		log.Printf("harness report conv=%d failed: %v", convID, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "harness report failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
