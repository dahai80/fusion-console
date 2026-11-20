package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/store"
)

// A phone that drops the stream mid-reply must not cancel the turn: the model
// call finishes, the reply is saved, and the turn endpoint replays it.
func TestChatTurnSurvivesDroppedStream(t *testing.T) {
	release := make(chan struct{})
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(text string) {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", text)
			w.(http.Flusher).Flush()
		}
		chunk("Hel")
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		if r.Context().Err() != nil {
			return // the turn was cancelled with the browser's connection
		}
		chunk("lo")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	t.Setenv("FUSION_CONSOLE_APIKEY_TURNTEST", "x")
	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), st)
	s.cfg.Provider = []config.Provider{{ID: "p", Name: "p", Type: "openai_compat", BaseURL: llm.URL + "/v1", Model: "m", APIKeyID: "turntest", Enabled: true}}
	cookie := loginCookie(t, s)
	conv, err := st.CreateConversation("admin", "conv", "chat", "", "")
	if err != nil {
		t.Fatalf("create conv: %v", err)
	}
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	ctx, drop := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/api/chat",
		strings.NewReader(fmt.Sprintf(`{"conversation_id":%d,"provider_id":"p","content":"hi","turn_id":"turn-1"}`, conv)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	if !strings.Contains(line, `"Hel"`) {
		t.Fatalf("first event = %q", line)
	}

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(cookie)
		s.mux.ServeHTTP(rec, r)
		return rec
	}
	turnPath := fmt.Sprintf("/api/conversations/%d/turn", conv)
	var body struct {
		Turn *chatTurn `json:"turn"`
	}
	_ = json.Unmarshal(get(turnPath).Body.Bytes(), &body)
	if body.Turn == nil || !body.Turn.Running || body.Turn.ID != "turn-1" || body.Turn.Kind != "chat" || body.Turn.Request != "hi" {
		t.Fatalf("running turn = %+v", body.Turn)
	}
	busy := httptest.NewRecorder()
	again := httptest.NewRequest("POST", "/api/chat", strings.NewReader(fmt.Sprintf(`{"conversation_id":%d,"provider_id":"p","content":"hi again"}`, conv)))
	again.AddCookie(cookie)
	s.mux.ServeHTTP(busy, again)
	if busy.Code != http.StatusConflict {
		t.Fatalf("second message while answering = %d %s", busy.Code, busy.Body.String())
	}

	drop() // the phone locks / switches networks
	resp.Body.Close()
	time.Sleep(150 * time.Millisecond)
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for {
		body.Turn = nil
		_ = json.Unmarshal(get(turnPath).Body.Bytes(), &body)
		if body.Turn != nil && !body.Turn.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("turn never finished: %+v", body.Turn)
		}
		time.Sleep(20 * time.Millisecond)
	}
	text, done := "", false
	for _, ev := range body.Turn.Events {
		if ev["type"] == "delta" {
			text += ev["text"].(string)
		}
		done = done || ev["type"] == "done"
	}
	if text != "Hello" || !done {
		t.Fatalf("replayed events = %v", body.Turn.Events)
	}
	msgs, _ := st.ListMessages(conv)
	if len(msgs) != 2 || msgs[1].Role != "assistant" || msgs[1].Content != "Hello" {
		t.Fatalf("saved messages = %+v", msgs)
	}

	// Another user never sees the turn.
	s.turns.mu.Lock()
	s.turns.byConv[conv].user = "someone-else"
	s.turns.mu.Unlock()
	if rec := get(turnPath); !strings.Contains(rec.Body.String(), `"turn":null`) {
		t.Fatalf("foreign turn leaked: %s", rec.Body.String())
	}
}

func TestChatTurnRegistry(t *testing.T) {
	var g chatTurnRegistry
	a, err := g.begin(7, "admin", "bad id!", "chat", strings.Repeat("长", 300))
	if err != nil || !validTurnID(a.ID) || a.ID == "bad id!" || len([]rune(a.Request)) != 200 {
		t.Fatalf("begin = %+v %v", a, err)
	}
	if _, err := g.begin(7, "admin", "x", "chat", ""); err != errTurnBusy {
		t.Fatalf("second running turn: %v", err)
	}
	if _, err := g.begin(8, "admin", "y", "agent", ""); err != nil {
		t.Fatalf("other conversation blocked: %v", err)
	}
	g.record(a, map[string]any{"type": "delta", "text": "a"})
	before := g.snapshot(7, "admin")
	g.record(a, map[string]any{"type": "delta", "text": "b"})
	g.record(a, map[string]any{"type": "tool", "tool": "run_agent", "status": "running"})
	g.record(a, map[string]any{"type": "delta", "text": "c"})
	g.record(a, map[string]any{"type": "usage", "tokens_in": 1})
	snap := g.snapshot(7, "admin")
	if len(snap.Events) != 4 || snap.Events[0]["text"] != "ab" || snap.Events[2]["text"] != "c" {
		t.Fatalf("events = %v", snap.Events)
	}
	if before.Events[0]["text"] != "a" {
		t.Fatalf("earlier snapshot changed: %v", before.Events)
	}
	g.finish(a)
	if b, err := g.begin(7, "admin", "next", "chat", ""); err != nil || b.ID != "next" {
		t.Fatalf("begin after finish = %+v %v", b, err)
	}

	full, _ := g.begin(9, "admin", "", "chat", "")
	for i := 0; i < turnMaxEvents+10; i++ {
		g.record(full, map[string]any{"type": "tool", "status": "progress"})
	}
	g.record(full, map[string]any{"type": "error", "text": "boom"})
	snap = g.snapshot(9, "admin")
	if !snap.Truncated || len(snap.Events) != turnMaxEvents+1 || snap.Events[turnMaxEvents]["type"] != "error" {
		t.Fatalf("cap: truncated=%v len=%d", snap.Truncated, len(snap.Events))
	}

	g.finish(full)
	g.mu.Lock()
	full.Ended = time.Now().Add(-turnKeep - time.Minute)
	g.mu.Unlock()
	if g.snapshot(9, "admin") != nil {
		t.Fatal("expired turn still readable")
	}
}

func TestTurnStreamHeartbeat(t *testing.T) {
	old := turnHeartbeat
	turnHeartbeat = 10 * time.Millisecond
	t.Cleanup(func() { turnHeartbeat = old })
	s := &Server{}
	turn, _ := s.turns.begin(1, "admin", "", "chat", "")
	rec := httptest.NewRecorder()
	send, stop := s.turnStream(rec, turn)
	send(map[string]any{"type": "delta", "text": "x"})
	time.Sleep(60 * time.Millisecond)
	stop()
	send(map[string]any{"type": "done"}) // after stop: recorded, not written
	out := rec.Body.String()
	if !strings.HasPrefix(out, "data: {\"text\":\"x\",\"type\":\"delta\"}\n\n") || !strings.Contains(out, ": ping\n\n") || strings.Contains(out, "done") {
		t.Fatalf("stream = %q", out)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
	}
	if snap := s.turns.snapshot(1, "admin"); len(snap.Events) != 2 {
		t.Fatalf("recorded = %v", snap.Events)
	}
}
