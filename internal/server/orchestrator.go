package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/harness"
)

// providerOpts carries per-request planner context through the loop.
type providerOpts struct {
	convID int64
	budget int
	model  string
	user   string
}

// Orchestrator: chat is the main brain. When the user enables orchestration
// for a turn, the LLM gets native tool-calls (list_agents / run_agent /
// read_artifact) and may dispatch work to headless coding agents in their
// own dedicated workspaces. Every dispatch passes the fusion-harness
// tool-admission chain (deny-first, fail-closed when the sidecar is down).

const maxOrchestratorTurns = 8 // bounded planner loop

const orchestratorSystemPrompt = `You are the main brain of a local development console. You can delegate coding work to headless coding agents (claude, fusion-code, codex, opencode), each running in its own dedicated workspace directory.

Tools:
- list_agents: see which agents are installed.
- run_agent: give one agent a concrete, self-contained task. The agent works in its own workspace and returns a result. For tasks expected to take more than a couple of minutes (issue fixes, refactors, test suites) pass background=true — you get a run id immediately, the worker keeps running after your turn ends, and the outcome is posted into this conversation automatically. Reference a finished background run by its id when the user asks about progress.
- read_artifact: read a file the agent produced, relative to the agents workspace root.
- read_attachment: read a chat attachment segment by stored name (byte range with next_offset continuation). When the user attaches a document, read it through this tool segment by segment — never request the whole file at once.

Rules:
- Answer simple questions directly; delegate only real coding/execution work.
- When delegating, write the task in full detail: files to create, behavior, constraints.
- After an agent finishes, verify its result (read_artifact when useful) before reporting to the user.
- Never invent agent results; report only what run_agent actually returned.
- Reply in the user's language.`

// orchestratorPrompt composes the planner system prompt plus the live
// workspace facts: brain work dir, bound code source, and the configured
// worker fleet. Without this the brain asks the user for repo paths that are
// already configured.
func (s *Server) orchestratorPrompt() string {
	orch := s.cfg.Orchestrator
	var b strings.Builder
	b.WriteString(orchestratorSystemPrompt)
	b.WriteString("\n\nYour workspace configuration (already set up — never ask the user for these):\n")
	b.WriteString("- Brain workspace root (read_artifact sandbox and worktree parent): " + s.agentsRoot() + "\n")
	if repo := strings.TrimSpace(orch.BrainRepo); repo != "" {
		if isRepoURL(repo) {
			b.WriteString("- Brain code source: GitHub repo " + repo + ", synced before every dispatch into " + filepath.Join(s.brainBaseDir(), "repo") + "\n")
		} else {
			b.WriteString("- Brain code source: local repo mounted as the workspace at " + s.agentsRoot() + "\n")
		}
		if strings.TrimSpace(orch.BrainRepoBranch) != "" {
			b.WriteString("- Brain repo branch: " + strings.TrimSpace(orch.BrainRepoBranch) + "\n")
		}
	}
	if len(orch.Workers) > 0 {
		b.WriteString("- Configured workers (dispatch targets, address by exact name):\n")
		for _, w := range orch.Workers {
			if !w.Enabled {
				continue
			}
			src := w.Isolation
			if src == "" {
				src = "worktree"
			}
			detail := src
			if w.Repo != "" {
				detail += " @ " + w.Repo
			}
			b.WriteString("  * " + w.Name + " (" + w.Agent + ", " + detail + ")\n")
		}
	}
	b.WriteString("- Repo ownership: a worker only handles work inside its bound repo/work dir (shown above); the console rejects cross-repo dispatches.\n")
	b.WriteString("- If a task concerns a repository that no worker owns, do not force a dispatch — tell the user to file an issue in that repo instead.\n")
	b.WriteString("- Dispatch workflow: instruct each worker to fix on a topic branch, run the repo's tests, then merge to the default branch and push — never direct commits to the default branch. Verify the merge landed (read_artifact or the worker's report) before reporting success to the user.\n")
	b.WriteString("- Long dispatches: use run_agent with background=true for anything expected to run longer than ~2 minutes. Tell the user it is running in the background; the result is posted into this conversation automatically when done. Do not block the turn waiting on it.\n")
	b.WriteString("- Dispatch sizing: each run_agent brief covers ONE bounded unit of work — a single issue, or one self-contained change. Never bundle multiple issues or phases into one brief: background dispatches are hard-killed at 30 minutes and a multi-part brief loses all unfinished parts. Split multi-part work into sequential dispatches (one issue per dispatch), verify each result, then dispatch the next.\n")
	if g := workspaceGuidelines(s.agentsRoot(), 8*1024); g != "" {
		b.WriteString("\nWorkspace conventions (CLAUDE.md / AGENTS.md found in the brain workspace):\n")
		b.WriteString(g)
	}
	if ctx := s.brainContext(); ctx != "" {
		b.WriteString("\nWorking conventions and background knowledge (from the configured context files):\n")
		b.WriteString(ctx)
	}
	return b.String()
}

