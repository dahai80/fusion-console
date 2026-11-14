package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestTeamRPCRoundTrip verifies the WS JSON-RPC wire format: Bearer token in
// Sec-WebSocket-Protocol, _auth injection, result/error decoding.
func TestTeamRPCRoundTrip(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Sec-WebSocket-Protocol"); got != "Bearer tok-1" {
			t.Errorf("missing/incorrect WS token header: %q", got)
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var req struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		if req.Params["_auth"] != "jwt-abc" {
			_ = conn.WriteJSON(map[string]any{"id": req.ID, "error": map[string]any{"code": 401, "message": "no _auth"}})
			return
		}
		_ = conn.WriteJSON(map[string]any{"id": req.ID, "result": map[string]any{"jwt": "ok", "tasks": []any{}}})
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	res, err := teamRPC(wsURL, "tok-1", "jwt-abc", "task.list", nil)
	if err != nil {
		t.Fatalf("teamRPC: %v", err)
	}
	if res["jwt"] != "ok" {
		t.Errorf("unexpected result: %v", res)
	}
}

// TestTeamRPCErrorSurface verifies RPC errors come back as errors, not nil.
func TestTeamRPCErrorSurface(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var req struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	if _, err := teamRPC(wsURL, "", "", "nope.method", nil); err == nil {
		t.Fatal("expected error for rpc failure, got nil")
	}
}
