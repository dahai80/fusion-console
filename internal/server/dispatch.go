package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/store"
)

// dispatchArgs is the parsed run_agent tool call payload.
type dispatchArgs struct {
	Agent      string `json:"agent"`
	Task       string `json:"task"`
	Path       string `json:"path"`
	Repo       string `json:"repo"`
	Stored     string `json:"stored"`
	Offset     int64  `json:"offset"`
	MaxBytes   int64  `json:"max_bytes"`
	Background bool   `json:"background"`
	// User is the console user the dispatch is attributed to. Cost rows
	// land under this label so the Dashboard Workers tab (filtered by the
	// logged-in user) sees the spend; empty falls back to the legacy
	// "mcp-brain" bucket.
	User string `json:"user,omitempty"`
}

// bgRun is one background dispatch: queued by the brain's run_agent call,
// executed by a goroutine (bounded by maxConcurrentAgentRuns worker slots),
// and reported back into the originating conversation when it finishes.
type bgRun struct {
	ID             int64     `json:"id"`
	DBID           int64     `json:"-"` // dispatch_runs row id (same value once persisted)
	Worker         string    `json:"worker"`
	Agent          string    `json:"agent"`
	Task           string    `json:"task"`
	ConversationID int64     `json:"conversation_id"`
	User           string    `json:"user"`
	Status         string    `json:"status"` // queued | running | done | error
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	ElapsedS       int       `json:"elapsed_s"`
	CostUSD        float64   `json:"cost_usd"`
	TokensIn       int       `json:"tokens_in"`
	TokensOut      int       `json:"tokens_out"`
	Detail         string    `json:"detail"`
	Result         string    `json:"result"`
	Err            string    `json:"error,omitempty"`
	Read           bool      `json:"read"`
}

// dispatchToken returns the local loopback secret used by the MCP sidecar
// process to hand background dispatches to the long-lived server process.
// The MCP process dies with the claude turn, so it must never execute
// background runs itself; it proxies them over HTTP with this token.
func dispatchToken(dir string) string {
	path := filepath.Join(dir, "dispatch-token")
	if data, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(data)) >= 32 {
		return strings.TrimSpace(string(data))
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Printf("dispatch: generate token: %v", err)
		return ""
	}
	tok := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(tok), 0o600); err != nil {
		log.Printf("dispatch: write token: %v", err)
		return ""
	}
	return tok
}

// enqueueViaServer proxies a background dispatch to the main server process
// (used only in MCP mode, where this process is ephemeral).
func (s *Server) enqueueViaServer(args dispatchArgs) (string, error) {
	if s.dispatchTok == "" {
		return "", fmt.Errorf("background dispatch unavailable: no dispatch token")
	}
	if strings.TrimSpace(args.User) == "" {
		// MCP sidecar has no auth context of its own; default keeps legacy
		// rows distinguishable from real users.
		args.User = "mcp-brain"
	}
	body, _ := json.Marshal(map[string]any{
		"agent": args.Agent, "task": args.Task, "repo": args.Repo,
		"background": true, "user": args.User,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/api/dispatches/enqueue", s.cfg.Server.Port), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Dispatch-Token", s.dispatchTok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch to console server: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dispatch rejected: %s", out.Error)
	}
	return out.Message, nil
}

// handleDispatchEnqueue accepts a background dispatch handed over by the MCP
// sidecar process (loopback + shared secret only). The payload is validated
// exactly like the run_agent tool call before execution.
func (s *Server) handleDispatchEnqueue(w http.ResponseWriter, r *http.Request) {
	if s.dispatchTok == "" || r.Header.Get("X-Dispatch-Token") != s.dispatchTok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid dispatch token"})
		return
	}
	var args dispatchArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if strings.TrimSpace(args.Task) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty task"})
		return
	}
	cli, wk := s.resolveWorkerTarget(args.Agent)
	if wk == nil && strings.TrimSpace(args.Repo) != "" {
		if byRepo := s.resolveWorkerByRepo(args.Repo); byRepo != nil {
			wk = byRepo
			cli = wk.Agent
		}
	}
	if _, ok := s.agents[cli]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown agent " + args.Agent})
		return
	}
	if wk != nil && !wk.Enabled {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "worker " + args.Agent + " is disabled in settings"})
		return
	}
	settings := s.agentSettings(cli)
	budget := settings.MaxBudgetUSD
	if wk != nil && wk.MaxBudgetUSD > 0 {
		budget = wk.MaxBudgetUSD
	}
	// Attribute the run to the requesting user when the brain passes it
	// through; cost rows under "mcp-brain" are invisible to the per-user
	// Workers tab spend query.
	user := strings.TrimSpace(args.User)
	if user == "" {
		user = "mcp-brain"
	}
	msg, err := s.enqueueBackgroundDispatch(0, user, args, wk, cli, settings, budget)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// dispatchRegistry tracks background dispatches. Runs are write-through
