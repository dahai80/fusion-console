// Chat and interactive agent turns outlive their HTTP stream. A phone that
// locks, switches between Wi-Fi and cellular or loses the tailnet mid-reply
// used to cancel the whole turn through the request context: the brain or
// agent was killed, the half-written reply was never saved, and the browser
// only showed its own "network error". A turn now runs on a detached context,
// records every event it streams, and the browser picks it back up through
// GET /api/conversations/{id}/turn after the stream breaks.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	turnKeep      = 15 * time.Minute // a finished turn stays readable this long
	turnMaxEvents = 4000             // consecutive text chunks merge, so this is rarely reached
)

// turnHeartbeat paces the SSE comment that keeps quiet stretches (a brain
// waiting on a worker, a long tool call) from looking like a dead link.
var turnHeartbeat = 15 * time.Second

var errTurnBusy = errors.New("这个对话上一条消息还在处理中，等它结束再发")

// chatTurn is one streamed turn as the browser sees it on reconnect.
type chatTurn struct {
	ID        string           `json:"id"`
	Kind      string           `json:"kind"` // "chat" | "agent"
	Request   string           `json:"request"`
	Running   bool             `json:"running"`
	Started   time.Time        `json:"started_at"`
	Ended     time.Time        `json:"ended_at,omitzero"`
	Events    []map[string]any `json:"events"`
	Truncated bool             `json:"truncated,omitempty"`
	user      string
}

// chatTurnRegistry keeps the latest turn per conversation, in memory: a
// console restart forgets it and the browser falls back to the saved
// messages.
type chatTurnRegistry struct {
	mu     sync.Mutex
	byConv map[int64]*chatTurn
}

// begin registers a new running turn; a conversation answers one message at
// a time. An invalid or empty id gets a server-generated one.
func (g *chatTurnRegistry) begin(conv int64, user, id, kind, request string) (*chatTurn, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.byConv == nil {
		g.byConv = map[int64]*chatTurn{}
	}
	now := time.Now()
	for c, t := range g.byConv {
		if !t.Running && now.Sub(t.Ended) > turnKeep {
			delete(g.byConv, c)
		}
	}
	if cur := g.byConv[conv]; cur != nil && cur.Running {
		return nil, errTurnBusy
	}
	if !validTurnID(id) {
		id = newTurnID()
	}
	if r := []rune(request); len(r) > 200 {
		request = string(r[:200])
	}
	t := &chatTurn{ID: id, Kind: kind, Request: request, Running: true, Started: now, user: user}
	g.byConv[conv] = t
	return t, nil
}

// record appends one streamed event. Consecutive text chunks fold into one
// event so a long reply stays a handful of entries; a folded event is a new
// map, never an edit, so snapshots taken earlier stay consistent.
func (g *chatTurnRegistry) record(t *chatTurn, ev map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	typ, _ := ev["type"].(string)
	text, isText := ev["text"].(string)
	if n := len(t.Events); n > 0 && isText && len(ev) == 2 && (typ == "delta" || typ == "text") {
		last := t.Events[n-1]
		if prev, ok := last["text"].(string); ok && len(last) == 2 && last["type"] == typ {
			t.Events[n-1] = map[string]any{"type": typ, "text": prev + text}
			return
		}
	}
	if len(t.Events) >= turnMaxEvents {
		switch typ {
		case "error", "result", "blocked", "done":
		default:
			t.Truncated = true
			return
		}
	}
	t.Events = append(t.Events, ev)
}

func (g *chatTurnRegistry) finish(t *chatTurn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t.Running = false
	t.Ended = time.Now()
}

// snapshot copies the conversation's latest turn for one user; nil when
// there is none (never started, expired, console restarted).
func (g *chatTurnRegistry) snapshot(conv int64, user string) *chatTurn {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.byConv[conv]
	if t == nil || t.user != user || (!t.Running && time.Since(t.Ended) > turnKeep) {
		return nil
	}
	cp := *t
	cp.Events = append([]map[string]any(nil), t.Events...)
	return &cp
}

func validTurnID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func newTurnID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// turnContext detaches a turn from its request: closing the browser tab or
// losing the network no longer cancels the brain, the agent or the model
// call. The dispatch timeout still bounds a turn nobody is watching.
func (s *Server) turnContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), s.dispatchTimeout())
}

// turnStream starts the SSE response for a turn. Every event is recorded for
// a later reconnect, then written best-effort: once the client is gone the
// writes stop and the turn just keeps recording. A comment heartbeat runs
// until stop, which must be called before the handler returns.
func (s *Server) turnStream(w http.ResponseWriter, t *chatTurn) (send func(map[string]any), stop func()) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	var mu sync.Mutex
	closed, gone := false, false
	write := func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		if closed || gone {
			return
		}
		if _, err := w.Write(b); err != nil {
			gone = true
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	send = func(ev map[string]any) {
		s.turns.record(t, ev)
		data, _ := json.Marshal(ev)
		write([]byte("data: " + string(data) + "\n\n"))
	}
	done := make(chan struct{})
	go func() {
		tick := time.NewTicker(turnHeartbeat)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				write([]byte(": ping\n\n"))
			}
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			close(done)
			mu.Lock()
			closed = true
			mu.Unlock()
		})
	}
	return send, stop
}

// handleConversationTurn reports the conversation's latest streamed turn so
// a browser whose stream broke can keep showing it ({"turn": null} = none).
func (s *Server) handleConversationTurn(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad conversation id"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"turn": s.turns.snapshot(id, s.auth.User(r))})
}