// brainContext loads the configured context files (working conventions, repo
// inventories, team rules) into one bounded text block for the planner
// prompt. Missing files are logged and skipped; the block is capped so a
// huge file cannot drown the conversation.
func (s *Server) brainContext() string {
	const (
		maxPerFile = 6 * 1024
		maxTotal   = 12 * 1024
	)
	var b strings.Builder
	for _, path := range s.cfg.Orchestrator.BrainContextFiles {
		path = expandBrainPath(path)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("orchestrator: brain context file %s unreadable (%v) — skipped", path, err)
			continue
		}
		if b.Len()+len(data) > maxTotal {
			data = data[:max(0, maxTotal-b.Len())]
		}
		if len(data) > maxPerFile {
			data = data[:maxPerFile]
		}
		b.WriteString("--- " + path + " ---\n")
		b.Write(data)
		b.WriteString("\n")
		if b.Len() >= maxTotal {
			log.Printf("orchestrator: brain context truncated at %d bytes", b.Len())
			break
		}
	}
	return b.String()
}

// expandBrainPath expands a leading ~/ in a context file path.
func expandBrainPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	return path
}

// orchestratorTools declares the planner tool set (OpenAI function format;
// the Anthropic provider converts to input_schema).
func orchestratorTools() []chat.ToolDecl {
	return []chat.ToolDecl{
		{
			Name:        "list_agents",
			Description: "List available headless coding agents and whether each is installed on this machine.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "run_agent",
			Description: "Run one headless coding agent on a concrete task. With worktree isolation the agent edits a fresh git worktree of the target repo; otherwise it works in its dedicated workspace. Returns the agent's final result text. For long tasks (issue fixes, multi-step refactors) pass background=true: the call returns a run id immediately and the outcome is reported back into this conversation when the run finishes.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"agent":{"type":"string","description":"worker name from list_agents (preferred) or a bare agent type like claude"},` +
				`"task":{"type":"string","description":"complete self-contained task description"},` +
				`"repo":{"type":"string","description":"absolute path of the target repository; required when the agent uses worktree isolation, must be in the configured whitelist"},` +
				`"background":{"type":"boolean","description":"true = run in background and report back later; use for tasks expected to take more than a couple of minutes"}` +
				`},"required":["agent","task"]}`),
		},
		{
			Name:        "read_artifact",
			Description: "Read a file produced by an agent, relative to the agents workspace root (e.g. \"claude/notes.md\"). Returns up to 64 KB of text.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"relative path inside the agents root"}` +
				`},"required":["path"]}`),
		},
		{
			Name:        "read_attachment",
			Description: "Read one byte-range segment of a chat attachment by its stored name. Returns the segment plus continuation metadata (total size, next_offset, eof) — follow next_offset until eof instead of requesting the whole file at once, so large documents never flood the context.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"stored":{"type":"string","description":"stored attachment name from the [attachment: ...] message reference"},` +
				`"offset":{"type":"integer","description":"byte offset to start reading from; 0 for the first segment"},` +
				`"max_bytes":{"type":"integer","description":"segment size in bytes, capped at 256 KB; default 64 KB"}` +
				`},"required":["stored"]}`),
		},
		{
			Name:        "create_team_member",
			Description: "Register a new member on the agent-studio team roster (creates team capacity for dispatch). Requires the agent-studio integration to be enabled.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"agent_id":{"type":"string","description":"unique member id, e.g. coder-1"},` +
				`"name":{"type":"string","description":"display name"},` +
				`"team":{"type":"string","description":"team name; omit for the configured default team"},` +
				`"capabilities":{"type":"array","items":{"type":"string"},"description":"what this member can do, e.g. [\"edit\",\"test\"]"}` +
				`},"required":["agent_id"]}`),
		},
		{
			Name:        "team_status",
			Description: "Get the current team snapshot from agent-studio: task counts by status, member roster, connection health. Read-only.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}
}

// agentsRoot is the sandbox root for read_artifact and the parent of
// worktree workspaces. When the brain is bound to a local repo that repo dir
// is the root; otherwise the configured brain work dir (created on demand),
// else the historical <config dir>/agents default.
func (s *Server) agentsRoot() string {
	if repo := strings.TrimSpace(s.cfg.Orchestrator.BrainRepo); repo != "" && !isRepoURL(repo) {
		if st, err := os.Stat(repo); err == nil && st.IsDir() {
			return repo
		}
		log.Printf("orchestrator: brain repo %s not found or not a directory — falling back to work dir", repo)
	}
	return s.brainBaseDir()
}

// brainBaseDir resolves (and creates) the brain work dir: the configured
// value with ~ / relative paths expanded, else <config dir>/agents.
func (s *Server) brainBaseDir() string {
	d := strings.TrimSpace(s.cfg.Orchestrator.BrainWorkDir)
	if d != "" {
		if d == "~" || strings.HasPrefix(d, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				d = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(d, "~"), "/"))
			}
		}
		if !filepath.IsAbs(d) {
			if abs, err := filepath.Abs(d); err == nil {
				d = abs
			}
		}
		if err := os.MkdirAll(d, 0o700); err != nil {
			log.Printf("orchestrator: brain work dir %s unusable (%v) — falling back to default root", d, err)
		} else {
			return d
		}
	}
	return filepath.Join(s.cfgDir, "agents")
}