// persisted to SQLite: history and unread state survive console restarts,
// and runs still marked running at boot are restored with a note (their
// subprocess is reconciled separately by the orphan scan).
type dispatchRegistry struct {
	mu     sync.Mutex
	nextID int64
	runs   []*bgRun
}

func (r *dispatchRegistry) enqueue(run *bgRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run.ID > r.nextID {
		r.nextID = run.ID
	}
	r.runs = append(r.runs, run)
}

// loadPersisted pulls the persisted run history into the registry at boot.
// Runs still queued/running from a previous process get a restored note —
// they were interrupted mid-flight, and any surviving subprocess shows up
// via the worker orphan scan instead.
func (s *Server) loadPersistedRuns() {
	if s.store == nil {
		return
	}
	rows, err := s.store.ListDispatchRuns(500)
	if err != nil {
		log.Printf("dispatch: load persisted runs: %v", err)
		return
	}
	restored := 0
	for _, row := range rows {
		run := &bgRun{
			ID: row.ID, Worker: row.Worker, Agent: row.Agent, Task: row.Task,
			ConversationID: row.ConvID, User: row.User, Status: row.Status,
			CostUSD: row.CostUSD, TokensIn: row.TokensIn, TokensOut: row.TokensOut,
			Detail: row.Detail, Result: row.Result, Err: row.Error, Read: row.Read,
		}
		if t, err := time.Parse(time.RFC3339, row.StartedAt); err == nil {
			run.StartedAt = t
		}
		if t, err := time.Parse(time.RFC3339, row.FinishedAt); err == nil {
			run.FinishedAt = t
			run.ElapsedS = row.ElapsedS
		}
		if run.Status == "queued" || run.Status == "running" {
			run.Status = "error"
			run.Err = "interrupted by console restart"
			run.FinishedAt = time.Now()
			restored++
		}
		s.bg.enqueue(run)
	}
	if len(rows) > 0 {
		log.Printf("dispatch: restored %d persisted run(s) (%d interrupted in flight)", len(rows), restored)
	}
}

// persistRun writes one run's current state to the store (best effort —
// persistence failure never breaks the run itself).
func (s *Server) persistRun(run *bgRun) {
	if s.store == nil {
		return
	}
	row := &store.DispatchRunRow{
		ID: run.ID, ConvID: run.ConversationID, User: run.User,
		Worker: run.Worker, Agent: run.Agent, Task: run.Task, Status: run.Status,
		StartedAt:  run.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt: run.FinishedAt.UTC().Format(time.RFC3339),
		ElapsedS:   run.ElapsedS, CostUSD: run.CostUSD,
		TokensIn: run.TokensIn, TokensOut: run.TokensOut,
		Detail: run.Detail, Result: run.Result, Error: run.Err, Read: run.Read,
	}
	var err error
	if run.DBID == 0 {
		if row.ID, err = s.store.CreateDispatchRun(row); err == nil {
			run.DBID = row.ID
			run.ID = row.ID // the DB row id is the canonical run id
		}
	} else {
		row.ID = run.DBID
		err = s.store.SaveDispatchRun(row)
	}
	if err != nil {
		log.Printf("dispatch: persist run #%d: %v", run.ID, err)
	}
}

