package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/chat"
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
- run_agent: give one agent a concrete, self-contained task. The agent works in its own workspace and returns a result.
- read_artifact: read a file the agent produced, relative to the agents workspace root.

Rules:
- Answer simple questions directly; delegate only real coding/execution work.
- When delegating, write the task in full detail: files to create, behavior, constraints.
- After an agent finishes, verify its result (read_artifact when useful) before reporting to the user.
- Never invent agent results; report only what run_agent actually returned.
- Reply in the user's language.`

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
			Description: "Run one headless coding agent on a concrete task in its dedicated workspace. Returns the agent's final result text.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"agent":{"type":"string","description":"agent name, e.g. claude or fusion-code"},` +
				`"task":{"type":"string","description":"complete self-contained task description"}` +
				`},"required":["agent","task"]}`),
		},
		{
			Name:        "read_artifact",
			Description: "Read a file produced by an agent, relative to the agents workspace root (e.g. \"claude/notes.md\"). Returns up to 64 KB of text.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"relative path inside the agents root"}` +
				`},"required":["path"]}`),
		},
	}
}

// agentsRoot is the sandbox root for read_artifact and the default parent of
// per-agent workspaces.
func (s *Server) agentsRoot() string {
	return filepath.Join(s.cfgDir, "agents")
}

// executeToolCall runs one admitted tool call. The returned string becomes
// the tool-role message for the next planner iteration.
func (s *Server) executeToolCall(ctx context.Context, call chat.ToolCall, user string, convID int64) (string, error) {
	var args struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return "", fmt.Errorf("tool %s: bad arguments: %v", call.Name, err)
	}
	switch call.Name {
	case "list_agents":
		names := make([]string, 0, len(s.agents))
		for name, ad := range s.agents {
			status := "installed"
			if !ad.Detect() {
				status = "not installed"
			}
			names = append(names, fmt.Sprintf("%s (%s)", name, status))
		}
		sort.Strings(names)
		return "available agents:\n" + strings.Join(names, "\n"), nil

	case "run_agent":
		adapter, ok := s.agents[args.Agent]
		if !ok {
			return "", fmt.Errorf("unknown agent %q", args.Agent)
		}
		if strings.TrimSpace(args.Task) == "" {
			return "", fmt.Errorf("empty task")
		}
		settings := s.agentSettings(args.Agent)
		workdir := s.cfg.ResolveWorkDir(args.Agent)
		log.Printf("orchestrator: dispatch agent=%s conv-workdir=%s user=%s", args.Agent, workdir, user)
		opts := agent.RunOpts{
			Task: args.Task, Workdir: workdir,
			PermissionMode: settings.PermissionMode,
			MaxBudgetUSD:   settings.MaxBudgetUSD,
			Command:        settings.Command,
		}
		// One agent subprocess at a time, same gate as manual agent runs.
		s.agentRuns.Lock()
		defer s.agentRuns.Unlock()
		evCh, err := adapter.Run(ctx, opts)
		if err != nil {
			return "", fmt.Errorf("agent %s failed to start: %v", args.Agent, err)
		}
		full, result := "", ""
		var usage agent.Event
		for ev := range evCh {
			switch ev.Type {
			case agent.EventText:
				full += ev.Text
			case agent.EventResult:
				result = ev.Text
			case agent.EventUsage:
				usage = ev
			case agent.EventError:
				return "", fmt.Errorf("agent %s error: %s", args.Agent, ev.Text)
			}
		}
		final := result
		if final == "" {
			final = full
		}
		// Cost rows stay separate: planner chat vs agent runs.
		_ = s.store.LogCost(convID, user, "agent:"+args.Agent, usage.CostUSD, usage.TokensIn, usage.TokensOut)
		log.Printf("orchestrator: agent=%s finished cost=%.4f tokens_in=%d tokens_out=%d", args.Agent, usage.CostUSD, usage.TokensIn, usage.TokensOut)
		if final == "" {
			return "agent returned no output", nil
		}
		return final, nil

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
	}
	return "", fmt.Errorf("unknown tool %q", call.Name)
}

// runPlannerLoop drives the tool-calling loop for one orchestrated chat turn.
// send streams SSE events to the browser. Returns the final assistant text
// and cumulative usage.
func (s *Server) runPlannerLoop(ctx context.Context, tp chat.ToolProvider, prov *providerOpts, apiKey string, msgs []chat.Message, send func(map[string]any)) (string, chat.Event) {
	tools := orchestratorTools()
	msgs = append([]chat.Message{{Role: "system", Content: orchestratorSystemPrompt}}, msgs...)
	full := ""
	var total chat.Event
	for turn := 0; turn < maxOrchestratorTurns; turn++ {
		// Harness guard per planner iteration.
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
			send(map[string]any{"type": "tool", "tool": call.Name, "status": "running", "detail": toolDetail(call)})
			out, execErr := s.executeToolCall(ctx, call, user, prov.convID)
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

func toolDetail(call chat.ToolCall) string {
	var args struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
		Path  string `json:"path"`
	}
	_ = json.Unmarshal([]byte(call.Arguments), &args)
	switch call.Name {
	case "run_agent":
		if len(args.Task) > 120 {
			return args.Agent + ": " + args.Task[:120] + "..."
		}
		return args.Agent + ": " + args.Task
	case "read_artifact":
		return args.Path
	}
	return call.Name
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
func fromHarnessMessages(msgs []harness.Message) []chat.Message {
	out := make([]chat.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, chat.Message{Role: m.Role, Content: m.Content})
	}
	return out
}
