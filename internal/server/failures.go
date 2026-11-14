// Failure tagging (issue #4): MAST-taxonomy records of orchestrator failures
// — fc1_brief (defective task brief), fc2_handoff (brain/worker misalignment),
// fc3_tool (tool misuse or tool error), other. Auto-recorded on dispatch
// errors and brief warnings; manual notes via POST /api/failures; JSONL
// export for offline analysis.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

var failureCategories = map[string]bool{
	"fc1_brief": true, "fc2_handoff": true, "fc3_tool": true, "other": true,
}

// handleFailureList handles GET /api/failures and POST /api/failures.
func (s *Server) handleFailureList(w http.ResponseWriter, r *http.Request) {
	user := s.auth.User(r)
	if r.Method == http.MethodPost {
		var in struct {
			Agent    string `json:"agent"`
			Category string `json:"category"`
			Note     string `json:"note"`
			ConvID   int64  `json:"conv_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		in.Category = strings.TrimSpace(in.Category)
		if !failureCategories[in.Category] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "category must be one of fc1_brief, fc2_handoff, fc3_tool, other"})
			return
		}
		if err := s.store.LogFailure(in.ConvID, user, in.Agent, in.Category, strings.TrimSpace(in.Note)); err != nil {
			log.Printf("server: failure tag store: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	rows, err := s.store.ListFailures(user, 100)
	if err != nil {
		log.Printf("server: failure list: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"failures": rows})
}

// tagFailure auto-records a failure tag; never fails the dispatch path.
func (s *Server) tagFailure(convID int64, user, agentName, category, note string) {
	if len(note) > 500 {
		note = note[:500]
	}
	if err := s.store.LogFailure(convID, user, agentName, category, note); err != nil {
		log.Printf("server: auto failure tag (%s agent=%s): %v", category, agentName, err)
	}
}

// handleFailureExport handles GET /api/failures/export — JSONL, one record
// per line, chronological.
func (s *Server) handleFailureExport(w http.ResponseWriter, r *http.Request) {
	user := s.auth.User(r)
	rows, err := s.store.FailuresForExport(user)
	if err != nil {
		log.Printf("server: failure export: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "export failed"})
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="fusion-console-failures.jsonl"`)
	enc := json.NewEncoder(w)
	for _, row := range rows {
		_ = enc.Encode(row)
	}
}