// list returns a snapshot copy; conversation filters by convID > 0.
func (r *dispatchRegistry) list(convID int64) []bgRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bgRun, 0, len(r.runs))
	for i := len(r.runs) - 1; i >= 0; i-- {
		run := *r.runs[i]
		if convID > 0 && run.ConversationID != convID {
			continue
		}
		if run.Status == "running" {
			run.ElapsedS = int(time.Since(run.StartedAt).Seconds())
		}
		out = append(out, run)
	}
	return out
}

func (r *dispatchRegistry) ack(convID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, run := range r.runs {
		if run.ConversationID == convID && run.Status == "done" {
			run.Read = true
		}
	}
}

// dismiss removes one finished run from the registry.
func (r *dispatchRegistry) dismiss(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, run := range r.runs {
		if run.ID == id {
			r.runs = append(r.runs[:i], r.runs[i+1:]...)
			return
		}
	}
}

// clearFinished removes every done/error run from the registry.
func (r *dispatchRegistry) clearFinished() {
	r.mu.Lock()
	defer r.mu.Unlock()
	keep := r.runs[:0]
	for _, run := range r.runs {
		if run.Status != "done" && run.Status != "error" {
			keep = append(keep, run)
		}
	}
	r.runs = keep
}

// unreadCounts returns done-but-unseen run counts per conversation.
func (r *dispatchRegistry) unreadCounts() map[int64]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[int64]int{}
	for _, run := range r.runs {
		if run.Status == "done" && !run.Read {
			out[run.ConversationID]++
		}
	}
	return out
}

// enqueueBackgroundDispatch validates nothing further (callers already did)
// and starts the run in a goroutine. Execution takes one of the bounded
// worker slots, so runs queue only when all slots are busy.
func (s *Server) enqueueBackgroundDispatch(convID int64, user string, args dispatchArgs, wk *config.WorkerConfig, cli string, settings config.AgentSettings, budget float64) (string, error) {
	// Display name: the resolved named worker, not the raw agent id the brain
	// passed (often the CLI type "claude" — cards and chat summaries must show
	// which worker ran, e.g. "fusion-web-shell").
	workerName := args.Agent
	if wk != nil && wk.Name != "" {
		workerName = wk.Name
	}
	run := &bgRun{
		Worker:         workerName,
		Agent:          cli,
		Task:           args.Task,
		ConversationID: convID,
		User:           user,
		Status:         "queued",
	}
	s.bg.enqueue(run)
	s.persistRun(run)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("orchestrator: background run #%d panicked: %v", run.ID, p)
				s.bg.mu.Lock()
				run.Status, run.Err, run.FinishedAt = "error", fmt.Sprintf("internal panic: %v", p), time.Now()
				s.bg.mu.Unlock()
				s.persistRun(run)
			}
		}()
		// Bounded concurrency: acquire one of the worker slots, logging when
		// the dispatch has to wait so queuing stays visible in the log.
		select {
		case s.agentSema <- struct{}{}:
		default:
			log.Printf("orchestrator: all %d worker slots busy — background run #%d queued", cap(s.agentSema), run.ID)
			s.agentSema <- struct{}{}
		}
		defer func() { <-s.agentSema }()
		s.bg.mu.Lock()
		run.Status, run.StartedAt = "running", time.Now()
		s.bg.mu.Unlock()
		s.persistRun(run)

		// The brain turn's context dies with its SSE stream; background runs
		// must outlive it, capped at the same 30 minutes as foreground runs.
		ctx, cancel := context.WithTimeout(context.Background(), s.dispatchTimeout())
		defer cancel()
		final, err := s.finishDispatch(ctx, convID, user, "run_agent", args, wk, cli, settings, budget, nil)

		s.bg.mu.Lock()
		run.FinishedAt = time.Now()
		run.ElapsedS = int(run.FinishedAt.Sub(run.StartedAt).Seconds())
		if err != nil {
			run.Status, run.Err = "error", err.Error()
		} else {
			run.Status = "done"
			run.Result = final
		}
		s.bg.mu.Unlock()
		s.persistRun(run)
		log.Printf("orchestrator: background run #%d worker=%s status=%s", run.ID, run.Worker, run.Status)

		// Report into the conversation so the chat history carries the
		// outcome even if the user is on another page. MCP-sidecar handoffs
		// carry no conversation id (convID 0) — they are dashboard-only.
		if run.ConversationID > 0 {
			summary := summarizeDispatch(run)
			if _, aerr := s.store.AppendMessage(run.ConversationID, "assistant", summary, fmt.Sprintf(`{"background_run":%d}`, run.ID)); aerr != nil {
				log.Printf("orchestrator: background run #%d: append result message: %v", run.ID, aerr)
			}
		}
		// Chain continuation: wake the brain so a multi-step plan keeps
		// moving without a human nudge between background runs. Detached —
		// the worker slot must free before the next turn starts.
		go s.maybeAutoContinue(run)
	}()
	return fmt.Sprintf("dispatched as background run #%d (worker %s). It runs after any current agent subprocess and reports back into this conversation when finished; progress is visible in the dispatch panel and via dispatch_status.", run.ID, workerName), nil
}

