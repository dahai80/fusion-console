package server

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/dahai80/fusion-console/internal/chat"
	"github.com/dahai80/fusion-console/internal/config"
)

func newTeamToolServer(t *testing.T, sock string) *Server {
	t.Helper()
	t.Setenv("FUSION_STUDIO_SOCKET", sock)
	t.Setenv("FUSION_CONSOLE_APIKEY_TEST", "test-key")
	cfg := &config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}
	cfg.AgentStudio.Enabled = true
	cfg.AgentStudio.APIKeyID = "TEST"
	cfg.AgentStudio.Team = "alpha"
	return New(cfg, t.TempDir(), nil)
}

func runTool(t *testing.T, s *Server, name, args string) (string, error) {
	t.Helper()
	return s.executeToolCall(context.Background(), chat.ToolCall{ID: "t1", Name: name, Arguments: args}, "tester", 0, nil)
}

// TestCreateTeamToolPipeline verifies the chat-side create_team tool runs the
// same register -> add_task -> lead_start pipeline as the wizard proxy and
// reports per-task outcomes in the tool result text.
func TestCreateTeamToolPipeline(t *testing.T) {
	// Poller goroutine dials the same fake socket concurrently with the tool
	// pipeline; appends must not race on the shared slice (CI flake).
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
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		record(method)
		switch method {
		case "teams.register":
			def, _ := params["definition"].(map[string]any)
			if def["name"] != "report-crew" {
				t.Errorf("definition.name = %v", def["name"])
			}
			return map[string]any{"status": "ok", "namespace_key": "team:report-crew"}, nil
		case "teams.add_task":
			return map[string]any{"status": "ok", "task_id": "task-9"}, nil
		case "teams.lead_start":
			return map[string]any{"status": "ok"}, nil
		}
		return map[string]any{"status": "ok"}, nil
	})
	s := newTeamToolServer(t, sock)
	out, err := runTool(t, s, "create_team", `{
		"team":{"name":"report-crew","members":[{"name":"alice","role":"coder"}]},
		"tasks":[{"title":"draft outline"},{"title":"write sections"}],
		"lead_start":true,"lead_goal":"ship the weekly report"}`)
	if err != nil {
		t.Fatalf("create_team: %v", err)
	}
	// teamEnsureJWT warms caches with other RPCs; assert the create pipeline
	// as an ordered subsequence.
	methods = snapshot()
	var pipeline []string
	for _, m := range methods {
		switch m {
		case "teams.register", "teams.add_task", "teams.lead_start":
			pipeline = append(pipeline, m)
		}
	}
	want := []string{"teams.register", "teams.add_task", "teams.add_task", "teams.lead_start"}
	if len(pipeline) != len(want) {
		t.Fatalf("pipeline = %v (all: %v), want %v", pipeline, methods, want)
	}
	for i := range want {
		if pipeline[i] != want[i] {
			t.Fatalf("pipeline[%d] = %s, want %s (all: %v)", i, pipeline[i], want[i], methods)
		}
	}
	if !strings.Contains(out, "team report-crew created") || !strings.Contains(out, "task-9") || !strings.Contains(out, "lead loop started") {
		t.Fatalf("result text missing pieces: %q", out)
	}
}

// TestCreateTeamToolRegisterRejected verifies a kernel register rejection
// surfaces as a tool error (fail visibly) with no task attempted.
func TestCreateTeamToolRegisterRejected(t *testing.T) {
	var addTask bool
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "teams.register" {
			return map[string]any{"status": "error", "message": "duplicate namespace_key"}, nil
		}
		if method == "teams.add_task" {
			addTask = true
		}
		return map[string]any{"status": "ok"}, nil
	})
	s := newTeamToolServer(t, sock)
	_, err := runTool(t, s, "create_team", `{"team":{"name":"dup","members":[{"name":"a"}]}}`)
	if err == nil || !strings.Contains(err.Error(), "duplicate namespace_key") {
		t.Fatalf("err = %v, want duplicate namespace_key", err)
	}
	if addTask {
		t.Fatal("add_task attempted after register rejection")
	}
}

