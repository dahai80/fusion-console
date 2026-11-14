// Package server wires HTTP routes: static SPA, auth endpoints, chat SSE,
// agent SSE, config CRUD. All business routes sit behind session auth.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
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
	cfg        *config.Config
	cfgDir     string
	auth       *auth.Manager
	store      *store.Store
	models     *chat.ModelList
	agents     map[string]agent.Adapter
	agentRuns  sync.Mutex      // one agent subprocess per host at a time
	approvals  sync.Map        // confirm-mode dispatch gates: id -> chan bool
	harness    *harness.Client // fusion-harness sidecar (chat guards)
	autoToken  string          // one-time auto-login token for --open; empty = disabled
	ragInstall  ragInstallState // background fusion-rag install job (zero value = idle)
	codeInstall ragInstallState // background fusion-code install job (same job-state shape)
	runs        workerRuns      // live worker dispatch registry (busy/idle + current task)
	team        teamDialer      // agent-studio team observer (poller + snapshot cache)
	mux        *http.ServeMux
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
	s.startTeamPoller()
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
	m.HandleFunc("GET /api/gateway/backends", s.auth.Require(s.handleGatewayBackends))
	m.HandleFunc("POST /api/gateway/backend", s.auth.Require(s.handleGatewayBackendAdd))
	m.HandleFunc("DELETE /api/gateway/backend/{name}", s.auth.Require(s.handleGatewayBackendDelete))
	m.HandleFunc("GET /api/gateway/keys", s.auth.Require(s.handleGatewayKeys))
	m.HandleFunc("DELETE /api/gateway/keys/{name}", s.auth.Require(s.handleGatewayKeyDelete))
	m.HandleFunc("GET /api/gateway/models", s.auth.Require(s.handleGatewayModels))
	m.HandleFunc("GET /api/skills", s.auth.Require(s.handleSkillsList))
	m.HandleFunc("GET /api/skills/read", s.auth.Require(s.handleSkillsRead))
	m.HandleFunc("POST /api/skills", s.auth.Require(s.handleSkillsSave))
	m.HandleFunc("DELETE /api/skills/{name}", s.auth.Require(s.handleSkillsDelete))
	m.HandleFunc("POST /api/skills/draft", s.auth.Require(s.handleSkillsDraft))
	m.HandleFunc("POST /api/skills/import", s.auth.Require(s.handleSkillsImport))

	m.HandleFunc("GET /api/conversations", s.auth.Require(s.handleListConversations))
	m.HandleFunc("POST /api/conversations", s.auth.Require(s.handleCreateConversation))
	m.HandleFunc("GET /api/conversations/{id}/messages", s.auth.Require(s.handleListMessages))
	m.HandleFunc("DELETE /api/conversations/{id}", s.auth.Require(s.handleDeleteConversation))
	m.HandleFunc("PATCH /api/conversations/{id}", s.auth.Require(s.handleRenameConversation))
	m.HandleFunc("POST /api/chat", s.auth.Require(s.handleChat))
	m.HandleFunc("POST /api/agent/run", s.auth.Require(s.handleAgentRun))
	m.HandleFunc("GET /api/agents", s.auth.Require(s.handleAgents))

	m.HandleFunc("GET /api/settings/providers", s.auth.Require(s.handleListProviders))
	m.HandleFunc("POST /api/settings/providers", s.auth.Require(s.handleSaveProvider))
	m.HandleFunc("DELETE /api/settings/providers/{id}", s.auth.Require(s.handleDeleteProvider))
	m.HandleFunc("POST /api/settings/apikey", s.auth.Require(s.handleStoreAPIKey))
	m.HandleFunc("GET /api/settings/models", s.auth.Require(s.handleModels))
	m.HandleFunc("POST /api/settings/testcall", s.auth.Require(s.handleTestCall))
	m.HandleFunc("POST /api/approvals/{id}", s.auth.Require(s.handleApproval))
	m.HandleFunc("GET /api/auth/hint", s.handleAuthHint)
	m.HandleFunc("POST /api/gateway/key", s.auth.Require(s.handleGatewayKeyProvision))
	m.HandleFunc("GET /api/costs", s.auth.Require(s.handleCosts))
	m.HandleFunc("GET /api/dashboard", s.auth.Require(s.handleDashboard))
	m.HandleFunc("GET /api/settings/orchestration", s.auth.Require(s.handleGetOrchestration))
	m.HandleFunc("POST /api/settings/orchestration", s.auth.Require(s.handleSaveOrchestration))

	// Optional fusion-rag knowledge base: menu status/actions plus a
	// path-preserving reverse proxy for its UI and REST API. Auth-gated like
	// every other console surface.
	m.HandleFunc("GET /api/rag/status", s.auth.Require(s.handleRagStatus))
	m.HandleFunc("POST /api/rag/install", s.auth.Require(s.handleRagInstall))
	m.HandleFunc("POST /api/rag/start", s.auth.Require(s.handleRagStart))
	m.HandleFunc("POST /api/rag/stop", s.auth.Require(s.handleRagStop))
	m.HandleFunc("POST /api/rag/enabled", s.auth.Require(s.handleRagEnabled))
	m.HandleFunc("GET /api/workers/status", s.auth.Require(s.handleWorkersStatus))
	// Method-qualified registrations: a bare "/shell/fusion-rag/" subtree
	// pattern conflicts with the "GET /" SPA fallback in Go's mux.
	ragProxyHandler := s.auth.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ragProxy().ServeHTTP(w, r)
	}))
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		m.Handle(method+" /shell/fusion-rag/", ragProxyHandler)
	}

	// Optional fusion-code Coding Agent menu: same shape as the RAG menu.
	m.HandleFunc("GET /api/code/status", s.auth.Require(s.handleCodeStatus))
	m.HandleFunc("POST /api/code/install", s.auth.Require(s.handleCodeInstall))
	m.HandleFunc("POST /api/code/start", s.auth.Require(s.handleCodeStart))
	m.HandleFunc("POST /api/code/stop", s.auth.Require(s.handleCodeStop))
	m.HandleFunc("POST /api/code/enabled", s.auth.Require(s.handleCodeEnabled))
	m.HandleFunc("POST /api/code/key", s.auth.Require(s.handleCodeKey))
	m.HandleFunc("GET /api/failures", s.auth.Require(s.handleFailureList))
	m.HandleFunc("POST /api/failures", s.auth.Require(s.handleFailureList))
	m.HandleFunc("GET /api/failures/export", s.auth.Require(s.handleFailureExport))
	m.HandleFunc("GET /api/team/view", s.auth.Require(s.handleTeamView))
	m.HandleFunc("POST /api/team/op", s.auth.Require(s.handleTeamOp))
	codeProxyHandler := s.auth.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.codeProxy().ServeHTTP(w, r)
	}))
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		m.Handle(method+" /shell/fusion-code/", codeProxyHandler)
	}

	m.HandleFunc("GET /", s.handleStatic)
}

// Handler returns the root handler. A panic-recovery wrapper turns handler
// panics into HTTP 500 responses — without it a panicking handler kills the
// connection and the browser only shows an opaque "Failed to fetch".
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("server: panic in %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error (see console log)"})
			}
		}()
		s.mux.ServeHTTP(w, r)
	})
}

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
