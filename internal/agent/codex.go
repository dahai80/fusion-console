package agent

import (
	"context"
	"log"
)

// CodexAdapter runs `codex exec --json` (OpenAI Codex CLI headless mode).
type CodexAdapter struct{}

// Name implements Adapter.
func (CodexAdapter) Name() string { return "codex" }

// Detect implements Adapter.
func (CodexAdapter) Detect() bool { return detect("codex") }

// Run implements Adapter.
func (a CodexAdapter) Run(ctx context.Context, opts RunOpts) (<-chan Event, error) {
	command := orCommand(opts.Command, "codex")
	args := []string{"exec", "--json", "--skip-git-repo-check", opts.Task}
	if opts.Model != "" {
		args = []string{"exec", "--json", "--skip-git-repo-check", "-m", opts.Model, opts.Task}
	}
	_, stream, err := launch(ctx, opts, command, args, false, parseCodexJSON)
	return stream, err
}

// parseCodexJSON parses codex exec --json JSONL events:
// thread.started, turn.started, item.started/updated/completed,
// turn.completed (usage), turn.failed, error.
func parseCodexJSON(raw map[string]any) []Event {
	typ, _ := raw["type"].(string)
	switch typ {
	case "thread.started":
		return []Event{{Type: EventText, SessionID: str(raw["thread_id"])}}
	case "item.started", "item.updated":
		return nil
	case "item.completed":
		item, _ := raw["item"].(map[string]any)
		itemType, _ := item["type"].(string)
		switch itemType {
		case "agent_message":
			return []Event{{Type: EventResult, Text: str(item["text"])}}
		case "reasoning":
			return []Event{{Type: EventText, Text: str(item["text"])}}
		case "command_execution":
			detail := str(item["command"])
			if agg, _ := item["aggregated_output"].(string); agg != "" {
				detail = detail + " :: " + truncateString(agg, 200)
			}
			return []Event{{Type: EventTool, ToolName: "shell", ToolDetail: detail}}
		case "file_change", "patch_apply":
			return []Event{{Type: EventTool, ToolName: strOr(itemType, "file_change"), ToolDetail: truncateString(str(item["changes"]), 200)}}
		case "mcp_tool_call":
			return []Event{{Type: EventTool, ToolName: str(item["tool"]), ToolDetail: truncateString(str(item["arguments"]), 200)}}
		case "todo_list":
			return nil
		default:
			log.Printf("agent: codex: ignoring item type=%s", itemType)
			return nil
		}
	case "turn.completed":
		usage, _ := raw["usage"].(map[string]any)
		return []Event{{
			Type:      EventUsage,
			TokensIn:  intNum(usage["input_tokens"]),
			TokensOut: intNum(usage["output_tokens"]),
		}}
	case "turn.failed", "error":
		msg := str(raw["message"])
		if errObj, ok := raw["error"].(map[string]any); ok {
			msg = str(errObj["message"])
		}
		return []Event{{Type: EventError, Text: orDefaultStr(msg, "codex run failed")}}
	default:
		log.Printf("agent: codex: ignoring event type=%s", typ)
		return nil
	}
}

func strOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
