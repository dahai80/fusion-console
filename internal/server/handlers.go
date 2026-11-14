package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/harness"
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

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad conversation id"})
		return
	}
	if err := s.store.DeleteConversation(s.auth.User(r), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("server: conversation %d deleted by %s", id, s.auth.User(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRenameConversation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad conversation id"})
		return
	}
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title required"})
		return
	}
	if err := s.store.RenameConversation(s.auth.User(r), id, strings.TrimSpace(req.Title)); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
		Orchestrate    bool   `json:"orchestrate"` // chat as main brain: allow agent dispatch tools this turn
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
	apiKey, err := s.resolveProviderKey(r.Context(), prov)
	if err != nil {
		log.Printf("server: chat api key unavailable (provider %s, key id %s): %v", prov.ID, prov.APIKeyID, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "API key unavailable for provider " + prov.Name + " — open the Gateway page and click Create API key, then retry. (" + err.Error() + ")",
		})
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

	// fusion-harness guard: budget gate + compaction before every LLM call.
	// Sidecar missing = unguarded chat (logged at startup), never a failure.
	if s.cfg.Harness.Enabled {
		hMsgs := make([]harness.Message, len(chatMsgs))
		for i, m := range chatMsgs {
			hMsgs[i] = harness.Message{Role: m.Role, Content: m.Content}
		}
		proceed, reason, compacted, err := s.harness.PreLLM(req.ConversationID, s.cfg.Harness.BudgetTokens, prov.Model, hMsgs)
		if err != nil {
			log.Printf("server: harness pre_llm unavailable, proceeding unguarded: %v", err)
		} else if !proceed {
			log.Printf("server: harness denied llm call conv=%d: %s", req.ConversationID, reason)
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "conversation token budget exceeded: " + reason})
			return
		} else if len(compacted) > 0 {
			chatMsgs = make([]chat.Message, len(compacted))
			for i, m := range compacted {
				chatMsgs[i] = chat.Message{Role: m.Role, Content: m.Content}
			}
		}
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
	log.Printf("server: chat conv=%d provider=%s model=%s user=%s orchestrate=%v", req.ConversationID, prov.ID, prov.Model, user, req.Orchestrate)

	// Orchestrated turn: chat is the main brain and may dispatch agents via
	// native tool-calls (bounded loop, harness-guarded dispatch). Mode "off"
	// disables the brain loop entirely — plain chat only.
	if req.Orchestrate {
		if s.cfg.Orchestrator.Mode == "off" {
			log.Printf("server: orchestration mode=off, ignoring orchestrate flag conv=%d", req.ConversationID)
			send(map[string]any{"type": "tool", "tool": "orchestrate", "status": "denied", "detail": "orchestration is off in settings"})
		} else if tp, ok := provider.(chat.ToolProvider); ok {
			opts := providerOpts{convID: req.ConversationID, budget: s.cfg.Harness.BudgetTokens, model: prov.Model, user: user}
			full, total := s.runPlannerLoop(r.Context(), tp, &opts, apiKey, chatMsgs, send)
			if full != "" {
				meta, _ := json.Marshal(map[string]any{
					"provider": prov.ID, "model": prov.Model, "orchestrated": true,
					"tokens_in": total.TokensIn, "tokens_out": total.TokensOut,
				})
				_, _ = s.store.AppendMessage(req.ConversationID, "assistant", full, string(meta))
				if total.TokensIn > 0 || total.TokensOut > 0 {
					_ = s.store.LogCost(req.ConversationID, user, "chat:"+prov.ID, total.CostUSD, total.TokensIn, total.TokensOut)
				}
			}
			send(map[string]any{"type": "done"})
			return
		}
		log.Printf("server: provider %s does not support tool calls, falling back to plain chat", prov.ID)
		send(map[string]any{"type": "tool", "tool": "orchestrate", "status": "error", "detail": "provider does not support tool calls"})
	}

	// fusion-rag knowledge base: once installed and enabled, every plain
	// conversation gets the kb_search / kb_ask MCP tools (bounded loop).
	if s.ragEnabled() {
		if tp, ok := provider.(chat.ToolProvider); ok {
			log.Printf("server: rag tools active conv=%d", req.ConversationID)
			opts := providerOpts{convID: req.ConversationID, budget: s.cfg.Harness.BudgetTokens, model: prov.Model, user: user}
			full, total := s.runRAGChatLoop(r.Context(), tp, &opts, apiKey, chatMsgs, send)
			if full != "" {
				meta, _ := json.Marshal(map[string]any{
					"provider": prov.ID, "model": prov.Model, "rag": true,
					"tokens_in": total.TokensIn, "tokens_out": total.TokensOut,
				})
				_, _ = s.store.AppendMessage(req.ConversationID, "assistant", full, string(meta))
				if total.TokensIn > 0 || total.TokensOut > 0 {
					_ = s.store.LogCost(req.ConversationID, user, "chat:"+prov.ID, total.CostUSD, total.TokensIn, total.TokensOut)
				}
			}
			send(map[string]any{"type": "done"})
			return
		}
	}

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
	if s.cfg.Harness.Enabled {
		if err := s.harness.PostLLM(req.ConversationID, s.cfg.Harness.BudgetTokens, usage.TokensIn+usage.TokensOut); err != nil {
			log.Printf("server: harness post_llm unavailable: %v", err)
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
	// Accept a worker name (named workers are first-class @-mention targets)
	// or a bare agent type; resolve to the backing CLI and its worker config.
	cli, wk := s.resolveWorkerTarget(req.Agent)
	adapter, ok := s.agents[cli]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown agent " + req.Agent})
		return
	}
	if wk != nil && !wk.Enabled {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "worker " + req.Agent + " is disabled in settings"})
		return
	}
	settings := s.agentSettings(cli)
	if wk != nil && wk.PermissionMode != "" {
		settings.PermissionMode = wk.PermissionMode
	}
	workdir := req.Workdir
	if workdir == "" {
		// Dedicated workspace: named workers get their own directory.
		if wk != nil {
			workdir = s.cfg.ResolveWorkerDir(*wk)
		} else {
			workdir = s.cfg.ResolveWorkDir(cli)
		}
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

	pEnv, forcedModel := s.providerEnv(cli, wk)
	budget := settings.MaxBudgetUSD
	if wk != nil && wk.MaxBudgetUSD > 0 {
		budget = wk.MaxBudgetUSD
	}
	taskText := s.prefixWorkerTask(r.Context(), req.Agent, req.Task)
	opts := agent.RunOpts{
		Task: taskText, Workdir: workdir,
		Model:          forcedModel,
		PermissionMode: settings.PermissionMode,
		MaxBudgetUSD:   budget,
		Command:        settings.Command,
		Env:            pEnv,
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
		if s.workerMemoryMode() == "fusion-memory" {
			s.commitWorkerMemory(r.Context(), req.Agent, req.Task, final)
		}
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
		Worker         bool    `json:"is_worker"`
		AgentType      string  `json:"agent_type,omitempty"`
	}
	out := []agentInfo{}
	for name, ad := range s.agents {
		st := s.agentSettings(name)
		out = append(out, agentInfo{
			Name: name, Command: ad.Name(), Detected: ad.Detect(),
			PermissionMode: st.PermissionMode, MaxBudgetUSD: st.MaxBudgetUSD,
		})
	}
	// Named workers are dispatchable @-mention targets in their own right —
	// several can share one agent CLI.
	for _, wk := range s.cfg.Orchestrator.Workers {
		if wk.Name == "" || !wk.Enabled {
			continue
		}
		ad, ok := s.agents[wk.Agent]
		if !ok {
			continue
		}
		st := s.agentSettings(wk.Agent)
		if wk.PermissionMode != "" {
			st.PermissionMode = wk.PermissionMode
		}
		budget := st.MaxBudgetUSD
		if wk.MaxBudgetUSD > 0 {
			budget = wk.MaxBudgetUSD
		}
		out = append(out, agentInfo{
			Name: wk.Name, Command: ad.Name(), Detected: ad.Detect(),
			PermissionMode: st.PermissionMode, MaxBudgetUSD: budget,
			Worker: true, AgentType: wk.Agent,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

// providerKeyID resolves the keychain id for a provider. Gateway-type
// providers always authenticate with the console's own "gateway-key" entry —
// older builds and hand-edited configs left a raw key in api_key_id, which
// made every chat fail with "secret not found in keyring".
func providerKeyID(prov *config.Provider) string {
	if prov.Type == config.ProviderGateway {
		return "gateway-key"
	}
	return prov.APIKeyID
}

// resolveProviderKey returns the provider's API key, auto-provisioning a
// gateway key when the stored one is missing (fresh machine, copied config,
// wiped keychain). Non-gateway providers cannot self-heal — the key must be
// re-saved by the user, so the error says exactly that.
func (s *Server) resolveProviderKey(ctx context.Context, prov *config.Provider) (string, error) {
	keyID := providerKeyID(prov)
	raw, err := config.GetAPIKey(keyID)
	if err == nil {
		return raw, nil
	}
	if prov.Type != config.ProviderGateway {
		return "", fmt.Errorf("API key %q is not in the OS keychain of this machine — re-save the provider's API key on the Gateway page (%v)", keyID, err)
	}
	log.Printf("server: key %s missing, attempting gateway key auto-provision", keyID)
	if _, _, perr := s.provisionGatewayKey(ctx); perr != nil {
		return "", fmt.Errorf("gateway API key missing and auto-provision failed: %v (open the Gateway page and click Create API key; keychain error: %v)", perr, err)
	}
	return config.GetAPIKey(keyID)
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	// Never leak key material: APIKeyID is a keychain reference only.
	// Self-heal: gateway providers whose api_key_id was corrupted (raw key
	// pasted by an older UI or hand edit) are repaired to "gateway-key" and
	// the fix is persisted.
	healed := false
	for i := range s.cfg.Provider {
		p := &s.cfg.Provider[i]
		if p.Type == config.ProviderGateway && p.APIKeyID != "gateway-key" {
			log.Printf("server: repairing gateway provider %s api_key_id %q -> gateway-key", p.ID, p.APIKeyID)
			p.APIKeyID = "gateway-key"
			healed = true
		}
	}
	if healed {
		if err := s.cfg.Save(s.cfgDir); err != nil {
			log.Printf("server: gateway provider key heal save failed: %v", err)
		}
	}
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

// handleDeleteProvider removes one console provider entry (config.json).
// Used to clean up zombie direct providers left by the removed providers form.
func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kept := s.cfg.Provider[:0]
	found := false
	for _, p := range s.cfg.Provider {
		if p.ID == id {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider " + id})
		return
	}
	s.cfg.Provider = kept
	if err := s.cfg.Save(s.cfgDir); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	log.Printf("server: provider %s deleted", id)
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
	apiKey, err := config.GetAPIKey(providerKeyID(prov))
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

// firstGatewayProvider returns the first enabled gateway-type provider —
// used as the fallback target when a direct provider's keychain entry is
// missing (zombie entries left by the removed providers form).
func (s *Server) firstGatewayProvider() (*config.Provider, bool) {
	for i := range s.cfg.Provider {
		p := &s.cfg.Provider[i]
		if p.Type == config.ProviderGateway && p.Enabled {
			return p, true
		}
	}
	return nil, false
}

// runTestCall fires one minimal chat completion at the given provider and
// returns the JSON response body plus HTTP status.
func (s *Server) runTestCall(ctx context.Context, prov *config.Provider) (map[string]any, int) {
	provider, err := buildProvider(prov)
	if err != nil {
		return map[string]any{"error": err.Error()}, http.StatusInternalServerError
	}
	apiKey, err := s.resolveProviderKey(ctx, prov)
	if err != nil {
		return map[string]any{"error": "api key unavailable: " + err.Error()}, http.StatusBadRequest
	}
	if prov.Model == "" {
		return map[string]any{"error": "provider has no model set"}, http.StatusBadRequest
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	evCh, err := provider.ChatStream(callCtx, prov.Model, apiKey, []chat.Message{{Role: "user", Content: "Reply with the single word: pong"}})
	if err != nil {
		log.Printf("server: test call provider=%s dial failed: %v", prov.ID, err)
		return map[string]any{"ok": false, "error": err.Error()}, http.StatusOK
	}
	sample := ""
	var callErr string
	for ev := range evCh {
		if ev.Type == chat.EventDelta && ev.Text != "" {
			sample += ev.Text
			if len(sample) > 40 {
				break
			}
		}
		if ev.Type == chat.EventError {
			callErr = ev.Text
		}
		if ev.Type == chat.EventDone {
			break
		}
	}
	latency := time.Since(start).Milliseconds()
	if callErr != "" {
		log.Printf("server: test call provider=%s stream error after %dms: %s", prov.ID, latency, callErr)
		return map[string]any{"ok": false, "error": callErr, "latency_ms": latency}, http.StatusOK
	}
	if strings.TrimSpace(sample) == "" {
		log.Printf("server: test call provider=%s empty reply after %dms", prov.ID, latency)
		return map[string]any{"ok": false, "error": "model returned an empty reply", "latency_ms": latency}, http.StatusOK
	}
	log.Printf("server: test call provider=%s model=%s ok in %dms", prov.ID, prov.Model, latency)
	return map[string]any{"ok": true, "sample": sample, "latency_ms": latency}, http.StatusOK
}

// handleTestCall fires one minimal chat completion at the saved provider so
// onboarding can verify the endpoint + key + model actually work before the
// developer leaves the wizard. When the requested provider has no usable API
// key (stale keychain entry) the call retries once with the first enabled
// gateway provider — the gateway key self-heals, zombie direct providers do
// not block onboarding.
func (s *Server) handleTestCall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProviderID string `json:"provider_id"`
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
	resp, status := s.runTestCall(r.Context(), prov)
	if errText, _ := resp["error"].(string); status == http.StatusBadRequest && strings.HasPrefix(errText, "api key unavailable:") {
		if gw, found := s.firstGatewayProvider(); found && gw.ID != prov.ID {
			log.Printf("server: test call provider=%s key unavailable, falling back to gateway provider %s", prov.ID, gw.ID)
			fallback, fbStatus := s.runTestCall(r.Context(), gw)
			if fbStatus == http.StatusOK {
				fallback["note"] = fmt.Sprintf("provider %q has no usable API key; test call ran against gateway provider %q instead", prov.ID, gw.ID)
				writeJSON(w, http.StatusOK, fallback)
				return
			}
		}
	}
	writeJSON(w, status, resp)
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

// handleDashboard serves brain-vs-worker token/cost series for the home
// dashboard. range is today|week|month|all (default week).
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.UsageDashboard(s.auth.User(r), r.URL.Query().Get("range"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleWorkersStatus backs the dashboard Workers tab: every configured
// worker with its runtime-detected agent availability plus lifetime spend
// (source "agent:<name>" in the cost log).
func (s *Server) handleWorkersStatus(w http.ResponseWriter, r *http.Request) {
	user := s.auth.User(r)
	spend, err := s.store.WorkerSpend(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := []map[string]any{}
	for _, w := range s.cfg.Orchestrator.Workers {
		id := w.Name
		if id == "" {
			id = w.Agent // legacy entries are cost-tracked under the agent type
		}
		detected := false
		if ad, ok := s.agents[w.Agent]; ok {
			detected = ad.Detect()
		}
		row := map[string]any{
			"name": id, "agent": w.Agent, "enabled": w.Enabled, "detected": detected,
			"isolation": w.Isolation, "work_dir": s.cfg.ResolveWorkerDir(w),
			"repo": w.Repo, "provider_id": w.ProviderID, "max_budget_usd": w.MaxBudgetUSD,
			"calls": 0, "cost_usd": 0.0, "tokens_in": 0, "tokens_out": 0, "last_run": "",
		}
		if sp, ok := spend["agent:"+id]; ok {
			for k, v := range sp {
				row[k] = v
			}
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": out})
}

// handleAuthHint tells the login page whether to prefill the factory default
// credentials. Loopback-only: on any remote deployment the answer is a flat
// "no", so the endpoint leaks nothing about password state to the network.
func (s *Server) handleAuthHint(w http.ResponseWriter, r *http.Request) {
	hint := map[string]any{"user": "admin", "factory_default": false}
	if isLoopback(r.RemoteAddr) && len(s.cfg.Users) > 0 {
		for _, u := range s.cfg.Users {
			if u.Name != "admin" {
				continue
			}
			if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("fusion-admin-2026")) == nil {
				hint["factory_default"] = true
			}
		}
	}
	writeJSON(w, http.StatusOK, hint)
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleApproval resolves one confirm-mode dispatch gate. The planner loop
// blocks on the channel until this arrives, the 10-minute timeout fires, or
// the chat request is cancelled.
func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Approve bool `json:"approve"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	id := r.PathValue("id")
	v, ok := s.approvals.LoadAndDelete(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown or already-resolved approval " + id})
		return
	}
	ch := v.(chan bool)
	select {
	case ch <- req.Approve:
	default:
	}
	log.Printf("server: approval %s -> %v", id, req.Approve)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func settingsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// handleGetOrchestration returns the guided brain-and-workers setup.
func (s *Server) handleGetOrchestration(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"orchestrator": s.cfg.Orchestrator})
}

// handleSaveOrchestration persists the onboarding wizard's brain-and-workers
// decisions. Values are validated here so a prompt-injected client cannot
// store arbitrary modes or isolation paths.
func (s *Server) handleSaveOrchestration(w http.ResponseWriter, r *http.Request) {
	var orch config.OrchestratorConfig
	if err := json.NewDecoder(r.Body).Decode(&orch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	switch orch.Mode {
	case "off", "confirm", "auto":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be off, confirm or auto"})
		return
	}
	if orch.BrainAgent != "" {
		if _, ok := s.agents[orch.BrainAgent]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown brain agent " + orch.BrainAgent})
			return
		}
	}
	for i := range orch.Workers {
		wk := &orch.Workers[i]
		if _, ok := s.agents[wk.Agent]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown worker agent " + wk.Agent})
			return
		}
		switch wk.Isolation {
		case "", "worktree", "standalone":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "isolation must be worktree or standalone"})
			return
		}
		if wk.Isolation == "" {
			wk.Isolation = "worktree"
		}
	}
	orch.Configured = true
	s.cfg.Orchestrator = orch
	if err := s.cfg.Save(s.cfgDir); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	log.Printf("server: orchestration saved mode=%s brain=%s workers=%d", orch.Mode, orch.BrainAgent, len(orch.Workers))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
