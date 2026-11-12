package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func mustRaw(t *testing.T, line string) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		t.Fatalf("fixture unparsable: %v", err)
	}
	return raw
}

func TestParseClaudeAssistantTextAndTool(t *testing.T) {
	evs := parseClaudeJSON(mustRaw(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"},{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]},"session_id":"s1"}`))
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	if evs[0].Type != EventText || evs[0].Text != "hello" {
		t.Errorf("text event wrong: %+v", evs[0])
	}
	if evs[1].Type != EventTool || evs[1].ToolName != "Bash" || evs[1].ToolDetail != "ls -la" {
		t.Errorf("tool event wrong: %+v", evs[1])
	}
	if evs[0].SessionID != "s1" {
		t.Errorf("session id not propagated: %q", evs[0].SessionID)
	}
}

func TestParseClaudeResult(t *testing.T) {
	evs := parseClaudeJSON(mustRaw(t, `{"type":"result","subtype":"success","result":"done","total_cost_usd":0.42,"usage":{"input_tokens":10,"output_tokens":5},"session_id":"s2","is_error":false}`))
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	if evs[0].Type != EventResult || evs[0].Text != "done" {
		t.Errorf("result wrong: %+v", evs[0])
	}
	u := evs[1]
	if u.Type != EventUsage || u.CostUSD != 0.42 || u.TokensIn != 10 || u.TokensOut != 5 || u.SessionID != "s2" {
		t.Errorf("usage wrong: %+v", u)
	}
}

func TestParseClaudeIsError(t *testing.T) {
	evs := parseClaudeJSON(mustRaw(t, `{"type":"result","subtype":"error_max_turns","result":"","is_error":true}`))
	found := false
	for _, ev := range evs {
		if ev.Type == EventError {
			found = true
		}
	}
	if !found {
		t.Fatalf("is_error=true must emit error event, got %+v", evs)
	}
}

func TestParseCodexItemCompleted(t *testing.T) {
	evs := parseCodexJSON(mustRaw(t, `{"type":"item.completed","item":{"type":"command_execution","command":"go test ./...","aggregated_output":"ok"}}`))
	if len(evs) != 1 || evs[0].Type != EventTool || evs[0].ToolName != "shell" {
		t.Fatalf("tool event wrong: %+v", evs)
	}
	evs = parseCodexJSON(mustRaw(t, `{"type":"item.completed","item":{"type":"agent_message","text":"final answer"}}`))
	if len(evs) != 1 || evs[0].Type != EventResult || evs[0].Text != "final answer" {
		t.Fatalf("result event wrong: %+v", evs)
	}
}

func TestParseCodexTurnCompleted(t *testing.T) {
	evs := parseCodexJSON(mustRaw(t, `{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":20}}`))
	if len(evs) != 1 || evs[0].Type != EventUsage || evs[0].TokensIn != 100 || evs[0].TokensOut != 20 {
		t.Fatalf("usage wrong: %+v", evs)
	}
}

func TestParseCodexFailure(t *testing.T) {
	evs := parseCodexJSON(mustRaw(t, `{"type":"turn.failed","error":{"message":"boom"}}`))
	if len(evs) != 1 || evs[0].Type != EventError || evs[0].Text != "boom" {
		t.Fatalf("error event wrong: %+v", evs)
	}
}

func TestParseOpencodeTextAndStepFinish(t *testing.T) {
	evs := parseOpencodeJSON(mustRaw(t, `{"type":"text","part":{"text":"thinking out loud"}}`))
	if len(evs) != 1 || evs[0].Type != EventText || evs[0].Text != "thinking out loud" {
		t.Fatalf("text wrong: %+v", evs)
	}
	evs = parseOpencodeJSON(mustRaw(t, `{"type":"step_finish","part":{"cost":0.01,"tokens":{"input":11,"output":7}}}`))
	if len(evs) != 1 || evs[0].Type != EventUsage || evs[0].TokensIn != 11 || evs[0].TokensOut != 7 || evs[0].CostUSD != 0.01 {
		t.Fatalf("usage wrong: %+v", evs)
	}
}

func TestProcessPlainLineFallback(t *testing.T) {
	h := &processHandle{parser: parseOpencodeJSON}
	evs := h.parseLine("plain text output")
	if len(evs) != 1 || evs[0].Type != EventText || evs[0].Text != "plain text output\n" {
		t.Fatalf("plain fallback wrong: %+v", evs)
	}
}

func TestProcessUnparsableJSONFailsVisible(t *testing.T) {
	h := &processHandle{parser: parseClaudeJSON}
	evs := h.parseLine("{not json")
	if len(evs) != 1 || evs[0].Type != EventError {
		t.Fatalf("unparsable JSON must fail visible, got %+v", evs)
	}
}

func TestBudgetCapKillsProcess(t *testing.T) {
	h := &processHandle{budget: 1.0, parser: parseClaudeJSON}
	evs := h.handleLine(`{"type":"result","result":"x","total_cost_usd":2.5,"usage":{"input_tokens":1,"output_tokens":1}}`)
	if len(evs) != 1 || evs[0].Type != EventError {
		t.Fatalf("budget breach must emit error, got %+v", evs)
	}
	if !h.killed {
		t.Fatal("process must be flagged killed")
	}
}

func TestDetect(t *testing.T) {
	ca := ClaudeAdapter{}
	if !ca.Detect() {
		t.Skip("claude CLI not on PATH in this environment")
	}
}

func TestLaunchEcho(t *testing.T) {
	// End-to-end subprocess pump: echo emits a plain-text line, done closes.
	opts := RunOpts{Task: "", Workdir: t.TempDir()}
	_, ch, err := launch(context.Background(), opts, "echo", []string{"hello-pump"}, false, parseOpencodeJSON)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	var got Event
	for ev := range ch {
		if ev.Type == EventText {
			got = ev
		}
	}
	if got.Text != "hello-pump\n" {
		t.Fatalf("echo text wrong: %q", got.Text)
	}
}
