package agent

import (
	"context"
	"log"
)

// OpencodeCompatAdapter runs an opencode-jsonl family CLI headless:
// `<cmd> run <task>`.
type OpencodeCompatAdapter struct {
	AgentName string
	Command   string
}

// Name implements Adapter.
func (a OpencodeCompatAdapter) Name() string { return a.AgentName }

// Detect implements Adapter.
func (a OpencodeCompatAdapter) Detect() bool { return detect(a.Command) }

// Run implements Adapter.
func (a OpencodeCompatAdapter) Run(ctx context.Context, opts RunOpts) (<-chan Event, error) {
	command := orCommand(opts.Command, a.Command)
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
func parseOpencodeJSON(raw map[string]any, st *parseState) []Event {
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
		cost := num(part["cost"])
		in, out := intNum(tokens["input"]), intNum(tokens["output"])
		st.addUsage(in, out, 0, 0)
		return []Event{{
			Type:      EventUsage,
			TokensIn:  in,
			TokensOut: out,
			CostUSD:   cost,
		}}
	case "step_start":
		return nil
	default:
		log.Printf("agent: opencode: ignoring event type=%s", typ)
		return nil
	}
}