// dispatchTimeout returns the configured per-run cap for dispatched agent
// subprocesses (foreground and background). Default 3 hours — long coding
// tasks outlive the old 30-minute cap.
func (s *Server) dispatchTimeout() time.Duration {
	if m := s.cfg.Orchestrator.DispatchTimeoutMinutes; m > 0 {
		return time.Duration(m) * time.Minute
	}
	return 300 * time.Minute
}

// autoContinueMax caps consecutive brain self-continuations in one chain —
// background run finishes, brain wakes, dispatches the next run, repeat. A
// human message in the conversation resets the counter.
const autoContinueMax = 8

// maybeAutoContinue wakes the agent-brain after a background run reports into
// its conversation, so a multi-step plan keeps moving without a human nudge
// between steps (each brain turn ends at dispatch time; without this wake-up
// the next step waits for the user to send a message). Bounded by
// autoContinueMax per human turn; chat-brain mode is skipped because its
// planner loop lives inside the SSE chat handler.
func (s *Server) maybeAutoContinue(run *bgRun) {
	if run.ConversationID <= 0 || s.cfg.Orchestrator.Mode == "off" {
		return
	}
	if s.cfg.Orchestrator.BrainMode != "agent" {
		log.Printf("orchestrator: background run #%d: auto-continue skipped (chat brain mode)", run.ID)
		return
	}
	s.autoMu.Lock()
	s.autoDepth[run.ConversationID]++
	depth := s.autoDepth[run.ConversationID]
	s.autoMu.Unlock()
	if depth > autoContinueMax {
		log.Printf("orchestrator: background run #%d: auto-continue cap %d reached conv=%d — waiting for human", run.ID, autoContinueMax, run.ConversationID)
		return
	}
	// The agent-brain starts fresh every turn and cannot read the chat
	// history — the wake message must carry the finished run's outcome so
	// the brain can decide the next step without re-deriving context.
	result := run.Result
	if result == "" {
		result = run.Err
	}
	if len(result) > 1200 {
		result = result[:1200] + "…"
	}
	msg := fmt.Sprintf("[auto-continue] Background run #%d (worker %s) finished: status %s, %ds, $%.2f.\n\nTask was: %s\n\nResult:\n%s\n\nContinue the plan: dispatch the next step via run_agent (background=true) if steps remain, or post a short completion summary if the plan is done. Never repeat work this run already did.", run.ID, run.Worker, run.Status, run.ElapsedS, run.CostUSD, oneLine(run.Task, 300), result)
	log.Printf("orchestrator: auto-continue conv=%d depth=%d after run #%d (%s)", run.ConversationID, depth, run.ID, run.Status)
	ctx, cancel := context.WithTimeout(context.Background(), s.dispatchTimeout())
	defer cancel()
	// No SSE stream to write to — the brain's reply lands in the chat
	// history via runAgentBrainTurn's own persistence.
	noopSend := func(map[string]any) {}
	s.runAgentBrainTurn(ctx, run.ConversationID, run.User, msg, noopSend)
}

