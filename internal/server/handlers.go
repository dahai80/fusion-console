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
		cli, wk := s.resolveWorkerTarget(req.AgentName)
		if _, ok := s.agents[cli]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown agent " + req.AgentName})
			return
		}
		if wk != nil && !wk.Enabled {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "worker " + req.AgentName + " is disabled in settings"})
			return
		}
		if strings.TrimSpace(req.Workdir) == "" {
			if wk != nil {
				req.Workdir = config.ConfiguredWorkDir(wk.WorkDir)
			} else {
				req.Workdir = config.ConfiguredWorkDir(s.agentSettings(cli).WorkDir)
			}
		}
		if req.Workdir == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent session requires workdir"})
			return
		}
		// Looking up "." inside the directory also checks permission to enter it.
		if fi, err := os.Stat(req.Workdir + string(os.PathSeparator) + "."); err != nil || !fi.IsDir() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workdir not accessible: " + req.Workdir})
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

// handleSetConversationModel persists the per-conversation provider/model
// binding. Empty provider_id clears the binding so the conversation falls
// back to the default gateway provider.
func (s *Server) handleSetConversationModel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad conversation id"})
		return
	}
	var req struct {
		ProviderID string `json:"provider_id"`
		Model      string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.ProviderID = strings.TrimSpace(req.ProviderID)
	req.Model = strings.TrimSpace(req.Model)
	if req.ProviderID != "" {
		if _, ok := s.providerByID(req.ProviderID); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown or disabled provider " + req.ProviderID})
			return
		}
	}
	if err := s.store.SetConversationModel(s.auth.User(r), id, req.ProviderID, req.Model); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("server: conversation %d bound to provider=%q model=%q by %s", id, req.ProviderID, req.Model, s.auth.User(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
		Model          string `json:"model"` // per-conversation chat model (design v2)
		Content        string `json:"content"`
		Orchestrate    bool   `json:"orchestrate"` // chat as main brain: allow agent dispatch tools this turn
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	// Team group chat: a conversation bound to a team (kind "team") turns
	// each message into a task assignment — no LLM provider involved.
	if req.ConversationID > 0 {
		if conv, cerr := s.store.Conversation(s.auth.User(r), req.ConversationID); cerr == nil && conv.Kind == "team" {
			s.handleTeamChatTurn(w, r, conv, req.Content)
			return
		}
	}

	// Provider resolution: explicit request override > brain config
	// (Agents -> Workers -> Brain) > per-conversation binding > first enabled
	// gateway provider. The brain setting is the everyday default so chat
	// never has to pick a model per message; provider.Model itself is never
	// written here (single-owner rule: the Models page owns the provider
	// default).
	user0 := s.auth.User(r)
	var convProv, convModel string
	var fallbackNotice string
	var model string
	prov, ok := s.providerByID(req.ProviderID)
	if ok {
		model = prov.DefaultModel()
	} else if bp := strings.TrimSpace(s.cfg.Orchestrator.BrainProviderID); bp != "" {
		if p, pok := s.providerByID(bp); pok {
			prov = p
			model = strings.TrimSpace(s.cfg.Orchestrator.BrainModel)
			if model == "" {
				model = p.DefaultModel()
			}
		} else {
			log.Printf("server: chat brain provider %q unusable, falling back to gateway default", bp)
		}
	} else if conv, cerr := s.store.Conversation(user0, req.ConversationID); cerr == nil && conv.ProviderID != "" {
		log.Printf("server: chat conv=%d request provider %q unusable, trying conversation binding %s", req.ConversationID, req.ProviderID, conv.ProviderID)
		if p, pok := s.providerByID(conv.ProviderID); pok {
			prov = p
			convProv, convModel = conv.ProviderID, conv.Model
		} else {
			fallbackNotice = "conversation bound to provider " + conv.ProviderID + " which is missing or disabled — using the default gateway provider"
			log.Printf("server: chat conv=%d conversation provider binding %s unusable, falling back", req.ConversationID, conv.ProviderID)
		}
	}
	if prov == nil {
		if gp, gpok := s.firstGatewayProvider(); gpok {
			prov = gp
		}
	}
	if prov == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no usable provider — pass provider_id or enable a provider on the Models page"})
		return
	}
	// Managed-gateway fast path + local MLX fallback (P1): when the chat
	// provider rides fusion-gateway and its port stays closed even after the
	// managed auto-start, an enabled fusion-mlx takes this turn so chat
	// survives cloud outages. Fail-open: without MLX the original error path
	// is untouched.
	if prov.Type == config.ProviderGateway && !gatewayPortOpen(strings.TrimSuffix(prov.BaseURL, "/v1")) {
		if note := s.ensureChatGateway(prov); note != "" {
			fallbackNotice = strings.TrimSpace(fallbackNotice + " " + note)
			if s.mlxUsable() {
				prov = s.mlxProvider()
				fallbackNotice = strings.TrimSpace(fallbackNotice + " cloud gateway unreachable — this turn runs on local fusion-mlx")
				log.Printf("server: chat falling back to local fusion-mlx (model %s)", prov.Model)
			}
		}
	}
	provider, err := buildProvider(prov)
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	apiKey, err := s.resolveProviderKey(r.Context(), prov)
	if prov.ID == "fusion-mlx" {
		// Local engine: no keychain round-trip — fusion-mlx does not check
		// bearer material on the loopback surface.
		apiKey, err = "mlx-local", nil
	}
	if err != nil {
		log.Printf("server: chat api key unavailable (provider %s, key id %s): %v", prov.ID, prov.APIKeyID, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "API key unavailable for provider " + prov.Name + " — open the Models page and re-save its API key, then retry. (" + err.Error() + ")",
		})
		return
	}

	// Chat model resolution (design v2): request override > brain default
	// (already seeded above) > conversation's stored choice > global chat
	// default > provider default. The resolved choice is persisted on the
	// conversation so it carries across sessions.
	if m := strings.TrimSpace(req.Model); m != "" {
		model = m
	}
	if model == "" && convModel != "" && (convProv == "" || convProv == prov.ID) {
		model = strings.TrimSpace(convModel)
	}
	if model == "" && (s.cfg.Chat.DefaultProviderID == "" || s.cfg.Chat.DefaultProviderID == prov.ID) {
		model = strings.TrimSpace(s.cfg.Chat.DefaultModel)
	}
	if model == "" {
		model = prov.DefaultModel()
	}
	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "provider " + prov.Name + " has no model — pick one in the Models page"})
		return
	}
	if err := s.store.SetConversationModel(user0, req.ConversationID, prov.ID, model); err != nil {
		log.Printf("server: persist conversation model failed (non-fatal): %v", err)
	}

	if _, err := s.store.AppendMessage(req.ConversationID, "user", req.Content, "{}"); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	// A human message restarts the auto-continue chain: the brain may keep
	// working on its own after background runs finish, but only for a bounded
	// number of hops since the last human turn.
	s.autoMu.Lock()
	s.autoDepth[req.ConversationID] = 0
	s.autoMu.Unlock()
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
	var budgetSpent, budgetLimit = -1, 0
	if s.cfg.Harness.Enabled {
		hMsgs := make([]harness.Message, len(chatMsgs))
		for i, m := range chatMsgs {
			hMsgs[i] = harness.Message{Role: m.Role, Content: m.Content}
		}
		pre, err := s.harness.PreLLM(req.ConversationID, s.cfg.Harness.BudgetTokens, model, hMsgs)
		if err != nil {
			// Fail-closed (issue #110): a broken guard denies the call, it
			// never becomes unguarded chat.
			log.Printf("server: harness pre_llm unavailable, failing closed: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "harness guard unavailable: " + err.Error()})
			return
		}
		{
			budgetSpent, budgetLimit = pre.Spent, pre.Limit
			if !pre.Proceed {
				log.Printf("server: harness denied llm call conv=%d: %s", req.ConversationID, pre.Reason)
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "conversation token budget exceeded: " + pre.Reason})
				return
			} else if len(pre.Messages) > 0 {
				chatMsgs = make([]chat.Message, len(pre.Messages))
				for i, m := range pre.Messages {
					chatMsgs[i] = chat.Message{Role: m.Role, Content: m.Content}
				}
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
	// Budget meter seed (issue #66): the pre_llm gate's spent/limit snapshot.
	if budgetSpent >= 0 {
		send(map[string]any{"type": "budget", "spent": budgetSpent, "limit": budgetLimit})
	}
	if fallbackNotice != "" {
		send(map[string]any{"type": "notice", "text": fallbackNotice})
	}
	log.Printf("server: chat conv=%d provider=%s model=%s user=%s orchestrate=%v", req.ConversationID, prov.ID, model, user, req.Orchestrate)

	// Orchestrated turn: chat is the main brain and may dispatch agents via
	// native tool-calls (bounded loop, harness-guarded dispatch). Mode "off"
	// disables the brain loop entirely — plain chat only.
	if req.Orchestrate {
		if s.cfg.Orchestrator.Mode == "off" {
			log.Printf("server: orchestration mode=off, ignoring orchestrate flag conv=%d", req.ConversationID)
			send(map[string]any{"type": "tool", "tool": "orchestrate", "status": "denied", "detail": "orchestration is off in settings"})
		} else if s.cfg.Orchestrator.BrainMode == "agent" {
			// Agent-brain mode: claude code runs the turn with the console
			// dispatch tools over MCP instead of the chat planner loop.
			msg := ""
			for i := len(chatMsgs) - 1; i >= 0; i-- {
				if chatMsgs[i].Role == "user" {
					msg = chatMsgs[i].Content
					break
				}
			}
			s.runAgentBrainTurn(r.Context(), req.ConversationID, user, msg, send)
			send(map[string]any{"type": "done"})
			return
		} else if tp, ok := provider.(chat.ToolProvider); ok {
			opts := providerOpts{convID: req.ConversationID, budget: s.cfg.Harness.BudgetTokens, model: model, user: user}
			full, total := s.runPlannerLoop(r.Context(), tp, &opts, apiKey, chatMsgs, send)
			if full != "" {
				meta, _ := json.Marshal(map[string]any{
					"provider": prov.ID, "model": model, "orchestrated": true,
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
			opts := providerOpts{convID: req.ConversationID, budget: s.cfg.Harness.BudgetTokens, model: model, user: user}
			full, total := s.runRAGChatLoop(r.Context(), tp, &opts, apiKey, chatMsgs, send)
			if full != "" {
				meta, _ := json.Marshal(map[string]any{
					"provider": prov.ID, "model": model, "rag": true,
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

	evCh, err := provider.ChatStream(r.Context(), model, apiKey, chatMsgs)
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
			"provider": prov.ID, "model": model,
			"tokens_in": usage.TokensIn, "tokens_out": usage.TokensOut,
		})
		_, _ = s.store.AppendMessage(req.ConversationID, "assistant", full, string(meta))
		if usage.TokensIn > 0 || usage.TokensOut > 0 {
			_ = s.store.LogCost(req.ConversationID, user, "chat:"+prov.ID, usage.CostUSD, usage.TokensIn, usage.TokensOut)
		}
	}
	if s.cfg.Harness.Enabled {
		if spent, _, err := s.harness.PostLLM(req.ConversationID, s.cfg.Harness.BudgetTokens, usage.TokensIn+usage.TokensOut); err != nil {
			log.Printf("server: harness post_llm unavailable: %v", err)
		} else {
			send(map[string]any{"type": "budget", "spent": spent, "limit": s.cfg.Harness.BudgetTokens})
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
		// Background skips the streaming run: the dispatch is enqueued into
		// the background registry (worker slots, dispatch card, result
		// reported into the conversation) exactly like a brain run_agent.
		Background   bool   `json:"background"`
		RequiresRepo bool   `json:"requires_repo"`
		Repo         string `json:"repo"` // optional source for background worktree dispatch
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
	// Direct background dispatch: same pipeline as the brain's run_agent
	// (workdir/isolation/brief/caveman inside finishDispatch) without a brain
	// turn in front — the @-mention path for single-shot tasks.
	if req.Background {
		if strings.TrimSpace(req.Task) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty task"})
			return
		}
		user := s.auth.User(r)
		settings := s.agentSettings(cli)
		budget := settings.MaxBudgetUSD
		if wk != nil && wk.MaxBudgetUSD > 0 {
			budget = wk.MaxBudgetUSD
		}
		args := dispatchArgs{Agent: req.Agent, Task: req.Task, User: user, Repo: req.Repo, RequiresRepo: req.RequiresRepo}
		note, err := s.enqueueBackgroundDispatch(req.ConversationID, user, args, wk, cli, settings, budget)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": note})
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
	// Github-isolation workers run in the synced checkout, not the bare
	// workspace dir (same behavior as brain dispatch).
	if wk != nil && wk.Isolation == "github" && req.Workdir == "" {
		dir, warn, err := syncRepo(s.cfg.ResolveWorkerDir(*wk), "worker "+req.Agent, wk.Repo, wk.RepoBranch)
		if err != nil {
			if req.RequiresRepo {
				writeJSON(w, http.StatusConflict, repoBlocked(err.Error()))
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if warn != "" {
			log.Printf("server: manual run repo sync warning: %s", warn)
		}
		workdir = dir
	}
	if req.RequiresRepo {
		if err := requireGitWorkspace(r.Context(), workdir); err != nil {
			if r.Context().Err() == nil {
				writeJSON(w, http.StatusConflict, err)
			}
			return
		}
	} else if fi, err := os.Stat(workdir); err != nil || !fi.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workdir not accessible: " + workdir})
		return
	}

	if _, err := s.store.AppendMessage(req.ConversationID, "user", req.Task, "{}"); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}

	// No worker-slot acquire on the interactive path: background dispatches
	// hold slots for their whole run, and a manual agent run must not queue
	// behind them. The slots only bound background workers against each other.

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

	// Version fingerprint: a silent CLI upgrade is the most common drift
	// trigger — make the change visible before the run starts.
	if note := s.versionChangeNotice(cli, agent.CommandFor(settings.Name, settings.Command)); note != "" {
		send(map[string]any{"type": "notice", "text": note})
	}

	pEnv, forcedModel, providerNotice := s.providerEnv(cli, wk)
	if providerNotice != "" {
		send(map[string]any{"type": "notice", "text": providerNotice})
	}
	budget := settings.MaxBudgetUSD
	if wk != nil && wk.MaxBudgetUSD > 0 {
		budget = wk.MaxBudgetUSD
	}
	// Model resolution lives in providerEnv (per-agent override > provider
	// default); empty leaves the CLI's own default.
	taskText := s.prefixWorkerTask(r.Context(), req.Agent, req.Task, s.workerMemoryModeFor(wk))
	if req.RequiresRepo {
		taskText = workerResultContract + "\n" + taskText
	}
	if brief := s.workerWorkspaceBrief(req.Agent, wk, workdir); brief != "" {
		taskText = brief + "\n" + taskText
	}
	opts := agent.RunOpts{
		Timeout: s.dispatchTimeout(),
		Task:    taskText, Workdir: workdir,
		Model:           forcedModel,
		PermissionMode:  settings.PermissionMode,
		MaxBudgetUSD:    budget,
		MaxBudgetTokens: settings.MaxBudgetTokens,
		Command:         settings.Command,
		Env:             pEnv,
	}
	evCh, err := adapter.Run(r.Context(), opts)
	if err != nil {
		send(map[string]any{"type": "error", "text": err.Error()})
		return
	}
	// Live dashboard state: mark the worker busy and publish progress.
	s.runs.start(req.Agent, req.Task)
	defer s.runs.end(req.Agent)
	full, result := "", ""
	var runFailed bool
	var usage agent.Event
	for ev := range evCh {
		switch ev.Type {
		case agent.EventText:
			full += ev.Text
			if ev.Text != "" {
				send(map[string]any{"type": "text", "text": ev.Text})
			}
		case agent.EventTool:
			s.runs.progress(req.Agent, ev.ToolDetail)
			send(map[string]any{"type": "tool", "tool": ev.ToolName, "detail": ev.ToolDetail})
		case agent.EventResult:
			result = ev.Text
			send(map[string]any{"type": "result", "text": ev.Text})
		case agent.EventUsage:
			usage = ev
			send(map[string]any{"type": "usage", "cost_usd": ev.CostUSD, "tokens_in": ev.TokensIn, "tokens_out": ev.TokensOut,
				"tokens_cache_read": ev.TokensCacheRead, "tokens_cache_write": ev.TokensCacheWrite})
		case agent.EventNotice:
			send(map[string]any{"type": "notice", "text": ev.Text})
		case agent.EventError:
			runFailed = true
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
	var blocked *dispatchBlocked
	if !runFailed {
		blocked = blockedResult(final)
	}
	if blocked != nil {
		final = "**blocked:** " + blocked.Reason
		send(map[string]any{"type": "blocked", "text": final, "reason": blocked.Reason})
	}
	if final != "" {
		meta, _ := json.Marshal(map[string]any{
			"agent": req.Agent, "workdir": workdir, "session_id": usage.SessionID,
			"cost_usd": usage.CostUSD, "tokens_in": usage.TokensIn, "tokens_out": usage.TokensOut,
			"tokens_cache_read": usage.TokensCacheRead, "tokens_cache_write": usage.TokensCacheWrite,
		})
		_, _ = s.store.AppendMessage(req.ConversationID, "assistant", final, string(meta))
		_ = s.store.LogCost(req.ConversationID, user, "agent:"+req.Agent, usage.CostUSD, usage.TokensIn, usage.TokensOut)
		if blocked == nil && s.workerMemoryMode() == "fusion-memory" {
			s.commitWorkerMemory(r.Context(), req.Agent, req.Task, final)
		}
	}
	if blocked == nil {
		send(map[string]any{"type": "done"})
	}
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
		Name            string  `json:"name"`
		Command         string  `json:"command"`
		Detected        bool    `json:"detected"`
		PermissionMode  string  `json:"permission_mode"`
		MaxBudgetUSD    float64 `json:"max_budget_usd"`
		Model           string  `json:"model,omitempty"`
		Protocol        string  `json:"protocol,omitempty"`
		MaxBudgetTokens int     `json:"max_budget_tokens,omitempty"`
		ProviderID      string  `json:"provider_id,omitempty"`
		Worker          bool    `json:"is_worker"`
		AgentType       string  `json:"agent_type,omitempty"`
	}
	out := []agentInfo{}
	// Detect() probes a subprocess per call; dozens of workers share one
	// CLI, so cache per agent type for this request.
	detectCache := map[string]bool{}
	detectOnce := func(agentType string, ad agent.Adapter) bool {
		if v, ok := detectCache[agentType]; ok {
			return v
		}
		v := ad.Detect()
		detectCache[agentType] = v
		return v
	}
	for name, ad := range s.agents {
		st := s.agentSettings(name)
		out = append(out, agentInfo{
			Name: name, Command: ad.Name(), Detected: detectOnce(name, ad),
			PermissionMode: st.PermissionMode, MaxBudgetUSD: st.MaxBudgetUSD,
			Model: st.Model, Protocol: st.Protocol, MaxBudgetTokens: st.MaxBudgetTokens,
			ProviderID: st.ProviderID,
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
		model := wk.Model
		if model == "" {
			model = st.Model
		}
		providerID := wk.ProviderID
		if providerID == "" {
			providerID = st.ProviderID
		}
		out = append(out, agentInfo{
			Name: wk.Name, Command: ad.Name(), Detected: detectOnce(wk.Agent, ad),
			PermissionMode: st.PermissionMode, MaxBudgetUSD: budget,
			Model: model, Protocol: st.Protocol, MaxBudgetTokens: st.MaxBudgetTokens,
			ProviderID: providerID,
			Worker:     true, AgentType: wk.Agent,
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
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.cfg.Provider, "chat_default": s.cfg.Chat})
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
	// Design v2 §4.1A: validate + normalize before persisting. Previously only
	// non-empty ID was checked, which let bad base URLs and nonexistent
	// default models slip through silently.
	note, verr := config.ValidateProvider(&prov)
	if verr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": verr.Error()})
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
	s.invalidateModelCatalog()
	log.Printf("server: provider %s saved", prov.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": note})
}

// handleSetChatDefault stores the global default chat model (design v2:
// new conversations inherit it; the gateway brain card's old role).
func (s *Server) handleSetChatDefault(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProviderID string `json:"provider_id"`
		Model      string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.ProviderID != "" {
		found := false
		for i := range s.cfg.Provider {
			if s.cfg.Provider[i].ID == req.ProviderID && s.cfg.Provider[i].Enabled {
				found = true
				break
			}
		}
		if !found {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown or disabled provider " + req.ProviderID})
			return
		}
	}
	s.cfg.Chat = config.ChatConfig{DefaultProviderID: req.ProviderID, DefaultModel: req.Model}
	if err := s.cfg.Save(s.cfgDir); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	log.Printf("server: chat default set provider=%s model=%s", req.ProviderID, req.Model)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteProvider removes one console provider entry (config.json).
// Design v2 §4.1A: refuses when the source is still referenced (agents,
// workers, chat default, conversations) unless force=1, and cleans the
// keychain entry so key material does not outlive the source.
func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Consumer check: agents + workers + chat default + conversations.
	var consumers []string
	for _, a := range s.cfg.Agent {
		if a.ProviderID == id {
			consumers = append(consumers, "agent "+a.Name)
		}
	}
	for _, o := range s.cfg.Orchestrator.Workers {
		if o.ProviderID == id {
			consumers = append(consumers, "worker "+o.Name)
		}
	}
	if s.cfg.Chat.DefaultProviderID == id {
		consumers = append(consumers, "default chat model")
	}
	if ids, err := s.store.ConversationProviderUsers(id); err == nil && len(ids) > 0 {
		consumers = append(consumers, fmt.Sprintf("%d conversation(s)", len(ids)))
	}
	if len(consumers) > 0 && r.URL.Query().Get("force") != "1" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "provider " + id + " is still referenced by: " + strings.Join(consumers, ", "),
			"consumers": consumers,
		})
		return
	}
	kept := s.cfg.Provider[:0]
	found := false
	var deleted config.Provider
	for _, p := range s.cfg.Provider {
		if p.ID == id {
			found = true
			deleted = p
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider " + id})
		return
	}
	s.cfg.Provider = kept
	// Clear the chat default when it pointed at the deleted source.
	if s.cfg.Chat.DefaultProviderID == id {
		s.cfg.Chat = config.ChatConfig{}
	}
	if err := s.cfg.Save(s.cfgDir); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	// Keychain cleanup: the key must not outlive its source. Best-effort —
	// a missing entry is not an error. Gateway keys are shared console-wide
	// (providerKeyID returns "gateway-key") and are never deleted here.
	if deleted.Type != config.ProviderGateway {
		if err := config.DeleteAPIKey(providerKeyID(&deleted)); err != nil {
			log.Printf("server: provider %s keychain cleanup skipped: %v", id, err)
		}
	}
	log.Printf("server: provider %s deleted", id)
	s.invalidateModelCatalog()
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
	rangeID := r.URL.Query().Get("range")
	if rangeID == "" {
		rangeID = "week"
	}
	names := make([]string, 0, len(s.cfg.Orchestrator.Workers))
	for _, worker := range s.cfg.Orchestrator.Workers {
		name := worker.Name
		if name == "" {
			name = worker.Agent
		}
		names = append(names, name)
	}
	data, err := s.store.UsageDashboard(s.auth.User(r), rangeID, names...)
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
	// Detect() spawns a `--version` subprocess per call; with one CLI shared
	// by dozens of workers that dominated the request latency. Cache per
	// agent type for this request.
	detectedCache := map[string]bool{}
	for _, w := range s.cfg.Orchestrator.Workers {
		id := w.Name
		if id == "" {
			id = w.Agent // legacy entries are cost-tracked under the agent type
		}
		detected, cached := detectedCache[w.Agent]
		if !cached {
			if ad, ok := s.agents[w.Agent]; ok {
				detected = ad.Detect()
			}
			detectedCache[w.Agent] = detected
		}
		row := map[string]any{
			"name": id, "agent": w.Agent, "enabled": w.Enabled, "detected": detected,
			"isolation": w.Isolation, "work_dir": s.cfg.ResolveWorkerDir(w),
			"repo": w.Repo, "provider_id": w.ProviderID, "max_budget_usd": w.MaxBudgetUSD,
			"calls": 0, "cost_usd": 0.0, "tokens_in": 0, "tokens_out": 0, "last_run": "",
		}
		for k, v := range s.runs.snapshot(id) {
			row[k] = v
		}
		// Registry miss is not proof of idle: agent subprocesses survive a
		// console restart, so check for a live agent process sitting in the
		// worker's work dir and report busy when one is found.
		if busy, _ := row["busy"].(bool); !busy && w.Enabled {
			workDir := s.cfg.ResolveWorkerDir(w)
			orphans := discoverOrphans()
			if pid, ok := orphans[workDir]; ok {
				row["busy"] = true
				row["task"] = "in-flight task (registry lost on restart)"
				if d := fmtOrphanDetail(pid); d != "" {
					row["detail"] = d
				}
			}
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
	if bp := strings.TrimSpace(orch.BrainProviderID); bp != "" {
		if _, ok := s.providerByID(bp); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown or disabled brain provider " + bp})
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
		case "", "github", "worktree", "standalone":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "isolation must be github, worktree or standalone"})
			return
		}
		if wk.Isolation == "" {
			wk.Isolation = "worktree"
		}
		if wk.Isolation == "github" && strings.TrimSpace(wk.Repo) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "github isolation requires a repository URL"})
			return
		}
		switch wk.Memory {
		case "", "notes", "fusion-memory", "off":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "worker memory must be notes, fusion-memory or off"})
			return
		}
		if br := strings.TrimSpace(orch.BrainRepo); br != "" &&
			!strings.HasPrefix(br, "http://") && !strings.HasPrefix(br, "https://") &&
			!strings.HasPrefix(br, "git@") && !strings.HasPrefix(br, "ssh://") &&
			!strings.HasPrefix(br, "/") && !strings.HasPrefix(br, "~/") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "brain repo must be a GitHub URL or an absolute local path"})
			return
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
