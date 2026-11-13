package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOpenAICompatRequestFields pins the wire format: lowercase
// role/content json tags (regression: 422 from providers on {"Role":...}).
func TestOpenAICompatRequestFields(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		gotBody = buf.String()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p := &OpenAICompatProvider{BaseURL: srv.URL}
	ch, err := p.ChatStream(context.Background(), "m", "k", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for range ch {
	}
	if !strings.Contains(gotBody, `"role":"user"`) || !strings.Contains(gotBody, `"content":"hi"`) {
		t.Fatalf("wire format wrong: %s", gotBody)
	}
	if strings.Contains(gotBody, `"Role"`) {
		t.Fatalf("capitalized fields leaked into wire format: %s", gotBody)
	}
}

// TestAnthropicBaseURLVariants pins /v1 handling — never /v1/v1/messages.
func TestAnthropicBaseURLVariants(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer srv.Close()
	for _, base := range []string{srv.URL, srv.URL + "/v1"} {
		p := &AnthropicCompatProvider{BaseURL: base}
		ch, err := p.ChatStream(context.Background(), "m", "k", nil)
		if err != nil {
			t.Fatalf("ChatStream(%s): %v", base, err)
		}
		for range ch {
		}
		if gotPath != "/v1/messages" {
			t.Fatalf("base %q hit %q, want /v1/messages", base, gotPath)
		}
	}
}

// TestOpenAICompatStream spins a fake SSE provider and checks delta/usage/done.
func TestOpenAICompatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"he"}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"llo"}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &OpenAICompatProvider{BaseURL: srv.URL}
	ch, err := p.ChatStream(context.Background(), "glm-5", "test-key", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	text, usageSeen, done := "", false, false
	for ev := range ch {
		switch ev.Type {
		case EventDelta:
			text += ev.Text
		case EventUsage:
			usageSeen = ev.TokensIn == 3 && ev.TokensOut == 2
		case EventDone:
			done = true
		case EventError:
			t.Fatalf("unexpected error event: %s", ev.Text)
		}
	}
	if text != "hello" {
		t.Errorf("text = %q, want hello", text)
	}
	if !usageSeen {
		t.Error("usage event missing or wrong")
	}
	if !done {
		t.Error("done event missing")
	}
}

// TestOpenAICompatHTTPError verifies non-200 fails visibly with body detail.
func TestOpenAICompatHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := &OpenAICompatProvider{BaseURL: srv.URL}
	_, err := p.ChatStream(context.Background(), "m", "k", nil)
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

// TestAnthropicCompatStream checks content_block_delta + message_stop parsing.
func TestAnthropicCompatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "ak" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"message_start","message":{"usage":{"input_tokens":9,"output_tokens":0}}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"yo"}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"message_delta","usage":{"input_tokens":0,"output_tokens":4}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer srv.Close()

	p := &AnthropicCompatProvider{BaseURL: srv.URL}
	ch, err := p.ChatStream(context.Background(), "claude-x", "ak", nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	text, tin, tout := "", 0, 0
	for ev := range ch {
		switch ev.Type {
		case EventDelta:
			text += ev.Text
		case EventUsage:
			if ev.TokensIn > 0 {
				tin = ev.TokensIn
			}
			if ev.TokensOut > 0 {
				tout = ev.TokensOut
			}
		}
	}
	if text != "yo" || tin != 9 || tout != 4 {
		t.Errorf("text=%q in=%d out=%d", text, tin, tout)
	}
}