// TestTeamSendTool verifies teams.send wire format and the delivered result.
func TestTeamSendTool(t *testing.T) {
	var got map[string]any
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "teams.send" {
			got = params
			return map[string]any{"status": "ok", "delivered": true, "msg_id": "msg_abc123"}, nil
		}
		return map[string]any{"status": "ok"}, nil
	})
	s := newTeamToolServer(t, sock)
	out, err := runTool(t, s, "team_send", `{"from":"console","to":"lead","body":"status?","mentions":["alice"]}`)
	if err != nil {
		t.Fatalf("team_send: %v", err)
	}
	if got["team"] != "alpha" || got["from"] != "console" || got["to"] != "lead" || got["body"] != "status?" || got["type"] != "text" {
		t.Fatalf("send params = %+v", got)
	}
	mentions, _ := got["mentions"].([]any)
	if len(mentions) != 1 || mentions[0] != "alice" {
		t.Fatalf("mentions = %v", mentions)
	}
	if !strings.Contains(out, "msg_abc123") || !strings.Contains(out, "team_inbox") {
		t.Fatalf("result = %q", out)
	}
}

// TestTeamSendToolMemberRequired verifies missing from/to/body fail before
// any RPC happens.
func TestTeamSendToolMemberRequired(t *testing.T) {
	called := false
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "teams.send" {
			called = true
		}
		return map[string]any{"status": "ok"}, nil
	})
	s := newTeamToolServer(t, sock)
	if _, err := runTool(t, s, "team_send", `{"to":"lead","body":"hi"}`); err == nil || !strings.Contains(err.Error(), "from and to") {
		t.Fatalf("err = %v, want from/to validation", err)
	}
	if called {
		t.Fatal("teams.send called despite missing sender")
	}
}

// TestTeamInboxTool verifies the inbox rendering: empty, single, reply chain.
func TestTeamInboxTool(t *testing.T) {
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		if method == "teams.inbox" {
			if params["member"] != "console" {
				t.Errorf("member = %v, want console", params["member"])
			}
			return map[string]any{"status": "ok", "messages": []any{
				map[string]any{"msg_id": "m1", "from": "lead", "body": "on it", "reply_to": "m0"},
				map[string]any{"msg_id": "m2", "from": "alice", "body": "draft done"},
			}}, nil
		}
		return map[string]any{"status": "ok"}, nil
	})
	s := newTeamToolServer(t, sock)
	out, err := runTool(t, s, "team_inbox", `{"member":"console"}`)
	if err != nil {
		t.Fatalf("team_inbox: %v", err)
	}
	if !strings.Contains(out, "2 message(s) for console on team alpha") ||
		!strings.Contains(out, "[m1] lead: on it (reply to m0)") ||
		!strings.Contains(out, "[m2] alice: draft done") {
		t.Fatalf("inbox render = %q", out)
	}

	// Empty inbox path.
	sock2 := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		return map[string]any{"status": "ok", "messages": []any{}}, nil
	})
	s2 := newTeamToolServer(t, sock2)
	out, err = runTool(t, s2, "team_inbox", `{"member":"console"}`)
	if err != nil {
		t.Fatalf("team_inbox empty: %v", err)
	}
	if !strings.Contains(out, "is empty") {
		t.Fatalf("empty render = %q", out)
	}
}

// TestTeamToolsDisabledWithoutIntegration verifies the fail-visible guard
// when the agent-studio integration is off.
func TestTeamToolsDisabledWithoutIntegration(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_APIKEY_TEST", "test-key")
	cfg := &config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}
	s := New(cfg, t.TempDir(), nil)
	for _, name := range []string{"create_team", "team_send", "team_inbox"} {
		if _, err := runTool(t, s, name, `{}`); err == nil || !strings.Contains(err.Error(), "not enabled") {
			t.Fatalf("%s err = %v, want not-enabled", name, err)
		}
	}
}
