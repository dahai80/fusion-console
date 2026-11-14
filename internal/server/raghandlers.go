// HTTP handlers for the optional fusion-rag knowledge base menu: status,
// install (background job), start/stop, and the config toggle.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// ragStatusOut is the Knowledge Base menu state payload.
type ragStatusOut struct {
	Installed bool     `json:"installed"`
	Enabled   bool     `json:"enabled"`
	Running   bool     `json:"running"`
	Phase     string   `json:"phase"` // install job: idle|downloading|extracting|installing|done|error
	Logs      []string `json:"logs,omitempty"`
	Error     string   `json:"error,omitempty"`
	Port      int      `json:"port"`
}

func (s *Server) handleRagStatus(w http.ResponseWriter, r *http.Request) {
	phase, logs, jobErr := s.ragInstall.snapshot()
	writeJSON(w, http.StatusOK, ragStatusOut{
		Installed: s.cfg.Rag.Installed,
		Enabled:   s.cfg.Rag.Enabled,
		Running:   s.cfg.Rag.Installed && s.ragHealthy(time.Second),
		Phase:     phase,
		Logs:      logs,
		Error:     jobErr,
		Port:      s.cfg.Rag.Port,
	})
}

func (s *Server) handleRagInstall(w http.ResponseWriter, r *http.Request) {
	phase, _, _ := s.ragInstall.snapshot()
	if phase == "downloading" || phase == "extracting" || phase == "installing" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "install already running"})
		return
	}
	if err := os.MkdirAll(s.cfg.Rag.InstallDir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.ragInstall.mu.Lock()
	s.ragInstall.phase = "downloading"
	s.ragInstall.logs = nil
	s.ragInstall.err = ""
	s.ragInstall.cancel = cancel
	s.ragInstall.mu.Unlock()
	go s.installRAG(ctx, &s.ragInstall)
	log.Printf("server: rag install started dir=%s", s.cfg.Rag.InstallDir)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handleRagStart(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Rag.Installed {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fusion-rag is not installed"})
		return
	}
	if err := s.startRAG(); err != nil {
		log.Printf("server: rag start failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"running": true})
}

func (s *Server) handleRagStop(w http.ResponseWriter, r *http.Request) {
	if err := s.stopRAG(); err != nil {
		log.Printf("server: rag stop failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"running": false})
}

// handleRagEnabled flips the config toggle used by the chat MCP wiring.
func (s *Server) handleRagEnabled(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	s.cfg.Rag.Enabled = *req.Enabled
	if err := s.cfg.Save(s.cfgDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("server: rag enabled=%v", s.cfg.Rag.Enabled)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": s.cfg.Rag.Enabled})
}