// TestModelListCache verifies second fetch hits cache (counter stays 1).
func TestModelListCache(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"data":[{"id":"glm-5"},{"id":"gpt-4o"}]}`)
	}))
	defer srv.Close()
	m := NewModelList()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a, err := m.Fetch(ctx, srv.URL, "k")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	b, err := m.Fetch(ctx, srv.URL, "k")
	if err != nil {
		t.Fatalf("fetch 2: %v", err)
	}
	if len(a) != 2 || len(b) != 2 || calls != 1 {
		t.Errorf("cache broken: a=%v b=%v calls=%d", a, b, calls)
	}
}

// TestOpenAICompatToolCallStream pins the streaming tool-call wire format:
// first delta carries id+name (nested under function), arguments accumulate
// by index, EventToolCall fires once with complete calls.
func TestOpenAICompatToolCallStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"run_agent","arguments":""}}]},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"agent\":"}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"claude\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p := &OpenAICompatProvider{BaseURL: srv.URL}
	ch, err := p.ChatStreamTools(context.Background(), "m", "k", nil, []ToolDecl{{Name: "run_agent"}})
	if err != nil {
		t.Fatalf("ChatStreamTools: %v", err)
	}
	var calls []ToolCall
	done := false
	for ev := range ch {
		switch ev.Type {
		case EventToolCall:
			calls = ev.ToolCalls
		case EventDone:
			done = true
		case EventError:
			t.Fatalf("unexpected error: %s", ev.Text)
		}
	}
	if !done || len(calls) != 1 {
		t.Fatalf("done=%v calls=%+v", done, calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "run_agent" {
		t.Errorf("id/name wrong: %+v", calls[0])
	}
	if calls[0].Arguments != `{"agent":"claude"}` {
		t.Errorf("arguments not accumulated: %q", calls[0].Arguments)
	}
}

// TestOpenAICompatToolDeclOnWire pins tools being sent in request body.
func TestOpenAICompatToolDeclOnWire(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		gotBody = buf.String()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p := &OpenAICompatProvider{BaseURL: srv.URL}
	tools := []ToolDecl{{Name: "run_agent", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}}
	ch, err := p.ChatStreamTools(context.Background(), "m", "k", nil, tools)
	if err != nil {
		t.Fatalf("ChatStreamTools: %v", err)
	}
	for range ch {
	}
	// OpenAI wire format: tools must be nested under function, never flat
	// (flat entries are silently ignored — model hallucinates tool results).
	if !strings.Contains(gotBody, `"type":"function"`) || !strings.Contains(gotBody, `"function":{`) {
		t.Fatalf("tools not wrapped in OpenAI function format: %s", gotBody)
	}
}

// TestAnthropicToolUseStream pins the tool_use block parsing: block start
// (id+name) + partial_json arguments + stop_reason tool_use.
func TestAnthropicToolUseStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"run_agent"}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"agent\":\"claude\"}"}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer srv.Close()
	p := &AnthropicCompatProvider{BaseURL: srv.URL}
	ch, err := p.ChatStreamTools(context.Background(), "m", "k", nil, nil)
	if err != nil {
		t.Fatalf("ChatStreamTools: %v", err)
	}
	var calls []ToolCall
	for ev := range ch {
		if ev.Type == EventToolCall {
			calls = ev.ToolCalls
		}
	}
	if len(calls) != 1 || calls[0].ID != "tu_1" || calls[0].Name != "run_agent" || calls[0].Arguments != `{"agent":"claude"}` {
		t.Fatalf("tool calls wrong: %+v", calls)
	}
}

// TestAnthropicToolMessageConversion pins system extraction + tool_result
// role mapping (tool role -> user content block).
func TestAnthropicToolMessageConversion(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		gotBody = buf.String()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer srv.Close()
	p := &AnthropicCompatProvider{BaseURL: srv.URL}
	msgs := []Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "tu_1", Name: "run_agent", Arguments: `{"task":"x"}`}}},
		{Role: "tool", ToolCallID: "tu_1", Content: "done"},
	}
	ch, err := p.ChatStream(context.Background(), "m", "k", msgs)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for range ch {
	}
	for _, want := range []string{`"type":"tool_result"`, `"tool_use_id":"tu_1"`, `"type":"tool_use"`, `"input":{"task":"x"}`, `"system":"be brief"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %s: %s", want, gotBody)
		}
	}
}

func TestNormalizeGatewayBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "http://127.0.0.1:11432/v1"},
		{"http://127.0.0.1:11432", "http://127.0.0.1:11432/v1"},
		{"http://127.0.0.1:11432/", "http://127.0.0.1:11432/v1"},
		{"http://127.0.0.1:11432/v1", "http://127.0.0.1:11432/v1"},
		{"http://127.0.0.1:11432/v1/", "http://127.0.0.1:11432/v1"},
		{" http://10.0.0.5:11432 ", "http://10.0.0.5:11432/v1"},
		{"http://proxy.example.com/gw/v1", "http://proxy.example.com/gw/v1"},
	}
	for _, c := range cases {
		if got := normalizeGatewayBaseURL(c.in); got != c.want {
			t.Errorf("normalizeGatewayBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Regression: base_url without /v1 hit /chat/completions and got a 404 from
// the gateway (Windows fresh-install report, 2026-11-13).
func TestGatewayProviderChatUsesV1Path(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"1","choices":[{"delta":{"content":"hi"}}]}`+"\n\n")
		fmt.Fprint(w, `data: [DONE]`+"\n\n")
	}))
	defer srv.Close()
	p := NewGatewayProvider(srv.URL) // no /v1 in base URL
	ch, err := p.ChatStream(context.Background(), "m", "k", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for range ch {
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("request path = %q, want /v1/chat/completions", gotPath)
	}
}
