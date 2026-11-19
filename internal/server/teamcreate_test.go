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

	"github.com/dahai80/fusion-console/internal/config"
)

// fakeStudioFunc serves newline-delimited JSON-RPC over a UDS socket with a
// per-method responder — lets the create-proxy tests script partial success
// (register ok, task 2 rejected) without touching the shared fakeStudio.
func fakeStudioFunc(t *testing.T, respond func(method string, params map[string]any) (map[string]any, error)) string {
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
					res, rpcErr := respond(req.Method, req.Params)
					if rpcErr != nil {
						_ = json.NewEncoder(c).Encode(map[string]any{
							"id": req.ID, "error": map[string]any{"code": 500, "message": rpcErr.Error()},
						})
						continue
					}
					_ = json.NewEncoder(c).Encode(map[string]any{"id": req.ID, "result": res})
				}
			}(conn)
		}
	}()
	return sock
}

func newTeamTestServer(t *testing.T, sock string) (*Server, *http.Cookie) {
	t.Helper()
	t.Setenv("FUSION_STUDIO_SOCKET", sock)
	t.Setenv("FUSION_CONSOLE_APIKEY_TEST", "test-key")
	cfg := &config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}
	cfg.AgentStudio.APIKeyID = "TEST"
	cfg.AgentStudio.Team = "alpha"
	s := New(cfg, t.TempDir(), nil)
	return s, loginCookie(t, s)
}

func postTeamCreate(s *Server, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/team/create", strings.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleTeamCreate(rec, req)
	return rec
}

const wizardBody = `{"team":{"name":"workers-two","namespace_key":"team:workers-two",
"members":[{"name":"alice","role":"coder","skills":["go"],"tool_allowlist":["bash"],
"permission_mode":"default","budget_tokens":50000,"max_concurrency":1}],
"lead_budget_tokens":200000,"playbook":false,"max_depth":1,"metadata":{}},
"tasks":[{"title":"fix the login bug","detail":"output: patch; use repo files; scope: auth only; budget: 10k tokens"}]}`

// TestTeamCreateSuccess verifies the wire format end to end: register carries
// the definition under the "definition" key with kernel field names, tasks
// are added in order on the created team, the response reports task ids.
func TestTeamCreateSuccess(t *testing.T) {
	// The team poller goroutine dials the same fake socket concurrently with
	// the create pipeline; appends must not race on the shared slice.
	var mu sync.Mutex
	var methods []string
	record := func(method string) {
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
	}
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), methods...)
	}
	var addTeams []any
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		record(method)
		switch method {
		case "teams.register":
			def, _ := params["definition"].(map[string]any)
			if def["name"] != "workers-two" {
				t.Errorf("definition.name = %v, want workers-two", def["name"])
			}
			if def["namespace_key"] != "team:workers-two" {
				t.Errorf("definition.namespace_key = %v", def["namespace_key"])
			}
			members, _ := def["members"].([]any)
			if len(members) != 1 {
				t.Errorf("definition.members = %d entries", len(members))
			}
			return map[string]any{"status": "ok", "name": "workers-two", "namespace_key": "team:workers-two"}, nil
		case "teams.add_task":
			addTeams = append(addTeams, params["team"])
			return map[string]any{"status": "ok", "task_id": "task-1"}, nil
		}
		return map[string]any{"status": "ok"}, nil
	})
	s, cookie := newTeamTestServer(t, sock)
	rec := postTeamCreate(s, cookie, wizardBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out teamCreateRes
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != "ok" || out.Team != "workers-two" || out.NamespaceKey != "team:workers-two" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if len(out.Tasks) != 1 || out.Tasks[0].TaskID != "task-1" || out.Tasks[0].Error != "" {
		t.Fatalf("tasks = %+v", out.Tasks)
	}
	if m := snapshot(); len(m) < 2 || m[0] != "teams.register" || m[1] != "teams.add_task" {
		t.Fatalf("call order = %v", m)
	}
	if len(addTeams) != 1 || addTeams[0] != "workers-two" {
		t.Fatalf("add_task team = %v", addTeams)
	}
}

// TestTeamCreateRegisterFailure verifies a kernel rejection comes back as a
// 502 with the error verbatim and that no add_task is attempted.
func TestTeamCreateRegisterFailure(t *testing.T) {
	addTaskCalled := false
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "teams.register" {
			return map[string]any{"status": "error", "message": "team already registered: workers-two"}, nil
		}
		if method == "teams.add_task" {
			addTaskCalled = true
		}
		return map[string]any{"status": "ok"}, nil
	})
	s, cookie := newTeamTestServer(t, sock)
	rec := postTeamCreate(s, cookie, wizardBody)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var errRes map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errRes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errRes["error"] != "team already registered: workers-two" {
		t.Fatalf("error = %q, want kernel message verbatim", errRes["error"])
	}
	if addTaskCalled {
		t.Fatal("add_task attempted after failed register")
	}
}

