package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
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
	evs := parseClaudeJSON(mustRaw(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"},{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]},"session_id":"s1"}`), &parseState{})
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
	evs := parseClaudeJSON(mustRaw(t, `{"type":"result","subtype":"success","result":"done","total_cost_usd":0.42,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":4},"session_id":"s2","is_error":false}`), &parseState{})
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	if evs[0].Type != EventResult || evs[0].Text != "done" {
		t.Errorf("result wrong: %+v", evs[0])
	}
	u := evs[1]
	if u.Type != EventUsage || u.CostUSD != 0.42 || u.TokensIn != 10 || u.TokensOut != 5 || u.TokensCacheRead != 3 || u.TokensCacheWrite != 4 || u.SessionID != "s2" {
		t.Errorf("usage wrong: %+v", u)
	}
}

func TestParseClaudeUsageFallbackFromAssistant(t *testing.T) {
	// atlascode <0.1.120: result event carries no usage; the per-assistant
	// accumulation must back the usage event instead.
	st := &parseState{}
	evs := parseClaudeJSON(mustRaw(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":7,"cache_creation_input_tokens":9}}}`), st)
	if len(evs) != 1 || evs[0].Type != EventText {
		t.Fatalf("assistant event wrong: %+v", evs)
	}
	evs = parseClaudeJSON(mustRaw(t, `{"type":"result","subtype":"success","result":"done","is_error":false}`), st)
	var u Event
	for _, ev := range evs {
		if ev.Type == EventUsage {
			u = ev
		}
	}
	if u.Type != EventUsage || u.TokensIn != 100 || u.TokensOut != 50 || u.TokensCacheRead != 7 || u.TokensCacheWrite != 9 {
		t.Fatalf("accumulated usage fallback wrong: %+v", u)
	}
}

// TestParseAtlasFixture replays a real atlascode 0.1.120 stream-json
// transcript (recorded against the local macfabric gateway) through the
// process pump: assistant text line, then result with claude-shaped usage and
// no total_cost_usd (honest absence when endpoint pricing is unknown).
func TestParseAtlasFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/atlas-0120.jsonl")
	if err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	h := &processHandle{parser: parseClaudeJSON}
	var events []Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		events = append(events, h.handleLine(strings.TrimSpace(line))...)
	}
	var result, usage *Event
	for i := range events {
		switch events[i].Type {
		case EventResult:
			result = &events[i]
		case EventUsage:
			usage = &events[i]
		case EventError:
			t.Fatalf("fixture replay must be error-free, got %+v", events[i])
		}
	}
	if result == nil || result.Text != "OK" {
		t.Fatalf("result event wrong: %+v", result)
	}
	if usage == nil || usage.TokensIn != 22481 || usage.TokensOut != 3 || usage.TokensCacheRead != 21888 || usage.TokensCacheWrite != 0 {
		t.Fatalf("usage from fixture wrong: %+v", usage)
	}
	if usage.CostUSD != 0 {
		t.Errorf("fixture endpoint has no pricing; cost must stay 0, got %v", usage.CostUSD)
	}
	if !h.state.usageSeen || h.state.meaningful == 0 {
		t.Errorf("sentinel state wrong: usageSeen=%v meaningful=%d", h.state.usageSeen, h.state.meaningful)
	}
}

func TestParseClaudeIsError(t *testing.T) {
	evs := parseClaudeJSON(mustRaw(t, `{"type":"result","subtype":"error_max_turns","result":"","is_error":true}`), &parseState{})
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
	evs := parseCodexJSON(mustRaw(t, `{"type":"item.completed","item":{"type":"command_execution","command":"go test ./...","aggregated_output":"ok"}}`), &parseState{})
	if len(evs) != 1 || evs[0].Type != EventTool || evs[0].ToolName != "shell" {
		t.Fatalf("tool event wrong: %+v", evs)
	}
	evs = parseCodexJSON(mustRaw(t, `{"type":"item.completed","item":{"type":"agent_message","text":"final answer"}}`), &parseState{})
	if len(evs) != 1 || evs[0].Type != EventResult || evs[0].Text != "final answer" {
		t.Fatalf("result event wrong: %+v", evs)
	}
}

func TestParseCodexTurnCompleted(t *testing.T) {
	evs := parseCodexJSON(mustRaw(t, `{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":20}}`), &parseState{})
	if len(evs) != 1 || evs[0].Type != EventUsage || evs[0].TokensIn != 100 || evs[0].TokensOut != 20 {
		t.Fatalf("usage wrong: %+v", evs)
	}
}

func TestParseCodexFailure(t *testing.T) {
	evs := parseCodexJSON(mustRaw(t, `{"type":"turn.failed","error":{"message":"boom"}}`), &parseState{})
	if len(evs) != 1 || evs[0].Type != EventError || evs[0].Text != "boom" {
		t.Fatalf("error event wrong: %+v", evs)
	}
}

func TestParseOpencodeTextAndStepFinish(t *testing.T) {
	evs := parseOpencodeJSON(mustRaw(t, `{"type":"text","part":{"text":"thinking out loud"}}`), &parseState{})
	if len(evs) != 1 || evs[0].Type != EventText || evs[0].Text != "thinking out loud" {
		t.Fatalf("text wrong: %+v", evs)
	}
	evs = parseOpencodeJSON(mustRaw(t, `{"type":"step_finish","part":{"cost":0.01,"tokens":{"input":11,"output":7}}}`), &parseState{})
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

func TestTokenCapKillsWhenCostUnavailable(t *testing.T) {
	// The token cap is the fallback for CLIs that never report cost: it must
	// kill mid-run while no positive CostUSD has been seen, and must NOT fire
	// once cost data arrived.
	h := &processHandle{tokenBudget: 10, parser: parseClaudeJSON}
	evs := h.handleLine(`{"type":"assistant","message":{"content":[],"usage":{"input_tokens":8,"output_tokens":4}}}`)
	if len(evs) != 1 || evs[0].Type != EventError {
		t.Fatalf("token cap breach must emit error, got %+v", evs)
	}
	if !h.killed {
		t.Fatal("process must be flagged killed")
	}
	h2 := &processHandle{tokenBudget: 10, parser: parseClaudeJSON}
	evs2 := h2.handleLine(`{"type":"result","result":"x","total_cost_usd":0.01,"usage":{"input_tokens":500,"output_tokens":500}}`)
	for _, ev := range evs2 {
		if ev.Type == EventError && strings.Contains(ev.Text, "token budget cap") {
			t.Fatalf("token cap must not fire once cost data arrived: %+v", ev)
		}
	}
}

func TestProtocolFor(t *testing.T) {
	cases := []struct{ name, protocol, want string }{
		{"claude", "", ProtocolClaudeStreamJSON},
		{"fusion-code", "", ProtocolClaudeStreamJSON},
		{"atlascode", "", ProtocolClaudeStreamJSON},
		{"codex", "", ProtocolCodexJSONL},
		{"opencode", "", ProtocolOpencodeJSONL},
		{"whatever", ProtocolCodexJSONL, ProtocolCodexJSONL},
	}
	for _, c := range cases {
		if got := ProtocolFor(c.name, c.protocol); got != c.want {
			t.Errorf("ProtocolFor(%q,%q)=%q want %q", c.name, c.protocol, got, c.want)
		}
	}
	if a := NewFor("atlascode", "", ""); a.Name() != "atlascode" {
		t.Errorf("NewFor atlascode wrong: %+v", a)
	}
	if ca, ok := NewFor("fusion-code", "", "").(ClaudeCompatAdapter); !ok || ca.Command != "fusion-code" {
		t.Errorf("NewFor fusion-code default command wrong: %+v", ca)
	}
}

func TestDetect(t *testing.T) {
	ca := ClaudeCompatAdapter{AgentName: "claude", Command: "claude"}
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

func TestSentinelZeroMeaningfulEvents(t *testing.T) {
	// A clean exit whose stream parses to nothing must surface a visible
	// protocol-drift error instead of an empty success.
	_, ch, err := launch(context.Background(), RunOpts{Workdir: t.TempDir()}, "printf", []string{`{"type":"item.started"}\n`}, false, parseCodexJSON)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	var sawDrift, sawDone bool
	for ev := range ch {
		switch ev.Type {
		case EventError:
			if strings.Contains(ev.Text, "protocol drift") {
				sawDrift = true
			}
		case EventDone:
			sawDone = true
		}
	}
	if !sawDone || !sawDrift {
		t.Fatalf("sentinel wrong: drift=%v done=%v", sawDrift, sawDone)
	}
}

func TestSentinelNoUsageNotice(t *testing.T) {
	// Plain-text output parses fine but carries no usage: the run succeeds
	// with a notice, not an error.
	_, ch, err := launch(context.Background(), RunOpts{Workdir: t.TempDir()}, "echo", []string{"hello"}, false, parseClaudeJSON)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	var sawNotice, sawError bool
	for ev := range ch {
		switch ev.Type {
		case EventNotice:
			sawNotice = true
		case EventError:
			sawError = true
		}
	}
	if !sawNotice || sawError {
		t.Fatalf("usage notice wrong: notice=%v error=%v", sawNotice, sawError)
	}
}
