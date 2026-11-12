package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
)

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	convs, err := s.store.ListConversations(s.auth.User(r))
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": convs})
}

func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string `json:"title"`
		Kind      string `json:"kind"`
		AgentName string `json:"agent_name"`
		Workdir   string `json:"workdir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Kind == "" {
		req.Kind = "chat"
	}
	if req.Kind == "agent" {
		if _, ok := s.agents[req.AgentName]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown agent " + req.AgentName})
			return
		}
		if req.Workdir == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent session requires workdir"})
			return
		}
	}
	id, err := s.store.CreateConversation(s.auth.User(r), req.Title, req.Kind, req.AgentName, req.Workdir)
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad conversation id"})
		return
	}
	msgs, err := s.store.ListMessages(id)
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (s *Server) providerByID(id string) (*config.Provider, bool) {
	for i := range s.cfg.Provider {
		if s.cfg.Provider[i].ID == id && s.cfg.Provider[i].Enabled {
			return &s.cfg.Provider[i], true
		}
	}
	return nil, false
}

func buildProvider(p *config.Provider) (chat.Provider, error) {
	switch p.Type {
	case config.ProviderGateway:
		return chat.NewGatewayProvider(p.BaseURL), nil
	case config.ProviderAnthropic:
		return &chat.AnthropicCompatProvider{BaseURL: p.BaseURL}, nil
	default:
		return &chat.OpenAICompatProvider{BaseURL: p.BaseURL}, nil
	}
}

// handleChat streams one chat completion over SSE and persists the turn.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID int64  `json:"conversation_id"`
		ProviderID     string `json:"provider_id"`
		Content        string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	prov, ok := s.providerByID(req.ProviderID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown or disabled provider " + req.ProviderID})
		return
	}
	provider, err := buildProvider(prov)
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	apiKey, err := config.GetAPIKey(prov.APIKeyID)
	if err != nil {
		serverError(w, err, http.StatusBadRequest)
		return
	}

	if _, err := s.store.AppendMessage(req.ConversationID, "user", req.Content, "{}"); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	msgs, err := s.store.ListMessages(req.ConversationID)
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	chatMsgs := make([]chat.Message, 0, len(msgs))
	for _, m := range msgs {
		chatMsgs = append(chatMsgs, chat.Message{Role: m.Role, Content: m.Content})
	}

	flusher := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func(ev map[string]any) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	user := s.auth.User(r)
	log.Printf("server: chat conv=%d provider=%s model=%s user=%s", req.ConversationID, prov.ID, prov.Model, user)

	evCh, err := provider.ChatStream(r.Context(), prov.Model, apiKey, chatMsgs)
	if err != nil {
		send(map[string]any{"type": "error", "text": err.Error()})
		return
	}
	full := ""
	var usage chat.Event
	for ev := range evCh {
		switch ev.Type {
		case chat.EventDelta:
			full += ev.Text
			send(map[string]any{"type": "delta", "text": ev.Text})
		case chat.EventUsage:
			usage = ev
			send(map[string]any{"type": "usage", "tokens_in": ev.TokensIn, "tokens_out": ev.TokensOut})
		case chat.EventError:
			send(map[string]any{"type": "error", "text": ev.Text})
		case chat.EventDone:
		}
		if ev.Type == chat.EventDone {
			break
		}
	}
	if full != "" {
		meta, _ := json.Marshal(map[string]any{
			"provider": prov.ID, "model": prov.Model,
			"tokens_in": usage.TokensIn, "tokens_out": usage.TokensOut,
		})
		_, _ = s.store.AppendMessage(req.ConversationID, "assistant", full, string(meta))
		if usage.TokensIn > 0 || usage.TokensOut > 0 {
			_ = s.store.LogCost(req.ConversationID, user, "chat:"+prov.ID, usage.CostUSD, usage.TokensIn, usage.TokensOut)
		}
	}
	send(map[string]any{"type": "done"})
}

// handleAgentRun streams one headless agent run over SSE.
func (s *Server) handleAgentRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID int64  `json:"conversation_id"`
		Agent          string `json:"agent"`
		Task           string `json:"task"`
		Workdir        string `json:"workdir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	adapter, ok := s.agents[req.Agent]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown agent " + req.Agent})
		return
	}
	settings := s.agentSettings(req.Agent)
	workdir := req.Workdir
	if workdir == "" {
		workdir = settingsDir()
	}
	if fi, err := os.Stat(workdir); err != nil || !fi.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workdir not accessible: " + workdir})
		return
	}

	if _, err := s.store.AppendMessage(req.ConversationID, "user", req.Task, "{}"); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}

	// One agent subprocess at a time — office host stays predictable.
	s.agentRuns.Lock()
	defer s.agentRuns.Unlock()

	flusher := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(ev map[string]any) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	user := s.auth.User(r)
	log.Printf("server: agent=%s conv=%d workdir=%s user=%s", req.Agent, req.ConversationID, workdir, user)

	opts := agent.RunOpts{
		Task: req.Task, Workdir: workdir,
		PermissionMode: settings.PermissionMode,
		MaxBudgetUSD:   settings.MaxBudgetUSD,
		Command:        settings.Command,
	}
	evCh, err := adapter.Run(r.Context(), opts)
	if err != nil {
		send(map[string]any{"type": "error", "text": err.Error()})
		return
	}
	full, result := "", ""
	var usage agent.Event
	for ev := range evCh {
		switch ev.Type {
		case agent.EventText:
			full += ev.Text
			if ev.Text != "" {
				send(map[string]any{"type": "text", "text": ev.Text})
			}
		case agent.EventTool:
			send(map[string]any{"type": "tool", "tool": ev.ToolName, "detail": ev.ToolDetail})
		case agent.EventResult:
			result = ev.Text
			send(map[string]any{"type": "result", "text": ev.Text})
		case agent.EventUsage:
			usage = ev
			send(map[string]any{"type": "usage", "cost_usd": ev.CostUSD, "tokens_in": ev.TokensIn, "tokens_out": ev.TokensOut})
		case agent.EventError:
			send(map[string]any{"type": "error", "text": ev.Text})
		case agent.EventDone:
		}
		if ev.Type == agent.EventDone {
			break
		}
	}
	final := result
	if final == "" {
		final = full
	}
	if final != "" {
		meta, _ := json.Marshal(map[string]any{
			"agent": req.Agent, "workdir": workdir, "session_id": usage.SessionID,
			"cost_usd": usage.CostUSD, "tokens_in": usage.TokensIn, "tokens_out": usage.TokensOut,
		})
		_, _ = s.store.AppendMessage(req.ConversationID, "assistant", final, string(meta))
		_ = s.store.LogCost(req.ConversationID, user, "agent:"+req.Agent, usage.CostUSD, usage.TokensIn, usage.TokensOut)
	}
	send(map[string]any{"type": "done"})
}

