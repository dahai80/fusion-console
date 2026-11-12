package agent

import (
	"context"
	"log"
)

// OpencodeAdapter runs `opencode run` (opencode CLI headless mode). Recent
// opencode versions emit JSONL part events; plain-text output falls back to
// text deltas in process.go.
type OpencodeAdapter struct{}

// Name implements Adapter.
func (OpencodeAdapter) Name() string { return "opencode" }

// Detect implements Adapter.
func (OpencodeAdapter) Detect() bool { return detect("opencode") }

// Run implements Adapter.
func (a OpencodeAdapter) Run(ctx context.Context, opts RunOpts) (<-chan Event, error) {
	command := orCommand(opts.Command, "opencode")
	args := []string{"run", opts.Task}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	_, stream, err := launch(ctx, opts, command, args, false, parseOpencodeJSON)
	return stream, err
}

// parseOpencodeJSON parses opencode JSONL events. Known shapes:
// {"type":"step_start"...}, {"type":"text","part":{...text...}},
// {"type":"tool_use","part":{...tool, state...}}, {"type":"step_finish","part":{tokens...}}.
// Unrecognized types are logged and skipped (fail visible via log).
func parseOpencodeJSON(raw map[string]any) []Event {
	typ, _ := raw["type"].(string)
	part, _ := raw["part"].(map[string]any)
	switch typ {
	case "text":
		return []Event{{Type: EventText, Text: str(part["text"])}}
	case "tool_use", "tool":
		tool := strOr(str(part["tool"]), "tool")
		state, _ := part["state"].(map[string]any)
		detail := ""
		if state != nil {
			if input, ok := state["input"].(map[string]any); ok {
				detail = truncateString(mustJSON(input), 200)
			}
		} else {
			detail = truncateString(str(part["tool"]), 200)
		}
		return []Event{{Type: EventTool, ToolName: tool, ToolDetail: detail}}
	case "step_finish":
		tokens, _ := part["tokens"].(map[string]any)
		return []Event{{
			Type:      EventUsage,
			TokensIn:  intNum(tokens["input"]),
			TokensOut: intNum(tokens["output"]),
			CostUSD:   num(part["cost"]),
		}}
	case "step_start":
		return nil
	default:
		log.Printf("agent: opencode: ignoring event type=%s", typ)
		return nil
	}
}
