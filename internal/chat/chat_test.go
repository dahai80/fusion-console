package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