func (s *Server) agentSettings(name string) config.AgentSettings {
	for _, a := range s.cfg.Agent {
		if a.Name == name {
			return a
		}
	}
	return config.AgentSettings{Name: name, Enabled: true, MaxBudgetUSD: 5}
}

// handleAgents lists agents with install detection.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	type agentInfo struct {
		Name           string  `json:"name"`
		Command        string  `json:"command"`
		Detected       bool    `json:"detected"`
		PermissionMode string  `json:"permission_mode"`
		MaxBudgetUSD   float64 `json:"max_budget_usd"`
	}
	out := []agentInfo{}
	for name, ad := range s.agents {
		st := s.agentSettings(name)
		out = append(out, agentInfo{
			Name: name, Command: ad.Name(), Detected: ad.Detect(),
			PermissionMode: st.PermissionMode, MaxBudgetUSD: st.MaxBudgetUSD,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	// Never leak key material: APIKeyID is a keychain reference only.
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.cfg.Provider})
}

func (s *Server) handleSaveProvider(w http.ResponseWriter, r *http.Request) {
	var prov config.Provider
	if err := json.NewDecoder(r.Body).Decode(&prov); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if prov.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider id required"})
		return
	}
	replaced := false
	for i := range s.cfg.Provider {
		if s.cfg.Provider[i].ID == prov.ID {
			s.cfg.Provider[i] = prov
			replaced = true
		}
	}
	if !replaced {
		s.cfg.Provider = append(s.cfg.Provider, prov)
	}
	if err := s.cfg.Save(s.cfgDir); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	log.Printf("server: provider %s saved", prov.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleStoreAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID  string `json:"key_id"`
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.KeyID == "" || req.Secret == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key_id and secret required"})
		return
	}
	if err := config.StoreAPIKey(req.KeyID, req.Secret); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	providerID := r.URL.Query().Get("provider")
	prov, ok := s.providerByID(providerID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown provider " + providerID})
		return
	}
	apiKey, err := config.GetAPIKey(prov.APIKeyID)
	if err != nil {
		serverError(w, err, http.StatusBadRequest)
		return
	}
	models, err := s.models.Fetch(r.Context(), modelBaseURL(prov), apiKey)
	if err != nil {
		serverError(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func modelBaseURL(p *config.Provider) string {
	switch p.Type {
	case config.ProviderGateway:
		return "http://127.0.0.1:11432/v1"
	case config.ProviderAnthropic:
		base := strings.TrimRight(p.BaseURL, "/")
		if strings.HasSuffix(base, "/v1") {
			return base
		}
		return base + "/v1"
	default:
		return p.BaseURL
	}
}

func (s *Server) handleCosts(w http.ResponseWriter, r *http.Request) {
	report, err := s.store.CostReport(s.auth.User(r))
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": report})
}

func settingsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}