// executeToolCall runs one admitted tool call. The returned string becomes
// the tool-role message for the next planner iteration. Long-running agent
// dispatches stream progress through send so the browser shows live status.
func (s *Server) executeToolCall(ctx context.Context, call chat.ToolCall, user string, convID int64, send func(map[string]any)) (string, error) {
	var args dispatchArgs
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return "", fmt.Errorf("tool %s: bad arguments: %v", call.Name, err)
	}
	switch call.Name {
	case "list_agents":
		names := make([]string, 0, len(s.agents)+len(s.cfg.Orchestrator.Workers))
		for name, ad := range s.agents {
			status := "installed"
			if !ad.Detect() {
				status = "not installed"
			}
			names = append(names, fmt.Sprintf("%s (%s)", name, status))
		}
		// Named workers are first-class dispatch targets: the brain should
		// address them by name so parallel workers of one CLI stay distinct.
		for _, w := range s.cfg.Orchestrator.Workers {
			if w.Name == "" {
				continue
			}
			status := "installed"
			if ad, ok := s.agents[w.Agent]; ok && !ad.Detect() {
				status = "not installed"
			}
			names = append(names, fmt.Sprintf("%s (worker, type %s, %s)", w.Name, w.Agent, status))
		}
		sort.Strings(names)
		return "available agents:\n" + strings.Join(names, "\n"), nil

	case "run_agent":
		cli, wk := s.resolveWorkerTarget(args.Agent)
		// The brain sometimes dispatches with a bare agent type plus a repo
		// path. Resolve that back to the owning worker so isolation, budget,
		// repo binding and Dashboard status all apply (deterministic, not
		// prompt-dependent).
		if wk == nil && strings.TrimSpace(args.Repo) != "" {
			if byRepo := s.resolveWorkerByRepo(args.Repo); byRepo != nil {
				wk = byRepo
				cli = wk.Agent
				log.Printf("orchestrator: dispatch by repo %s resolved to worker %s", args.Repo, wk.Name)
			}
		}
		if _, ok := s.agents[cli]; !ok {
			return "", fmt.Errorf("unknown agent %q", args.Agent)
		}
		if strings.TrimSpace(args.Task) == "" {
			return "", fmt.Errorf("empty task")
		}
		// Brief quality gate (issue #4): non-blocking pre-flight warnings on
		// the dispatch payload, surfaced in the chat tool feed.
		if warnings := checkBrief(args.Task); len(warnings) > 0 {
			parts := make([]string, 0, len(warnings))
			for _, w := range warnings {
				parts = append(parts, w.Item+": "+w.Fix)
			}
			detail := "brief quality warning for " + args.Agent + " — " + strings.Join(parts, "; ")
			log.Printf("orchestrator: %s", detail)
			s.tagFailure(convID, user, args.Agent, "fc1_brief", strings.Join(parts, "; "))
			if send != nil {
				send(map[string]any{"type": "tool", "tool": call.Name, "status": "progress", "detail": detail})
			}
		}
		// Fleet guardrails: only enabled workers, per-worker budget/isolation
		// from the wizard config, repo whitelist enforced for worktrees.
		if wk != nil && !wk.Enabled {
			return "", fmt.Errorf("worker %s is disabled in settings", args.Agent)
		}
		settings := s.agentSettings(cli)
		budget := settings.MaxBudgetUSD
		if wk != nil && wk.MaxBudgetUSD > 0 {
			budget = wk.MaxBudgetUSD
		}
		// Long tasks run in the background: the call returns immediately and
		// the outcome is reported back into the conversation when finished.
		// In MCP mode this process is ephemeral (it dies with the claude
		// turn), so the run is handed to the long-lived server instead.
		if args.Background {
			if s.mcpMode {
				return s.enqueueViaServer(args)
			}
			return s.enqueueBackgroundDispatch(convID, user, args, wk, cli, settings, budget)
		}
		return s.finishDispatch(ctx, convID, user, call.Name, args, wk, cli, settings, budget, send)

	case "read_artifact":
		root := s.agentsRoot()
		clean := filepath.Clean("/" + args.Path) // force-relative, no traversal
		abs := filepath.Join(root, clean)
		if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return "", fmt.Errorf("path escapes agents root")
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("read %s: %v", args.Path, err)
		}
		const maxArtifact = 64 * 1024
		if len(data) > maxArtifact {
			data = data[:maxArtifact]
			return string(data) + "\n...(truncated at 64 KB)", nil
		}
		return string(data), nil
	case "read_attachment":
		seg, err := s.readAttachmentSegment(args.Stored, args.Offset, args.MaxBytes)
		if err != nil {
			return "", err
		}
		header := fmt.Sprintf("[attachment %s: bytes %d-%d of %d]", seg.Stored, seg.Offset, seg.Offset+int64(seg.BytesRead), seg.Total)
		if seg.EOF {
			header += " (eof)"
		} else {
			header += fmt.Sprintf(" — continue with offset %d", seg.NextOffset)
		}
		if seg.Encoding == "base64" {
			header += " (binary segment, base64)"
		}
		return header + "\n" + seg.Content, nil
	case "create_team_member":
		if !s.cfg.AgentStudio.Enabled {
			return "", fmt.Errorf("team view (agent-studio integration) is not enabled in settings")
		}
		var margs struct {
			AgentID      string   `json:"agent_id"`
			Name         string   `json:"name"`
			Team         string   `json:"team"`
			Capabilities []string `json:"capabilities"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &margs); err != nil {
			return "", fmt.Errorf("tool %s: bad arguments: %v", call.Name, err)
		}
		if margs.AgentID == "" {
			margs.AgentID = args.Agent
		}
		if margs.AgentID == "" {
			return "", fmt.Errorf("agent_id required")
		}
		_, err := s.teamRegisterMember(margs.AgentID, margs.Name, margs.Team, "idle", margs.Capabilities)
		if err != nil {
			return "", fmt.Errorf("register member: %v", err)
		}
		log.Printf("orchestrator: create_team_member agent=%s team=%s user=%s", margs.AgentID, margs.Team, user)
		team := margs.Team
		if team == "" {
			team = s.cfg.AgentStudio.Team
		}
		return fmt.Sprintf("member %s registered on team %s", margs.AgentID, team), nil

	case "team_status":
		if !s.cfg.AgentStudio.Enabled {
			return "", fmt.Errorf("team view (agent-studio integration) is not enabled in settings")
		}
		snap := s.teamView()
		if !snap.Connected {
			return "team observer not connected: " + snap.Error, nil
		}
		byStatus := map[string]int{}
		for _, t := range snap.Tasks {
			st, _ := t["status"].(string)
			byStatus[st]++
		}
		names := make([]string, 0, len(snap.Roster))
		for _, m := range snap.Roster {
			if n, ok := m["name"].(string); ok && n != "" {
				names = append(names, n)
			}
		}
		return fmt.Sprintf("team %s: %d members (%s); tasks: %v; alerts: %d",
			s.cfg.AgentStudio.Team, len(snap.Roster), strings.Join(names, ", "), byStatus, len(snap.Alerts)), nil

	case "kb_search", "kb_ask":
		var kbArgs map[string]any
		if err := json.Unmarshal([]byte(call.Arguments), &kbArgs); err != nil {
			return "", fmt.Errorf("tool %s: bad arguments: %v", call.Name, err)
		}
		return s.ragToolCall(ctx, call.Name, kbArgs)
	}
	return "", fmt.Errorf("unknown tool %q", call.Name)
}

// runPlannerLoop drives the tool-calling loop for one orchestrated chat turn.
// send streams SSE events to the browser. Returns the final assistant text
// and cumulative usage.
func (s *Server) runPlannerLoop(ctx context.Context, tp chat.ToolProvider, prov *providerOpts, apiKey string, msgs []chat.Message, send func(map[string]any)) (string, chat.Event) {
	tools := orchestratorTools()
	if s.ragEnabled() {
		// Knowledge base tools ride along in orchestrated turns too; they are
		// read-only MCP calls executed against the local fusion-rag server.
		tools = append(tools, ragToolDecls()...)
	}
	msgs = append([]chat.Message{{Role: "system", Content: s.orchestratorPrompt()}}, msgs...)
	full := ""
	var total chat.Event
	for turn := 0; turn < maxOrchestratorTurns; turn++ {
		// Harness guard per planner iteration. The system prompt is compacted
		// together with history only on the wire — the loop always re-anchors
		// it at index 0, because the compactor may truncate old messages.
		if s.cfg.Harness.Enabled {
			hMsgs := toHarnessMessages(msgs)
			proceed, reason, compacted, err := s.harness.PreLLM(prov.convID, prov.budget, prov.model, hMsgs)
			if err != nil {
				log.Printf("orchestrator: harness pre_llm unavailable, proceeding unguarded: %v", err)
			} else if !proceed {
				log.Printf("orchestrator: budget denied planner call conv=%d: %s", prov.convID, reason)
				send(map[string]any{"type": "error", "text": "conversation token budget exceeded: " + reason})
				return full, total
			} else if len(compacted) > 0 {
				msgs = fromHarnessMessages(compacted)
				if len(msgs) == 0 || msgs[0].Role != "system" {
					msgs = append([]chat.Message{{Role: "system", Content: s.orchestratorPrompt()}}, msgs...)
				}
			}
		}

		evCh, err := tp.ChatStreamTools(ctx, prov.model, apiKey, msgs, tools)
		if err != nil {
			send(map[string]any{"type": "error", "text": err.Error()})
			return full, total
		}
		turnText := ""
		var toolCalls []chat.ToolCall
		iterIn, iterOut := 0, 0
		for ev := range evCh {
			switch ev.Type {
			case chat.EventDelta:
				turnText += ev.Text
				send(map[string]any{"type": "delta", "text": ev.Text})
			case chat.EventUsage:
				// Gateways may emit usage more than once per stream (partial
				// on the finish chunk, full on a trailing usage-only chunk).
				// Values only grow to the true total — keep the max.
				if ev.TokensIn > iterIn {
					iterIn = ev.TokensIn
				}
				if ev.TokensOut > iterOut {
					iterOut = ev.TokensOut
				}
			case chat.EventToolCall:
				toolCalls = ev.ToolCalls
			case chat.EventError:
				send(map[string]any{"type": "error", "text": ev.Text})
			case chat.EventDone:
			}
			if ev.Type == chat.EventDone {
				break
			}
		}
		total.TokensIn += iterIn
		total.TokensOut += iterOut
		if iterIn > 0 || iterOut > 0 {
			send(map[string]any{"type": "usage", "tokens_in": total.TokensIn, "tokens_out": total.TokensOut})
		}
		if s.cfg.Harness.Enabled {
			if err := s.harness.PostLLM(prov.convID, prov.budget, iterIn+iterOut); err != nil {
				log.Printf("orchestrator: harness post_llm unavailable: %v", err)
			}
		}
		full += turnText
		if len(toolCalls) == 0 {
			return full, total // plain answer, loop ends
		}

		// Execute each call behind the harness admission chain, then feed
		// results back as tool-role messages.
		msgs = append(msgs, chat.Message{Role: "assistant", Content: turnText, ToolCalls: toolCalls})
		for _, call := range toolCalls {
			user := prov.user
			var callArgs map[string]any
			_ = json.Unmarshal([]byte(call.Arguments), &callArgs)
			if callArgs == nil {
				callArgs = map[string]any{}
			}
			allowed, reason, err := s.harness.ToolAdmit(prov.convID, prov.budget, call.Name, callArgs)
			if err != nil {
				log.Printf("orchestrator: tool_admit unavailable for %s, failing closed: %v", call.Name, err)
				allowed, reason = false, "tool admission unavailable (fail-closed)"
			} else if allowed {
				// Sidecar allowlist is a superset; verify against the real
				// planner tool set here so only declared tools execute.
				allowed = false
				for _, t := range tools {
					if t.Name == call.Name {
						allowed = true
						break
					}
				}
				if !allowed {
					reason = "tool not in planner tool set"
				}
			}
			if !allowed {
				log.Printf("orchestrator: tool %s denied: %s", call.Name, reason)
				send(map[string]any{"type": "tool", "tool": call.Name, "status": "denied", "detail": reason})
				msgs = append(msgs, chat.Message{Role: "tool", ToolCallID: call.ID, Content: "tool call denied: " + reason})
				continue
			}
			// Confirm mode: hold every agent dispatch until the user approves
			// it in the browser. Timeout = deny (fail-closed).
			if call.Name == "run_agent" && s.cfg.Orchestrator.Mode == "confirm" {
				ok, waitReason := s.awaitApproval(ctx, prov, call, send)
				if !ok {
					log.Printf("orchestrator: dispatch not approved: %s", waitReason)
					send(map[string]any{"type": "tool", "tool": call.Name, "status": "denied", "detail": waitReason})
					msgs = append(msgs, chat.Message{Role: "tool", ToolCallID: call.ID, Content: "dispatch not approved: " + waitReason})
					continue
				}
			}
			send(map[string]any{"type": "tool", "tool": call.Name, "status": "running", "detail": toolDetail(call)})
			out, execErr := s.executeToolCall(ctx, call, user, prov.convID, send)
			status := "done"
			if execErr != nil {
				out = "error: " + execErr.Error()
				status = "error"
				log.Printf("orchestrator: tool %s failed: %v", call.Name, execErr)
			}
			send(map[string]any{"type": "tool", "tool": call.Name, "status": status, "detail": ""})
			msgs = append(msgs, chat.Message{Role: "tool", ToolCallID: call.ID, Content: out})
		}
	}
	send(map[string]any{"type": "error", "text": fmt.Sprintf("planner loop hit the %d-iteration limit", maxOrchestratorTurns)})
	return full, total
}

// approvalTimeout bounds how long a confirm-mode dispatch waits for the
// human decision before failing closed.
const approvalTimeout = 10 * time.Minute

// awaitApproval surfaces a run_agent dispatch in the browser and blocks until
// the user approves or denies it (or the timeout / request context ends).
func (s *Server) awaitApproval(ctx context.Context, prov *providerOpts, call chat.ToolCall, send func(map[string]any)) (bool, string) {
	id := fmt.Sprintf("appr-%d-%d", prov.convID, time.Now().UnixNano())
	ch := make(chan bool, 1)
	s.approvals.Store(id, ch)
	defer s.approvals.Delete(id)
	send(map[string]any{"type": "approval", "id": id, "tool": call.Name, "detail": toolDetail(call)})
	log.Printf("orchestrator: approval requested id=%s %s", id, toolDetail(call))
	select {
	case ok := <-ch:
		if ok {
			log.Printf("orchestrator: approval granted id=%s", id)
			return true, ""
		}
		return false, "denied by user"
	case <-time.After(approvalTimeout):
		return false, "approval timed out (fail-closed)"
	case <-ctx.Done():
		return false, "request cancelled"
	}
}

func toolDetail(call chat.ToolCall) string {
	var args struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
		Path  string `json:"path"`
		Repo  string `json:"repo"`
	}
	_ = json.Unmarshal([]byte(call.Arguments), &args)
	switch call.Name {
	case "run_agent":
		detail := args.Agent
		if args.Repo != "" {
			detail += " @ " + filepath.Base(args.Repo)
		}
		if len(args.Task) > 120 {
			return detail + ": " + args.Task[:120] + "..."
		}
		return detail + ": " + args.Task
	case "read_artifact":
		return args.Path
	}
	return call.Name
}

// workerConfig returns the wizard-configured worker entry for one agent, or
// nil when the agent has no worker entry. Legacy entries are addressed by
// agent type; named workers must match their Name exactly.
func (s *Server) workerConfig(name string) *config.WorkerConfig {
	for i := range s.cfg.Orchestrator.Workers {
		w := &s.cfg.Orchestrator.Workers[i]
		if w.Name != "" && w.Name == name {
			return w
		}
	}
	for i := range s.cfg.Orchestrator.Workers {
		w := &s.cfg.Orchestrator.Workers[i]
		if w.Name == "" && w.Agent == name {
			return w
		}
	}
	return nil
}

// resolveWorkerTarget maps a dispatch id (worker name or agent type) to the
// agent CLI that backs it plus its worker entry (nil = bare agent type with
// no worker config). Named workers win over agent-type matches so multiple
// workers can share one CLI.
func (s *Server) resolveWorkerTarget(id string) (cli string, wk *config.WorkerConfig) {
	if w := s.workerConfig(id); w != nil {
		return w.Agent, w
	}
	return id, nil
}

// resolveWorkerByRepo maps a repo path the brain passed to run_agent back to
// the worker that owns it: exact work_dir match first, then the worker whose
// name equals the repo directory's basename (workers are named after their
// fusion-* checkouts). nil when no enabled worker claims the path.
func (s *Server) resolveWorkerByRepo(repoPath string) *config.WorkerConfig {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return nil
	}
	base := strings.TrimSuffix(filepath.Base(repoPath), "/")
	for i := range s.cfg.Orchestrator.Workers {
		w := &s.cfg.Orchestrator.Workers[i]
		if !w.Enabled {
			continue
		}
		if wd := s.cfg.ResolveWorkerDir(*w); wd == repoPath {
			return w
		}
		if w.Name != "" && w.Name == base {
			return w
		}
	}
	return nil
}

// providerEnv resolves a worker's forced provider (agent settings override,
// else the wizard worker config) into subprocess env vars plus an optional
// model override. The third return is a user-facing notice describing any
// fallback to the CLI's own default (empty when the configured provider
// applies cleanly) — surfaced to the chat stream so a silently ignored model
// binding is visible to the user, not only in server logs.
// Anthropic-compatible providers speak the ANTHROPIC_* env
// family; OpenAI-compatible and gateway providers speak the OPENAI_* family.
// Empty result = the CLI uses its own default login.
func (s *Server) providerEnv(cli string, wk *config.WorkerConfig) (map[string]string, string, string) {
	pid := ""
	agentModel := ""
	for _, a := range s.cfg.Agent {
		if a.Name == cli {
			if a.ProviderID != "" {
				pid = a.ProviderID
			}
			agentModel = a.Model
		}
	}
	if pid == "" && wk != nil {
		pid = wk.ProviderID
	}
	if wk != nil && wk.Model != "" {
		// Worker-level override beats the agent-type default.
		agentModel = wk.Model
	}
	if pid == "" {
		// No forced provider, but a per-worker model can still apply on top
		// of the CLI's own login.
		if wk != nil && strings.TrimSpace(wk.Model) != "" {
			return nil, strings.TrimSpace(wk.Model), ""
		}
		return nil, "", ""
	}
	var prov *config.Provider
	for i := range s.cfg.Provider {
		if s.cfg.Provider[i].ID == pid {
			prov = &s.cfg.Provider[i]
		}
	}
	if prov == nil || !prov.Enabled {
		log.Printf("orchestrator: worker %s: provider %s not found or disabled, using CLI default", cli, pid)
		return nil, "", "configured provider " + pid + " not found or disabled for " + cli + " — falling back to the CLI default login/model"
	}
	key, err := config.GetAPIKey(providerKeyID(prov))
	if err != nil || key == "" {
		log.Printf("orchestrator: worker %s: provider %s has no stored key, using CLI default", cli, pid)
		return nil, "", "provider " + prov.Name + " has no stored API key for " + cli + " — falling back to the CLI default login/model"
	}
	// atlascode ignores OPENAI_* env when its settings.json resolves a
	// provider, so forcing goes through a settings.json merge instead
	// (user-ruled approach, see atlasconfig.go).
	settings := s.agentSettings(cli)
	cmdBase := filepath.Base(agent.CommandFor(settings.Name, settings.Command))
	if cmdBase == "atlascode" || cmdBase == "atlas" {
		if prov.Type != "openai_compat" && prov.Type != "gateway" {
			log.Printf("orchestrator: worker %s: atlascode needs an openai_compat provider, %s is %q", cli, pid, prov.Type)
			return nil, "", "provider " + prov.Name + " is " + string(prov.Type) + " but atlascode needs openai_compat — falling back to the CLI default login/model"
		}
		modelID := agentModel
		if modelID == "" {
			modelID = prov.DefaultModel()
		}
		ref, err := writeAtlasProvider(prov, modelID)
		if err != nil {
			log.Printf("orchestrator: worker %s: atlas settings write failed: %v", cli, err)
			return nil, "", "atlascode settings write failed (" + err.Error() + ") — falling back to the CLI default login/model"
		}
		log.Printf("orchestrator: worker %s forced to provider %s via atlas settings (%s)", cli, pid, ref)
		return map[string]string{"PROVIDER_ID": pid}, ref, ""
	}
	env := map[string]string{"PROVIDER_ID": pid}
	// Model precedence: per-agent override > provider default (pool head).
	// Anthropic keeps its historical contract: the model is forced via
	// ANTHROPIC_MODEL env only, so --model stays unset for claude.
	model := agentModel
	if model == "" {
		model = prov.DefaultModel()
	}
	switch prov.Type {
	case "anthropic_compat":
		env["ANTHROPIC_BASE_URL"] = prov.BaseURL
		env["ANTHROPIC_AUTH_TOKEN"] = key
		if model != "" {
			env["ANTHROPIC_MODEL"] = model
		}
		log.Printf("orchestrator: worker %s forced to provider %s (anthropic_compat) model=%q via env", cli, pid, model)
		return env, "", ""
	case "openai_compat", "gateway":
		env["OPENAI_BASE_URL"] = prov.BaseURL
		env["OPENAI_API_KEY"] = key
	default:
		log.Printf("orchestrator: worker %s: provider %s type %q not injectable, using CLI default", cli, pid, prov.Type)
		return nil, "", "provider " + prov.Name + " type " + string(prov.Type) + " is not injectable for " + cli + " — falling back to the CLI default login/model"
	}
	log.Printf("orchestrator: worker %s forced to provider %s (%s) model=%q", cli, pid, prov.Type, model)
	// Per-worker model override wins over the provider default.
	if wk != nil && strings.TrimSpace(wk.Model) != "" {
		model = strings.TrimSpace(wk.Model)
		if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
			env["ANTHROPIC_MODEL"] = model
		}
	}
	return env, model, ""
}

// normalizeRepo reduces a repo reference to a comparable identity token:
// trims whitespace, trailing slash and .git suffix, then keeps only the
// last path segment. A GitHub URL ("https://github.com/o/fusion-web-shell")
// and a local checkout path ("/Users/x/fusion/fusion-web-shell") both
// reduce to "fusion-web-shell", so a brain dispatch that points at the
// checkout directory matches the worker's bound repo URL. Different repos
// with the same basename still collide — acceptable at fusion scale where
// workers are named after their checkouts.
func normalizeRepo(repo string) string {
	r := strings.TrimSpace(repo)
	r = strings.TrimSuffix(r, "/")
	r = strings.TrimSuffix(r, ".git")
	if i := strings.LastIndexAny(r, "/\\"); i >= 0 {
		r = r[i+1:]
	}
	return strings.ToLower(r)
}

func isolationOf(wk *config.WorkerConfig) string {
	if wk == nil || wk.Isolation == "" {
		return "workspace"
	}
	return wk.Isolation
}

// workerWorkspaceBrief describes the worker's own workspace in one line so
// the agent never asks the user for its work dir or repository — it already
// runs in the right place with the repo synced.
func (s *Server) workerWorkspaceBrief(name string, wk *config.WorkerConfig, workdir string) string {
	if wk == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("[workspace] You are worker " + name + ", running in " + workdir)
	switch wk.Isolation {
	case "github":
		b.WriteString(" — a checkout of GitHub repo " + strings.TrimSpace(wk.Repo))
		if wk.RepoBranch != "" {
			b.WriteString(" (branch " + strings.TrimSpace(wk.RepoBranch) + ")")
		}
		b.WriteString(", already synced to the latest upstream commit before this run")
	case "worktree":
		if repo := strings.TrimSpace(wk.Repo); repo != "" {
			b.WriteString(" — a fresh worktree branched from local repo " + repo)
		}
	default: // standalone
		b.WriteString(" — a dedicated empty workspace with no repo binding")
	}
	b.WriteString(". The workspace is already set up; never ask the user for repo paths or clone locations.")
	b.WriteString("\n[workflow] Bug fixes and features follow the branch flow: create a topic branch (e.g. fix/<issue>), implement, run the repo's tests, merge into the default branch, push. Never commit directly to the default branch without a branch + test pass.")
	// The claude CLI auto-reads CLAUDE.md from the working dir; AGENTS.md is
	// injected explicitly so both convention files reach the worker.
	if g := workspaceGuidelines(workdir, 4*1024, "AGENTS.md"); g != "" {
		b.WriteString("\nWorkspace conventions (AGENTS.md):\n" + g)
	}
	return b.String()
}

// createWorktree adds a detached git worktree of repo under the agents root.
// The repo path must pass the configured whitelist (empty whitelist = any
// fusion repo under the workspace root). The worktree is left in place after
// the run so results stay inspectable via read_artifact.
func (s *Server) createWorktree(repo, agentName string) (string, error) {
	abs := repo
	if !filepath.IsAbs(abs) {
		return "", fmt.Errorf("repo path must be absolute: %s", repo)
	}
	if allowed := s.cfg.Orchestrator.Repos; len(allowed) > 0 {
		ok := false
		for _, a := range allowed {
			if a == abs || strings.HasPrefix(abs, a+string(filepath.Separator)) {
				ok = true
				break
			}
		}
		if !ok {
			return "", fmt.Errorf("repo %s is not in the configured whitelist", repo)
		}
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return "", fmt.Errorf("repo has no .git: %s", abs)
	}
	root := s.agentsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("agents root: %v", err)
	}
	wt := filepath.Join(root, fmt.Sprintf("%s-wt-%d", agentName, time.Now().Unix()))
	cmd := exec.Command("git", "-C", abs, "worktree", "add", "--detach", wt, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("worktree add failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("orchestrator: worktree created %s (from %s)", wt, abs)
	return wt, nil
}

// toHarnessMessages converts chat messages (dropping tool-call fields the
// sidecar contract does not carry).
func toHarnessMessages(msgs []chat.Message) []harness.Message {
	out := make([]harness.Message, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		if content == "" && len(m.ToolCalls) > 0 {
			content = "[tool call: " + m.ToolCalls[0].Name + "]"
		}
		out = append(out, harness.Message{Role: m.Role, Content: content})
	}
	return out
}

// fromHarnessMessages converts sidecar-compacted messages back to chat form.
// The harness wire carries only role+content — tool_call pairing is lost.
// A role-"tool" message without a preceding assistant tool_calls entry is
// rejected by OpenAI-compatible APIs, so any surviving tool message is
// demoted to a user message (its content still reaches the model).
func fromHarnessMessages(msgs []harness.Message) []chat.Message {
	out := make([]chat.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" {
			log.Printf("orchestrator: compaction stripped tool-call pairing — tool result demoted to user message")
			out = append(out, chat.Message{Role: "user", Content: "[tool result]\n" + m.Content})
			continue
		}
		out = append(out, chat.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

// runAgentBrainTurn executes one orchestrated turn with claude code as the
// brain: a claude CLI session in the brain work dir, wired to the console's
// dispatch tools over MCP. Its own agentic loop replaces the planner loop.
func (s *Server) runAgentBrainTurn(ctx context.Context, convID int64, user, msg string, send func(map[string]any)) {
	ad, ok := s.agents["claude"]
	if !ok {
		send(map[string]any{"type": "tool", "tool": "brain", "status": "error", "detail": "claude CLI not available"})
		return
	}
	settings := s.agentSettings("claude")
	mcpFile, err := s.ensureBrainMCPConfig()
	if err != nil {
		send(map[string]any{"type": "tool", "tool": "brain", "status": "error", "detail": "mcp config: " + err.Error()})
		return
	}
	allowed := ""
	for _, t := range orchestratorTools() {
		allowed += "mcp__fusion-console__" + t.Name + ","
	}
	workdir := s.agentsRoot()
	// The claude CLI auto-reads CLAUDE.md from its working dir; AGENTS.md and
	// the synced repo's own convention files are injected explicitly so both
	// conventions reach the agent-brain.
	pre := workspaceGuidelines(workdir, 6*1024, "AGENTS.md")
	if repoDir := filepath.Join(s.brainBaseDir(), "repo"); repoDir != workdir {
		pre += workspaceGuidelines(repoDir, 6*1024)
	}
	task := s.orchestratorPrompt() + "\n\nUser message:\n" + msg
	if pre != "" {
		task = "Workspace conventions:\n" + pre + "\n" + task
	}
	opts := agent.RunOpts{
		Task: task, Workdir: workdir,
		PermissionMode: settings.PermissionMode,
		MaxBudgetUSD:   settings.MaxBudgetUSD,
		Command:        settings.Command,
		ExtraArgs:      []string{"--mcp-config", mcpFile, "--allowedTools", strings.TrimSuffix(allowed, ",")},
	}
	log.Printf("orchestrator: agent-brain turn conv=%d workdir=%s user=%s", convID, workdir, user)
	s.agentRuns.Lock()
	defer s.agentRuns.Unlock()
	evCh, err := ad.Run(ctx, opts)
	if err != nil {
		send(map[string]any{"type": "tool", "tool": "brain", "status": "error", "detail": err.Error()})
		return
	}
	full := ""
	var usage agent.Event
	for ev := range evCh {
		switch ev.Type {
		case agent.EventText:
			full += ev.Text
			send(map[string]any{"type": "delta", "text": ev.Text})
		case agent.EventTool:
			send(map[string]any{"type": "tool", "tool": "brain", "status": "progress", "detail": ev.ToolDetail})
		case agent.EventResult:
			if ev.Text != "" {
				full = ev.Text
			}
		case agent.EventUsage:
			usage = ev
		case agent.EventError:
			send(map[string]any{"type": "tool", "tool": "brain", "status": "error", "detail": ev.Text})
		}
	}
	if full != "" {
		meta, _ := json.Marshal(map[string]any{
			"provider": "claude", "brain": "agent",
			"tokens_in": usage.TokensIn, "tokens_out": usage.TokensOut,
		})
		_, _ = s.store.AppendMessage(convID, "assistant", full, string(meta))
		_ = s.store.LogCost(convID, user, "brain:claude", usage.CostUSD, usage.TokensIn, usage.TokensOut)
	}
	log.Printf("orchestrator: agent-brain turn done conv=%d tokens_in=%d tokens_out=%d", convID, usage.TokensIn, usage.TokensOut)
}

// ensureBrainMCPConfig writes the MCP config file that points a claude CLI
// session back at this binary in "mcp" mode, exposing the dispatch tools.
func (s *Server) ensureBrainMCPConfig() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.cfgDir, "mcp.json")
	doc := map[string]any{
		"mcpServers": map[string]any{
			"fusion-console": map[string]any{"command": self, "args": []string{"mcp"}},
		},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// guidelineFiles are the convention files picked up from a workspace.
var guidelineFiles = []string{"CLAUDE.md", "AGENTS.md"}

// workspaceGuidelines reads the requested convention files found directly in
// dir, capped to budget bytes overall. Missing files are skipped silently —
// a workspace may have either, both, or neither.
func workspaceGuidelines(dir string, budget int, names ...string) string {
	if len(names) == 0 {
		names = guidelineFiles
	}
	var b strings.Builder
	for _, name := range names {
		if b.Len() >= budget {
			break
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if len(data) > budget-b.Len() {
			data = data[:budget-b.Len()]
		}
		b.WriteString("--- " + path + " ---\n")
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
}
