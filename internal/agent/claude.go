package agent

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
)

// ClaudeAdapter runs `claude -p --output-format stream-json --verbose`
// (Claude Code headless). fusion-code reuses the same stream-json protocol.
type ClaudeAdapter struct{}

// Name implements Adapter.
func (ClaudeAdapter) Name() string { return "claude" }

// Detect implements Adapter.
func (ClaudeAdapter) Detect() bool { return detect("claude") }

// Run implements Adapter.
func (a ClaudeAdapter) Run(ctx context.Context, opts RunOpts) (<-chan Event, error) {
	command := orCommand(opts.Command, "claude")
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
	_, stream, err := launch(ctx, opts, command, args, false, parseClaudeJSON)
	return stream, err
}

// FusionCodeAdapter targets the fusion CLI binary. fusion-code emits the same
// stream-json protocol as Claude Code (documented in the fusion-console design).
type FusionCodeAdapter struct{}

// Name implements Adapter.
func (FusionCodeAdapter) Name() string { return "fusion-code" }

// Detect implements Adapter.
func (FusionCodeAdapter) Detect() bool { return detect("fusion") }

// Run implements Adapter.
func (a FusionCodeAdapter) Run(ctx context.Context, opts RunOpts) (<-chan Event, error) {
	command := orCommand(opts.Command, "fusion")
	args := []string{
		"-p", opts.Task,
		"--output-format", "stream-json",
		"--verbose",
	}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	_, stream, err := launch(ctx, opts, command, args, false, parseClaudeJSON)
	return stream, err
}

// parseClaudeJSON parses Claude Code stream-json lines:
// system/init, assistant (content blocks), user (tool_result), result.
func parseClaudeJSON(raw map[string]any) []Event {
	typ, _ := raw["type"].(string)
	switch typ {
	case "system":
		if sub, _ := raw["subtype"].(string); sub == "init" {
			return []Event{{Type: EventText, Text: "", SessionID: str(raw["session_id"])}}
		}
		return nil
	case "assistant":
		msg, _ := raw["message"].(map[string]any)
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
		cost := num(raw["total_cost_usd"])
		events = append(events, Event{
			Type:      EventUsage,
			CostUSD:   cost,
			TokensIn:  intNum(usage["input_tokens"]),
			TokensOut: intNum(usage["output_tokens"]),
			SessionID: str(raw["session_id"]),
		})
		if isErr, _ := raw["is_error"].(bool); isErr {
			events = append(events, Event{Type: EventError, Text: orDefaultStr(str(raw["result"]), "agent run reported is_error")})
		}
		return events
	default:
		log.Printf("agent: claude: ignoring event type=%s", typ)
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
