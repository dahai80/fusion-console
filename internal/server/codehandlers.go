// HTTP handlers for the optional Coding Agent menu (fusion-code / claude /
// codex): status, per-agent install (background job), fusion-code
// start/stop, and the settings.json key binding.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
)

// codeStatusOut is the Coding Agent menu state payload. Agents covers all
// three installable CLIs; Fusion carries the fusion-code-specific service
// state (install job, ports, key binding).
type codeStatusOut struct {
	Agents []codeAgentStatus `json:"agents"`
	Fusion struct {
		Phase         string   `json:"phase"` // install job: idle|downloading|extracting|installing|done|error
		Logs          []string `json:"logs,omitempty"`
		Error         string   `json:"error,omitempty"`
		Port          int      `json:"port"`
		WebPort       int      `json:"web_port"`
		KeyConfigured bool     `json:"key_configured"`
	} `json:"fusion"`
}

func (s *Server) handleCodeStatus(w http.ResponseWriter, r *http.Request) {
	phase, logs, jobErr := s.codeInstall.snapshot()
	var out codeStatusOut
	out.Agents = s.codeAgents()
	out.Fusion.Phase = phase
	out.Fusion.Logs = logs
	out.Fusion.Error = jobErr
	out.Fusion.Port = s.cfg.Code.Port
	out.Fusion.WebPort = s.cfg.Code.WebPort
	out.Fusion.KeyConfigured = codeKeyConfigured()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCodeInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent string `json:"agent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Agent == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: agent required"})
		return
	}
	agent := codeAgentID(req.Agent)
	if agent != codeAgentFusion && agent != codeAgentClaude && agent != codeAgentCodex {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown agent " + req.Agent})
		return
	}
	if agent != codeAgentFusion {
		if p, err := exec.LookPath(string(agent)); err == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": p + " is already installed"})
			return
		}
	}
	phase, _, _ := s.codeInstall.snapshot()
	if phase == "downloading" || phase == "extracting" || phase == "installing" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "install already running"})
		return
	}
	if err := os.MkdirAll(s.cfg.Code.InstallDir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.codeInstall.mu.Lock()
	s.codeInstall.phase = "downloading"
	s.codeInstall.logs = nil
	s.codeInstall.err = ""
	s.codeInstall.cancel = cancel
	s.codeInstall.mu.Unlock()
	if err := s.installCodeAgent(ctx, agent, &s.codeInstall); err != nil {
		s.ragInstallFail(&s.codeInstall, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("server: code install started agent=%s", agent)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handleCodeStart(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Code.Installed {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fusion-code is not installed"})
		return
	}
	if err := s.startCode(); err != nil {
		log.Printf("server: code start failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"running": true})
}

func (s *Server) handleCodeStop(w http.ResponseWriter, r *http.Request) {
	if err := s.stopCode(); err != nil {
		log.Printf("server: code stop failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"running": false})
}

// handleCodeEnabled flips the config toggle (menu visibility / future wiring).
func (s *Server) handleCodeEnabled(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	s.cfg.Code.Enabled = *req.Enabled
	if err := s.cfg.Save(s.cfgDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("server: code enabled=%v", s.cfg.Code.Enabled)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": s.cfg.Code.Enabled})
}
