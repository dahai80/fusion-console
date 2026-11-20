// HTTP handlers for the optional fusion-memory hub menu: status, install
// (background job), start/stop. Mirrors the RAG menu shape. Route prefix is
// /api/memoryhub — /api/memory/status already serves the worker-memory
// dashboard surface (workermemory.go).
package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"
)

// memoryStatusOut is the Memory tab state payload.
type memoryStatusOut struct {
	Installed bool     `json:"installed"`
	Enabled   bool     `json:"enabled"`
	Running   bool     `json:"running"`
	Detail    string   `json:"detail,omitempty"` // fm-server binary path when present
	Phase     string   `json:"phase"`            // install job: idle|downloading|installing|done|error
	Logs      []string `json:"logs,omitempty"`
	Error     string   `json:"error,omitempty"`
	Port      int      `json:"port"`
}

func (s *Server) handleMemoryHubStatus(w http.ResponseWriter, r *http.Request) {
	phase, logs, jobErr := s.memoryInstall.snapshot()
	out := memoryStatusOut{
		Installed: s.cfg.Memory.Installed,
		Enabled:   s.cfg.Memory.Enabled,
		Running:   s.memoryHealthy(time.Second),
		Phase:     phase,
		Logs:      logs,
		Error:     jobErr,
		Port:      s.cfg.Memory.Port,
	}
	if p := s.memoryBinary(); p != "" {
		out.Detail = p
		if !out.Installed {
			// A fm-server binary arrived outside the console (manual build,
			// workspace tree) — it is still a usable hub.
			out.Installed = true
		}
	}
	if out.Running && !out.Installed {
		// The hub answers on :11435 even though no known binary layout was
		// found (started from a workspace tree, another install root) — it is
		// running, so there is nothing to install.
		out.Installed = true
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMemoryHubInstall(w http.ResponseWriter, r *http.Request) {
	phase, _, _ := s.memoryInstall.snapshot()
	if phase == "downloading" || phase == "extracting" || phase == "installing" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "install already running"})
		return
	}
	if _, err := os.Stat(s.cfg.Memory.InstallDir); os.IsNotExist(err) {
		if err := os.MkdirAll(s.cfg.Memory.InstallDir, 0o755); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.memoryInstall.mu.Lock()
	s.memoryInstall.phase = "downloading"
	s.memoryInstall.logs = nil
	s.memoryInstall.err = ""
	s.memoryInstall.cancel = cancel
	s.memoryInstall.mu.Unlock()
	go s.installMemory(ctx, &s.memoryInstall)
	log.Printf("server: memory install started dir=%s", s.cfg.Memory.InstallDir)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handleMemoryHubStart(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Memory.Installed {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fusion-memory is not installed"})
		return
	}
	if err := s.startMemory(); err != nil {
		log.Printf("server: memory start failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"running": true})
}

func (s *Server) handleMemoryHubStop(w http.ResponseWriter, r *http.Request) {
	if err := s.stopMemory(); err != nil {
		log.Printf("server: memory stop failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"running": false})
}