// TestTeamCreatePartialSuccess verifies the reportable partial state: the
// team registers, task 1 lands, task 2 is rejected — HTTP 200 with the
// per-task error inline (issue #16: partial success must be visible).
func TestTeamCreatePartialSuccess(t *testing.T) {
	n := 0
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "teams.register" {
			return map[string]any{"status": "ok", "name": "workers-two", "namespace_key": "team:workers-two"}, nil
		}
		if method == "teams.add_task" {
			n++
			if n == 2 {
				return map[string]any{"status": "error", "message": "tools outside member allowlist (bob): rm -rf"}, nil
			}
			return map[string]any{"status": "ok", "task_id": "task-1"}, nil
		}
		return map[string]any{"status": "ok"}, nil
	})
	s, cookie := newTeamTestServer(t, sock)
	body := `{"team":{"name":"workers-two","members":[{"name":"alice","role":"coder"}]},
"tasks":[{"title":"task one"},{"title":"task two"}]}`
	rec := postTeamCreate(s, cookie, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out teamCreateRes
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != "ok" {
		t.Fatalf("status = %q — register succeeded, request must not fail", out.Status)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("tasks = %+v", out.Tasks)
	}
	if out.Tasks[0].Error != "" || out.Tasks[0].TaskID != "task-1" {
		t.Fatalf("task 1 = %+v", out.Tasks[0])
	}
	if out.Tasks[1].TaskID != "" || out.Tasks[1].Error != "tools outside member allowlist (bob): rm -rf" {
		t.Fatalf("task 2 = %+v", out.Tasks[1])
	}
}

// TestTeamCreateLeadStart verifies the optional lead loop start rides last
// and its failure is reported without failing the created team.
func TestTeamCreateLeadStart(t *testing.T) {
	var order []string
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		order = append(order, method)
		switch method {
		case "teams.register":
			return map[string]any{"status": "ok", "name": "workers-two"}, nil
		case "teams.lead_start":
			if params["goal"] != "fix the login bug" {
				t.Errorf("lead goal = %v, want first task title fallback", params["goal"])
			}
			return map[string]any{"status": "ok", "lead_id": "lead-1"}, nil
		}
		return map[string]any{"status": "ok", "task_id": "task-1"}, nil
	})
	s, cookie := newTeamTestServer(t, sock)
	rec := postTeamCreate(s, cookie, strings.TrimSuffix(wizardBody, "}")+`,"lead_start":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out teamCreateRes
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.LeadStarted || out.LeadError != "" {
		t.Fatalf("lead = %+v", out)
	}
	if len(order) < 3 || order[len(order)-1] != "teams.lead_start" {
		t.Fatalf("lead_start must come last, order = %v", order)
	}
}

// TestTeamCreateValidation verifies the cheap pre-checks before any RPC.
func TestTeamCreateValidation(t *testing.T) {
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		t.Fatalf("unexpected RPC %s", method)
		return nil, nil
	})
	s, cookie := newTeamTestServer(t, sock)
	for _, body := range []string{
		`{"team":{"members":[{"name":"a","role":"r"}]}}`,
		`{"team":{"name":"x","members":[]}}`,
		`not json`,
	} {
		rec := postTeamCreate(s, cookie, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400", body, rec.Code)
		}
	}
}

// TestTeamTeamsList verifies GET /api/team/teams proxies teams.list.
func TestTeamTeamsList(t *testing.T) {
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "teams.list" {
			return map[string]any{"status": "ok", "teams": []any{
				map[string]any{"name": "alpha", "lifecycle": "ACTIVE"},
			}}, nil
		}
		return map[string]any{"status": "ok"}, nil
	})
	s, cookie := newTeamTestServer(t, sock)
	req := httptest.NewRequest("GET", "/api/team/teams", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleTeamTeams(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Teams []map[string]any `json:"teams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Teams) != 1 || out.Teams[0]["name"] != "alpha" {
		t.Fatalf("teams = %+v", out.Teams)
	}
}

// TestTeamViewTeamOverride verifies ?team= observes a non-default team
// synchronously (wizard deep-link) instead of serving the cached snapshot.
func TestTeamViewTeamOverride(t *testing.T) {
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "task.list" {
			return map[string]any{"status": "ok", "tasks": []any{}}, nil
		}
		return map[string]any{"status": "ok"}, nil
	})
	s, cookie := newTeamTestServer(t, sock)
	req := httptest.NewRequest("GET", "/api/team/view?team=workers-two", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleTeamView(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var snap teamSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !snap.Connected {
		t.Fatalf("override poll did not connect: %+v", snap)
	}
}
