package agent

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
)

// ClaudeCompatAdapter runs a claude-stream-json family CLI headless:
// `<cmd> -p --output-format stream-json --verbose`. claude, fusion-code and
// atlascode all speak this protocol (atlascode ≥0.1.120 carries usage on the
// result event; older versions only report usage per assistant message, which
// the run-state accumulator folds in as a fallback).
type ClaudeCompatAdapter struct {
	AgentName string // agent name from settings (claude, fusion-code, atlascode, ...)
	Command   string // resolved executable (CommandFor)
}

// Name implements Adapter.
func (a ClaudeCompatAdapter) Name() string { return a.AgentName }

// Detect implements Adapter.
func (a ClaudeCompatAdapter) Detect() bool { return detect(a.Command) }

// Run implements Adapter.
func (a ClaudeCompatAdapter) Run(ctx context.Context, opts RunOpts) (<-chan Event, error) {
	command := orCommand(opts.Command, a.Command)
	args := []string{
		"-p", opts.Task,
		"--output-format", "stream-json",
		"--verbose",
	}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	args = append(args, opts.ExtraArgs...)
	_, stream, err := launch(ctx, opts, command, args, false, parseClaudeJSON)
	return stream, err
}

// parseClaudeJSON parses claude-stream-json lines:
// system/init, assistant (content blocks + per-message usage), user
// (tool_result), result (final text + usage + optional total_cost_usd).
func parseClaudeJSON(raw map[string]any, st *parseState) []Event {
	typ, _ := raw["type"].(string)
	switch typ {
	case "system":
		if sub, _ := raw["subtype"].(string); sub == "init" {
			return []Event{{Type: EventText, Text: "", SessionID: str(raw["session_id"])}}
		}
		return nil
	case "assistant":
		msg, _ := raw["message"].(map[string]any)
		// Per-message usage reports (atlascode emits these; claude carries
		// them too). Accumulated for the fallback path when the result event
		// arrives without a usage object.
		if u, ok := msg["usage"].(map[string]any); ok {
			st.addUsage(intNum(u["input_tokens"]), intNum(u["output_tokens"]),
				intNum(u["cache_read_input_tokens"]), intNum(u["cache_creation_input_tokens"]))
		}
		blocks, _ := msg["content"].([]any)
		var events []Event
		for _, b := range blocks {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				events = append(events, Event{Type: EventText, Text: str(block["text"])})
			case "tool_use":
				events = append(events, Event{
					Type:       EventTool,
					ToolName:   str(block["name"]),
					ToolDetail: summarizeInput(block["input"]),
				})
			}
		}
		if sid := str(raw["session_id"]); sid != "" && len(events) > 0 {
			events[0].SessionID = sid
		}
		return events
	case "result":
		events := []Event{}
		if res := str(raw["result"]); res != "" {
			events = append(events, Event{Type: EventResult, Text: res})
		}
		usage, _ := raw["usage"].(map[string]any)
		tokensIn, tokensOut := intNum(usage["input_tokens"]), intNum(usage["output_tokens"])
		cacheRead, cacheWrite := intNum(usage["cache_read_input_tokens"]), intNum(usage["cache_creation_input_tokens"])
		// Fallback: CLIs whose result event lacks usage (atlascode <0.1.120)
		// get the assistant-message accumulation instead.
		if tokensIn == 0 && tokensOut == 0 {
			tokensIn, tokensOut = st.tokensIn, st.tokensOut
		}
		if cacheRead == 0 && cacheWrite == 0 {
			cacheRead, cacheWrite = st.cacheRead, st.cacheWrite
		}
		cost := num(raw["total_cost_usd"])
		events = append(events, Event{
			Type:             EventUsage,
			CostUSD:          cost,
			TokensIn:         tokensIn,
			TokensOut:        tokensOut,
			TokensCacheRead:  cacheRead,
			TokensCacheWrite: cacheWrite,
			SessionID:        str(raw["session_id"]),
		})
		if isErr, _ := raw["is_error"].(bool); isErr {
			events = append(events, Event{Type: EventError, Text: orDefaultStr(str(raw["result"]), "agent run reported is_error")})
		}
		return events
	default:
		log.Printf("agent: claude-family: ignoring event type=%s", typ)
		return nil
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := strconv.ParseFloat(n.String(), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

func intNum(v any) int {
	return int(num(v))
}

func orDefaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func summarizeInput(input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	// Deterministic primary-field preference across tool schemas.
	for _, k := range []string{"command", "file_path", "path", "pattern", "prompt", "query", "description"} {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return truncateString(s, 120)
			}
		}
	}
	data, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return truncateString(string(data), 120)
}

func orCommand(cmd, def string) string {
	if cmd != "" {
		return cmd
	}
	return def
}
