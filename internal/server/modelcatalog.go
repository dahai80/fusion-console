// Model catalog: the unified feed behind the chat/agent model pickers
// (design v2 §4). Every enabled provider's pool, annotated with kind and —
// for gateway-type sources — a short reachability probe. Chat availability
// never depends on a gateway: direct items are always selectable.
package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/config"
)

const (
	catalogTTL      = 30 * time.Second
	catalogDialTO   = 300 * time.Millisecond
	catalogCacheKey = "catalog"
)

type modelCatalogItem struct {
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	Kind         string `json:"kind"` // "gateway" | "direct"
	Model        string `json:"model"`
	Reachable    bool   `json:"reachable"`
	Lifecycle    string `json:"lifecycle,omitempty"`
}

// handleModelCatalog serves GET /api/model-catalog.
func (s *Server) handleModelCatalog(w http.ResponseWriter, r *http.Request) {
	s.catMu.Lock()
	if s.catCache != nil && time.Since(s.catAt) < catalogTTL {
		items := s.catCache
		s.catMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	s.catMu.Unlock()

	items := s.buildModelCatalog()

	s.catMu.Lock()
	s.catCache = items
	s.catAt = time.Now()
	s.catMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// invalidateModelCatalog drops the reachability cache (called when providers
// change so the picker reflects edits immediately).
func (s *Server) invalidateModelCatalog() {
	s.catMu.Lock()
	s.catCache = nil
	s.catMu.Unlock()
}

// buildModelCatalog merges all enabled providers' pools. Gateway-type
// sources get a TCP reachability probe; everything else is assumed
// reachable (the testcall is the authoritative check for direct sources).
func (s *Server) buildModelCatalog() []modelCatalogItem {
	var items []modelCatalogItem
	for i := range s.cfg.Provider {
		p := &s.cfg.Provider[i]
		if !p.Enabled {
			continue
		}
		kind := "direct"
		if p.Type == config.ProviderGateway {
			kind = "gateway"
		}
		reachable := true
		if kind == "gateway" {
			reachable = probeTCPEndpoint(p.BaseURL, catalogDialTO)
		}
		models := p.Models
		if len(models) == 0 && p.Model != "" {
			models = []string{p.Model}
		}
		for _, m := range models {
			items = append(items, modelCatalogItem{
				ProviderID:   p.ID,
				ProviderName: p.Name,
				Kind:         kind,
				Model:        m,
				Reachable:    reachable,
				Lifecycle:    p.Lifecycle,
			})
		}
	}
	return items
}

// probeTCPEndpoint dials host:port parsed from a base URL.
func probeTCPEndpoint(baseURL string, timeout time.Duration) bool {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		switch u.Scheme {
		case "https":
			host += ":443"
		default:
			host += ":80"
		}
	}
	conn, err := net.DialTimeout("tcp", host, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