// summarizeDispatch renders the completion message persisted into the chat.
func summarizeDispatch(run *bgRun) string {
	var b strings.Builder
	switch run.Status {
	case "done":
		fmt.Fprintf(&b, "> ✅ background run #%d finished — **%s** · %ds · $%.4f\n\n", run.ID, run.Worker, run.ElapsedS, run.CostUSD)
	default:
		fmt.Fprintf(&b, "> ❌ background run #%d failed — **%s**\n\n", run.ID, run.Worker)
	}
	fmt.Fprintf(&b, "**task:** %s\n\n", oneLine(run.Task, 200))
	if run.Err != "" {
		fmt.Fprintf(&b, "**error:** %s\n", oneLine(run.Err, 500))
	}
	if run.Result != "" {
		fmt.Fprintf(&b, "**result:**\n\n%s\n", truncateRunes(run.Result, 2000))
	}
	return b.String()
}

func oneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return truncateRunes(s, n)
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// finishDispatch carries a validated dispatch from workdir resolution through
// subprocess execution to cost/memory bookkeeping. Shared by the foreground
// planner path and background runs; send may be nil for background runs.
// The named error drives worktree failure preservation (issue #45 F6c): a
// failed worktree run is kept in place with a .console-preserved marker, so
// the work is re-deliverable instead of lost.
func (s *Server) finishDispatch(ctx context.Context, convID int64, user string, callName string, args dispatchArgs, wk *config.WorkerConfig, cli string, settings config.AgentSettings, budget float64, send func(map[string]any)) (result string, err error) {
	workdir := s.cfg.ResolveWorkDir(cli)
	// Worktree failure preservation (issue #45 F6c): when the dispatch fails
	// and the workspace was a created worktree, keep it with a preserved
	// marker — never a silent loss; re-deliver via the failed run card.
	defer func() {
		if err != nil && wk != nil && isolationOf(wk) == "worktree" && strings.HasPrefix(workdir, s.agentsRoot()) {
			s.preserveFailedWorktree(workdir, err)
		}
	}()
	// Brain code source: keep the configured GitHub repo current before
	// every dispatch so the brain workspace mirrors upstream. Warnings
	// surface in the chat tool feed without failing the dispatch.
	if url := strings.TrimSpace(s.cfg.Orchestrator.BrainRepo); url != "" && isRepoURL(url) {
		_, warn, err := syncRepo(s.brainBaseDir(), "brain", url, s.cfg.Orchestrator.BrainRepoBranch)
		if err != nil {
			return "", fmt.Errorf("brain repo sync: %v", err)
		}
		if warn != "" && send != nil {
			send(map[string]any{"type": "tool", "tool": callName, "status": "progress",
				"detail": "brain repo sync warning: " + warn})
		}
	}
	if wk != nil {
		// Repo-binding guard: a worker with a bound repo only works on
		// that repo. Cross-repo dispatches are rejected so the brain
		// routes the task to the owning worker instead.
		if bound := strings.TrimSpace(wk.Repo); bound != "" && wk.Isolation != "standalone" {
			if asked := strings.TrimSpace(args.Repo); asked != "" && normalizeRepo(asked) != normalizeRepo(bound) {
				name := wk.Name
				if name == "" {
					name = args.Agent
				}
				return "", fmt.Errorf(
					"worker %s is bound to repo %s and cannot work on %s — dispatch the task to the worker that owns %s, or file an issue in that repo instead",
					name, bound, asked, asked)
			}
		}
		switch wk.Isolation {
		case "github":
			// Sync the configured repository into the work dir, then run
			// inside it. Warnings (dirty tree, local commits) surface in
			// the chat tool feed without failing the dispatch.
			dir, warn, err := syncRepo(s.cfg.ResolveWorkerDir(*wk), "worker "+args.Agent, wk.Repo, wk.RepoBranch)
			if err != nil {
				return "", err
			}
			workdir = dir
			if warn != "" && send != nil {
				send(map[string]any{"type": "tool", "tool": callName, "status": "progress",
					"detail": fmt.Sprintf("%s: repo sync warning: %s", args.Agent, warn)})
			}
		case "standalone":
			workdir = s.cfg.ResolveWorkerDir(*wk)
		default: // worktree (also the empty-isolation default)
			// Named workers carry their own default repo; the per-run
			// argument wins when the brain passes one explicitly.
			repo := args.Repo
			if strings.TrimSpace(repo) == "" {
				repo = wk.Repo
			}
			if strings.TrimSpace(repo) != "" {
				wt, err := s.createWorktree(repo, args.Agent)
				if err != nil {
					return "", err
				}
				workdir = wt
			}
		}
	}
	log.Printf("orchestrator: dispatch worker=%s agent=%s isolation=%s workdir=%s budget=%.2f user=%s",
		args.Agent, cli, isolationOf(wk), workdir, budget, user)
	pEnv, forcedModel, providerNotice := s.providerEnv(cli, wk)
	if providerNotice != "" && send != nil {
		send(map[string]any{"type": "tool", "tool": callName, "status": "progress",
			"detail": fmt.Sprintf("%s provider notice: %s", args.Agent, providerNotice)})
	}
	taskText := s.prefixWorkerTask(ctx, args.Agent, args.Task, s.workerMemoryModeFor(wk))
	if brief := s.workerWorkspaceBrief(args.Agent, wk, workdir); brief != "" {
		taskText = brief + "\n" + taskText
	}
	adapter, ok := s.agents[cli]
	if !ok {
		return "", fmt.Errorf("unknown agent %q", cli)
	}
	opts := agent.RunOpts{
		Task: taskText, Workdir: workdir, Timeout: s.dispatchTimeout(),
		Model:           forcedModel,
		PermissionMode:  settings.PermissionMode,
		MaxBudgetUSD:    budget,
		MaxBudgetTokens: settings.MaxBudgetTokens,
		Command:         settings.Command,
		Env:             pEnv,
	}
	// Caveman default (issue #30): claude-family CLIs get the activation
	// ruleset as an appended system prompt; other families ride the task.
	claudeFamily := agent.ProtocolFor(cli, settings.Protocol) == agent.ProtocolClaudeStreamJSON
	injectCaveman(s.cavemanPrompt(), claudeFamily, &opts.Task, &opts)
	if note := s.versionChangeNotice(args.Agent, agent.CommandFor(settings.Name, settings.Command)); note != "" && send != nil {
		send(map[string]any{"type": "tool", "tool": callName, "status": "progress", "detail": note})
	}
	evCh, err := adapter.Run(ctx, opts)
	if err != nil {
		s.tagFailure(convID, user, args.Agent, "fc3_tool", "failed to start: "+err.Error())
		return "", fmt.Errorf("agent %s failed to start: %v", args.Agent, err)
	}
	// Live dashboard state: mark the worker busy and publish progress.
	s.runs.start(args.Agent, args.Task)
	defer s.runs.end(args.Agent)
	full, result := "", ""
	var usage agent.Event
	// Live progress: forward the agent's own tool activity plus a periodic
	// heartbeat, so the chat shows the worker is actually running.
	start := time.Now()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	running := true
	for running {
		select {
		case ev, ok := <-evCh:
			if !ok {
				running = false
				break
			}
			switch ev.Type {
			case agent.EventText:
				full += ev.Text
			case agent.EventTool:
				s.runs.progress(args.Agent, ev.ToolDetail)
				if send != nil {
					send(map[string]any{"type": "tool", "tool": callName, "status": "progress",
						"detail": fmt.Sprintf("%s: %s", args.Agent, ev.ToolDetail)})
				}
			case agent.EventResult:
				result = ev.Text
			case agent.EventUsage:
				usage = ev
			case agent.EventNotice:
				// Non-fatal visibility signal (drift/version hints): pass
				// through to the chat stream without failing the dispatch.
				if send != nil {
					send(map[string]any{"type": "tool", "tool": callName, "status": "progress",
						"detail": fmt.Sprintf("%s notice: %s", args.Agent, ev.Text)})
				}
			case agent.EventError:
				s.tagFailure(convID, user, args.Agent, "fc3_tool", "agent error: "+ev.Text)
				return "", fmt.Errorf("agent %s error: %s", args.Agent, ev.Text)
			}
		case <-heartbeat.C:
			s.runs.progress(args.Agent, fmt.Sprintf("running (%ds elapsed)", int(time.Since(start).Seconds())))
			if send != nil {
				send(map[string]any{"type": "tool", "tool": callName, "status": "progress",
					"detail": fmt.Sprintf("%s still running (%ds elapsed)", args.Agent, int(time.Since(start).Seconds()))})
			}
		case <-ctx.Done():
			return "", fmt.Errorf("agent %s canceled: %v", args.Agent, ctx.Err())
		}
	}
	final := result
	if final == "" {
		final = full
	}
	// Cost rows stay separate: planner chat vs agent runs.
	_ = s.store.LogCost(convID, user, "agent:"+args.Agent, usage.CostUSD, usage.TokensIn, usage.TokensOut)
	log.Printf("orchestrator: agent=%s finished cost=%.4f tokens_in=%d tokens_out=%d", args.Agent, usage.CostUSD, usage.TokensIn, usage.TokensOut)
	if s.workerMemoryModeFor(wk) == "fusion-memory" && final != "" {
		s.commitWorkerMemory(ctx, args.Agent, args.Task, final)
	}
	if final == "" {
		return "agent returned no output", nil
	}
	return final, nil
}

