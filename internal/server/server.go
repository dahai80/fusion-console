// Package server wires HTTP routes: static SPA, auth endpoints, chat SSE,
// agent SSE, config CRUD. All business routes sit behind session auth.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/auth"
	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/harness"
	"github.com/dahai80/fusion-console/internal/store"
)

// Server holds shared dependencies for all handlers.
type Server struct {
	cfg       *config.Config
	cfgDir    string
	auth      *auth.Manager
	store     *store.Store
	models    *chat.ModelList
	agents    map[string]agent.Adapter
	agentRuns sync.Mutex      // one agent subprocess per host at a time
	harness   *harness.Client // fusion-harness sidecar (chat guards)
	autoToken string          // one-time auto-login token for --open; empty = disabled
	mux       *http.ServeMux
}

// New builds the server and its route table.
func New(cfg *config.Config, cfgDir string, st *store.Store) *Server {
	s := &Server{
		cfg: cfg, cfgDir: cfgDir,
		auth:    auth.NewManager(),
		store:   st,
		models:  chat.NewModelList(),
		agents:  map[string]agent.Adapter{},
		harness: harness.New(),
	}
	for _, ad := range []agent.Adapter{
		agent.ClaudeAdapter{}, agent.FusionCodeAdapter{}, agent.CodexAdapter{}, agent.OpencodeAdapter{},
	} {
		s.agents[ad.Name()] = ad
	}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/health", s.handleHealth)
	m.HandleFunc("POST /api/login", s.handleLogin)
	m.HandleFunc("POST /api/autologin", s.handleAutoLogin)
	m.HandleFunc("POST /api/logout", s.handleLogout)
	m.HandleFunc("GET /api/me", s.auth.Require(s.handleMe))
	m.HandleFunc("GET /api/gateway/status", s.auth.Require(s.handleGatewayStatus))
	m.HandleFunc("POST /api/gateway/install", s.auth.Require(s.handleGatewayInstall))
	m.HandleFunc("POST /api/gateway/sso", s.auth.Require(s.handleGatewaySSO))

	m.HandleFunc("GET /api/conversations", s.auth.Require(s.handleListConversations))
	m.HandleFunc("POST /api/conversations", s.auth.Require(s.handleCreateConversation))
	m.HandleFunc("GET /api/conversations/{id}/messages", s.auth.Require(s.handleListMessages))
	m.HandleFunc("POST /api/chat", s.auth.Require(s.handleChat))
	m.HandleFunc("POST /api/agent/run", s.auth.Require(s.handleAgentRun))
	m.HandleFunc("GET /api/agents", s.auth.Require(s.handleAgents))

	m.HandleFunc("GET /api/settings/providers", s.auth.Require(s.handleListProviders))
	m.HandleFunc("POST /api/settings/providers", s.auth.Require(s.handleSaveProvider))
	m.HandleFunc("POST /api/settings/apikey", s.auth.Require(s.handleStoreAPIKey))
	m.HandleFunc("GET /api/settings/models", s.auth.Require(s.handleModels))
	m.HandleFunc("GET /api/costs", s.auth.Require(s.handleCosts))
	m.HandleFunc("GET /api/dashboard", s.auth.Require(s.handleDashboard))
	m.HandleFunc("GET /api/settings/orchestration", s.auth.Require(s.handleGetOrchestration))
	m.HandleFunc("POST /api/settings/orchestration", s.auth.Require(s.handleSaveOrchestration))

	m.HandleFunc("GET /", s.handleStatic)
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	u, ok := s.cfg.User(req.User)
	if !ok {
		log.Printf("server: login unknown user=%s", req.User)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if err := s.auth.Login(w, r, u.Name, u.PasswordHash, req.Password); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u.Name})
}

// SetAutoLoginToken arms a one-time auto-login token (used by --open so the
// browser that the console itself launched signs in without typing
// credentials). Only the first redemption succeeds.
func (s *Server) SetAutoLoginToken(token string) { s.autoToken = token }

// handleAutoLogin redeems the one-time auto-login token for a session cookie.
func (s *Server) handleAutoLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if s.autoToken == "" || req.Token != s.autoToken {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid auto-login token"})
		return
	}
	if len(s.cfg.Users) == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no users configured"})
		return
	}
	u := s.cfg.Users[0]
	s.autoToken = "" // one-time use
	if err := s.auth.LoginHash(w, r, u.Name); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "auto-login failed"})
		return
	}
	log.Printf("server: auto-login ok user=%s (one-time token)", u.Name)
	writeJSON(w, http.StatusOK, map[string]any{"user": u.Name})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.Logout(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": s.auth.User(r)})
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/login" || r.URL.Path == "/" || !strings.Contains(r.URL.Path, ".") {
		serveIndex(w)
		return
	}
	serveStatic(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func serverError(w http.ResponseWriter, err error, code int) {
	log.Printf("server: %v", err)
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
