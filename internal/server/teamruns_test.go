package server

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeStudioRecord records each non-auth request and answers every method
// with a configured result, so proxy handlers can be asserted verbatim.
type fakeStudioRecord struct {
	mu     sync.Mutex
	socket string
	lastM  string
	lastP  map[string]any
	result map[string]any
}

func newFakeStudioRecord(t *testing.T, result map[string]any) *fakeStudioRecord {
	t.Helper()
	dir, _ := os.MkdirTemp("", "fc-studio-rec-*")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "studio.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	rec := &fakeStudioRecord{socket: sock, result: result}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				sc := bufio.NewScanner(c)
				sc.Buffer(make([]byte, 256*1024), 4*1024*1024)
				for sc.Scan() {
					var req struct {
						ID     int64          `json:"id"`
						Method string         `json:"method"`
						Params map[string]any `json:"params"`
					}
					if json.Unmarshal(sc.Bytes(), &req) != nil {
						continue
					}
					if req.Method == "auth.login" {
						_ = json.NewEncoder(c).Encode(map[string]any{
							"id": req.ID, "result": map[string]any{"jwt": "jwt-test"},
						})
						continue
					}
					rec.mu.Lock()
					rec.lastM = req.Method
					rec.lastP = req.Params
					res := rec.result
					rec.mu.Unlock()
					_ = json.NewEncoder(c).Encode(map[string]any{"id": req.ID, "result": res})
				}
			}(conn)
		}
	}()
	return rec
}

func (f *fakeStudioRecord) last(t *testing.T) (string, map[string]any) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastM, f.lastP
}

// TestTeamRunsListProxy: GET /api/team/runs proxies team+task_id to
// teams.runs_list and passes the daemon payload through verbatim.
func TestTeamRunsListProxy(t *testing.T) {
	payload := map[string]any{
		"status": "ok", "team": "alpha", "task_id": "t1",
		"runs": []any{map[string]any{"attempt": float64(1), "member": "api-worker", "status": "ok"}},
	}
	rec := newFakeStudioRecord(t, payload)
	s, cookie := newTestServerWithSocket(t, rec.socket)
	req := httptest.NewRequest("GET", "/api/team/runs?team=alpha&task_id=t1", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.handleTeamRuns(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	method, params := rec.last(t)
	if method != "teams.runs_list" {
		t.Fatalf("method = %s", method)
	}
	if params["team"] != "alpha" || params["task_id"] != "t1" {
		t.Fatalf("params = %v", params)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out["task_id"] != "t1" {
		t.Fatalf("body = %s err=%v", w.Body.String(), err)
	}
}

// TestTeamRunMessagesProxy: attempt must reach the daemon as a number.
func TestTeamRunMessagesProxy(t *testing.T) {
	payload := map[string]any{
		"status": "ok", "team": "alpha", "task_id": "t1", "attempt": 2,
		"messages": []any{map[string]any{"seq": 1, "ts": 1000.0, "type": "prompt", "payload": map[string]any{}}},
	}
	rec := newFakeStudioRecord(t, payload)
	s, cookie := newTestServerWithSocket(t, rec.socket)
	req := httptest.NewRequest("GET", "/api/team/run_messages?team=alpha&task_id=t1&attempt=2", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.handleTeamRunMessages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	method, params := rec.last(t)
	if method != "teams.run_messages" {
		t.Fatalf("method = %s", method)
	}
	if v, ok := params["attempt"].(float64); !ok || v != 2 {
		t.Fatalf("attempt param = %v (%T)", params["attempt"], params["attempt"])
	}
}

// TestTeamRunUsageProxy verifies the usage passthrough.
func TestTeamRunUsageProxy(t *testing.T) {
	payload := map[string]any{
		"status": "ok", "task_id": "t1",
		"usage": []any{map[string]any{"attempt": 1, "status": "ok", "usage": map[string]any{"in": 18200.0}}},
	}
	rec := newFakeStudioRecord(t, payload)
	s, cookie := newTestServerWithSocket(t, rec.socket)
	req := httptest.NewRequest("GET", "/api/team/run_usage?task_id=t1", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.handleTeamRunUsage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	method, params := rec.last(t)
	if method != "teams.run_usage" || params["task_id"] != "t1" {
		t.Fatalf("method=%s params=%v", method, params)
	}
}

// TestTeamRunsErrorCarriesReasonCode: a daemon status:error result (e.g.
// task_not_found) maps to 502 with reason_code preserved for the UI.
func TestTeamRunsErrorCarriesReasonCode(t *testing.T) {
	rec := newFakeStudioRecord(t, map[string]any{
		"status": "error", "reason_code": "task_not_found", "message": "no such task", "hint": "check teams.status",
	})
	s, cookie := newTestServerWithSocket(t, rec.socket)
	req := httptest.NewRequest("GET", "/api/team/runs?task_id=nope", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.handleTeamRuns(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"reason_code":"task_not_found"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

// TestTeamRunsMissingTaskID: task_id is required client-side.
func TestTeamRunsMissingTaskID(t *testing.T) {
	rec := newFakeStudioRecord(t, map[string]any{"status": "ok"})
	s, cookie := newTestServerWithSocket(t, rec.socket)
	req := httptest.NewRequest("GET", "/api/team/runs", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.handleTeamRuns(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}
