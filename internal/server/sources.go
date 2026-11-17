// Unified source-lifecycle endpoint (design v2 §5.2/§6.2): the Models page
// drives fusion-gateway and macfabric through one surface,
// POST /api/sources/{id}/action, dispatched on Provider.Lifecycle.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/dahai80/fusion-console/internal/config"
)

// handleSourceAction serves POST /api/sources/{id}/action {action}.
func (s *Server) handleSourceAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Action string `json:"action"` // start | stop | install
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	var prov *configProviderRef
	for i := range s.cfg.Provider {
		if s.cfg.Provider[i].ID == id {
			prov = &configProviderRef{p: &s.cfg.Provider[i]}
		}
	}
	if prov == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider " + id})
		return
	}
	if req.Action != "start" && req.Action != "stop" && req.Action != "install" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be start, stop or install"})
		return
	}
	switch prov.p.Lifecycle {
	case config.LifecycleFusionGateway:
		s.gatewayAction(w, r, req.Action)
	case config.LifecycleMacfabric:
		s.macfabricAction(w, r, req.Action)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "provider " + id + " has no console-managed lifecycle (custom/external source)"})
	}
}

// configProviderRef keeps the alias short in the dispatch above.
type configProviderRef struct{ p *config.Provider }

func (s *Server) gatewayAction(w http.ResponseWriter, r *http.Request, action string) {
	ctx := r.Context()
	switch action {
	case "start":
		note, err := startGateway(ctx)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		s.invalidateModelCatalog()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": note})
	case "stop":
		note, err := stopGateway()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		s.invalidateModelCatalog()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": note})
	default: // install
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "use POST /api/gateway/install for fusion-gateway installation"})
	}
}

func (s *Server) macfabricAction(w http.ResponseWriter, r *http.Request, action string) {
	if runtime.GOOS != "darwin" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "macfabric is macOS (Apple Silicon) only"})
		return
	}
	switch action {
	case "start":
		note, err := macfabricStart(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		s.invalidateModelCatalog()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": note})
	case "stop":
		note, err := macfabricStop()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		s.invalidateModelCatalog()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": note})
	default: // install — background job, status via /api/sources/install-job
		if ph, _, _ := s.srcInstall.snapshot(); ph == "downloading" || ph == "extracting" || ph == "installing" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "an install job is already running"})
			return
		}
		s.srcInstall = ragInstallState{}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if err := s.macfabricInstall(ctx, &s.srcInstall); err != nil {
				s.ragInstallFail(&s.srcInstall, err)
			}
			s.invalidateModelCatalog()
		}()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": "install started"})
	}
}

// handleSourceInstallJob serves GET /api/sources/install-job — the streamed
// log/phase of the running (or last) source install job.
func (s *Server) handleSourceInstallJob(w http.ResponseWriter, r *http.Request) {
	phase, logs, errStr := s.srcInstall.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"phase": phase, "logs": logs, "error": errStr})
}
