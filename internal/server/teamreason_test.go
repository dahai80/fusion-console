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
	"testing"

	"github.com/dahai80/fusion-console/internal/config"
)

// fakeStudioError serves one shape for every non-auth RPC: either the #747
// structured error payload or the legacy message-only shape, chosen by the
// reasonCode arg.
func fakeStudioError(t *testing.T, reasonCode, message, hint string) string {
	t.Helper()
	dir, _ := os.MkdirTemp("", "fc-studio-err-*")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "studio.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				sc := bufio.NewScanner(c)
				sc.Buffer(make([]byte, 64*1024), 1024*1024)
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
					result := map[string]any{"status": "error", "message": message}
					if reasonCode != "" {
						result["reason_code"] = reasonCode
					}
					if hint != "" {
						result["hint"] = hint
					}
					_ = json.NewEncoder(c).Encode(map[string]any{"id": req.ID, "result": result})
				}
			}(conn)
		}
	}()
	return sock
}

func newTestServerWithSocket(t *testing.T, sock string) (*Server, *http.Cookie) {
	t.Helper()
	t.Setenv("FUSION_STUDIO_SOCKET", sock)
	t.Setenv("FUSION_CONSOLE_APIKEY_TEST", "test-key")
	cfg := &config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}
	cfg.AgentStudio.APIKeyID = "TEST"
	cfg.AgentStudio.Team = "alpha"
	s := New(cfg, t.TempDir(), nil)
	return s, loginCookie(t, s)
}

// TestTeamOpReasonCodeSurfaced: the daemon's structured error payload
// ({"status":"error","reason_code","message","hint"}, issue #747) must reach
// the HTTP body as {error, reason_code, hint} so the UI can render copy.
func TestTeamOpReasonCodeSurfaced(t *testing.T) {
	sock := fakeStudioError(t, "lead_not_running", "lead loop not running", "start the lead loop first")
	s, cookie := newTestServerWithSocket(t, sock)
	req := httptest.NewRequest("POST", "/api/team/op", strings.NewReader(`{"op":"pause"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleTeamOp(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if out["error"] != "lead loop not running" || out["reason_code"] != "lead_not_running" || out["hint"] != "start the lead loop first" {
		t.Fatalf("payload = %v", out)
	}
}

// TestTeamOpLegacyErrorShape: a daemon without reason_code (legacy
// {"status":"error","message":...}) still fails with the raw message and no
// reason_code key.
func TestTeamOpLegacyErrorShape(t *testing.T) {
	sock := fakeStudioError(t, "", "unknown permission mode: bypassPermissions", "")
	s, cookie := newTestServerWithSocket(t, sock)
	req := httptest.NewRequest("POST", "/api/team/op", strings.NewReader(`{"op":"pause"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleTeamOp(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["error"] != "unknown permission mode: bypassPermissions" {
		t.Fatalf("payload = %v", out)
	}
	if _, ok := out["reason_code"]; ok {
		t.Fatalf("legacy shape must not invent reason_code: %v", out)
	}
}

// TestTeamResultTypedError covers the shared result-carrier directly.
func TestTeamResultTypedError(t *testing.T) {
	_, err := teamResult(map[string]any{"status": "error", "reason_code": "task_not_found", "message": "no such task", "hint": "check teams.status"})
	te, ok := err.(*teamRPCError)
	if !ok {
		t.Fatalf("want *teamRPCError, got %T: %v", err, err)
	}
	if te.Code != "task_not_found" || te.Message != "no such task" || te.Hint != "check teams.status" {
		t.Fatalf("fields = %+v", te)
	}
	if te.Error() == "" {
		t.Fatalf("Error() must be non-empty")
	}
	if _, err := teamResult(map[string]any{"status": "ok"}); err != nil {
		t.Fatalf("ok result errored: %v", err)
	}
}
