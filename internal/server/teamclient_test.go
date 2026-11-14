package server

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// fakeStudio serves newline-delimited JSON-RPC over a UDS socket, mirroring
// the agent-studio daemon wire format (auth.login + one echo method).
func fakeStudio(t *testing.T, requireAuth bool) string {
	t.Helper()
	dir, _ := os.MkdirTemp("", "fc-studio-*")
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
					if requireAuth && req.Params["_auth"] != "jwt-test" {
						_ = json.NewEncoder(c).Encode(map[string]any{
							"id": req.ID, "error": map[string]any{"code": 401, "message": "no _auth"},
						})
						continue
					}
					_ = json.NewEncoder(c).Encode(map[string]any{
						"id": req.ID, "result": map[string]any{"ok": true, "method": req.Method},
					})
				}
			}(conn)
		}
	}()
	return sock
}

// TestTeamRPCUDSRoundTrip verifies the UDS wire format: newline-delimited
// JSON-RPC, _auth injection, result decoding.
func TestTeamRPCUDSRoundTrip(t *testing.T) {
	sock := fakeStudio(t, true)
	res, err := teamRPC(sock, "jwt-test", "task.list", nil)
	if err != nil {
		t.Fatalf("teamRPC: %v", err)
	}
	if res["method"] != "task.list" {
		t.Errorf("unexpected result: %v", res)
	}
}

// TestTeamRPCErrorSurface verifies RPC errors come back as errors, not nil.
func TestTeamRPCErrorSurface(t *testing.T) {
	sock := fakeStudio(t, true)
	if _, err := teamRPC(sock, "wrong-jwt", "task.list", nil); err == nil {
		t.Fatal("expected error for rpc failure, got nil")
	}
}

// TestTeamRPCDialFailure verifies a missing socket is a visible error.
func TestTeamRPCDialFailure(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "missing.sock")
	if _, err := teamRPC(sock, "", "task.list", nil); err == nil {
		t.Fatal("expected dial error, got nil")
	}
}