// handleDispatches lists background dispatch runs, newest first. Optional
// ?conversation_id=<id> scopes the list to one conversation.
func (s *Server) handleDispatches(w http.ResponseWriter, r *http.Request) {
	convID := int64(0)
	if v := r.URL.Query().Get("conversation_id"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &convID); err != nil {
			convID = 0
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dispatches":     s.bg.list(convID),
		"unread_by_conv": s.bg.unreadCounts(),
	})
}

// handleDispatchAck marks every finished run of one conversation as seen
// (called when the user opens that conversation).
func (s *Server) handleDispatchAck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID int64 `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConversationID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	s.bg.ack(req.ConversationID)
	if err := s.store.AckDispatchRuns(req.ConversationID); err != nil {
		log.Printf("dispatch: persist ack conv=%d: %v", req.ConversationID, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDispatchDismiss removes one finished run (POST /api/dispatches/{id}/dismiss).
func (s *Server) handleDispatchDismiss(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid run id"})
		return
	}
	s.bg.dismiss(id)
	if err := s.store.DeleteDispatchRun(id); err != nil {
		log.Printf("dispatch: dismiss run #%d: %v", id, err)
	}
	log.Printf("dispatch: run #%d dismissed", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDispatchClear removes every finished run (POST /api/dispatches/clear).
func (s *Server) handleDispatchClear(w http.ResponseWriter, r *http.Request) {
	s.bg.clearFinished()
	if err := s.store.ClearFinishedDispatchRuns(); err != nil {
		log.Printf("dispatch: clear finished: %v", err)
	}
	log.Printf("dispatch: finished runs cleared")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
