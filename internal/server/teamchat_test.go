package server

import (
	"strings"
	"testing"

	"github.com/dahai80/fusion-console/internal/store"

	"github.com/dahai80/fusion-console/internal/config"
)

// TestTaskTitleFromBrief pins the title derivation: first line, 80-char cap,
// empty fallback.
func TestTaskTitleFromBrief(t *testing.T) {
	if got := taskTitleFromBrief("fix the login bug\n\nfull brief here"); got != "fix the login bug" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("x", 120)
	if got := taskTitleFromBrief(long); len(got) != 80 {
		t.Fatalf("cap: len = %d", len(got))
	}
	if got := taskTitleFromBrief(""); !strings.HasPrefix(got, "task ") {
		t.Fatalf("empty fallback = %q", got)
	}
}

// TestTeamChatTurnAndPoll runs the full group-chat loop against a fake
// daemon: send assigns a task, poll returns inbox replies deduped by msg_id.
func TestTeamChatTurnAndPoll(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_APIKEY_TEST", "test-key")
	var addTaskParams map[string]any
	sock := fakeStudioFunc(t, func(method string, params map[string]any) (map[string]any, error) {
		switch method {
		case "team.roster_register":
			return map[string]any{"status": "ok"}, nil
		case "teams.add_task":
			addTaskParams = params
			return map[string]any{"status": "ok", "task_id": "task-7"}, nil
		case "teams.lead_status":
			return map[string]any{"status": "ok", "running": false}, nil
		case "teams.inbox":
			return map[string]any{"status": "ok", "messages": []any{
				map[string]any{"msg_id": "m1", "from": "lead", "body": "picked it up"},
			}}, nil
		}
		return map[string]any{"status": "ok"}, nil
	})
	t.Setenv("FUSION_STUDIO_SOCKET", sock)
	dir := t.TempDir()
	st, err := store.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	cfg := &config.Config{Server: config.ServerConfig{DataDir: dir}}
	cfg.AgentStudio.Enabled = true
	cfg.AgentStudio.APIKeyID = "TEST"
	s := New(cfg, dir, st)
	uid, err := s.store.CreateConversation("tester", "team: alpha", "team", "alpha", "")
	if err != nil {
		t.Fatalf("create conv: %v", err)
	}
	conv, err := s.store.Conversation("tester", uid)
	if err != nil {
		t.Fatalf("conv: %v", err)
	}
	taskID, hint, err := s.teamChatAssign(conv, "fix the login bug\nbrief body")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if taskID != "task-7" {
		t.Fatalf("task id = %q", taskID)
	}
	if addTaskParams["team"] != "alpha" || addTaskParams["title"] != "fix the login bug" {
		t.Fatalf("add_task params = %+v", addTaskParams)
	}
	if !strings.Contains(hint, "Lead loop is not running") {
		t.Fatalf("hint = %q", hint)
	}
	// Poll: first pull returns the inbox message; second pull is a no-op
	// (non-draining inbox, dedupe by msg_id).
	first, err := s.teamChatPullMessages("tester", uid)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(first) != 1 || !strings.Contains(first[0]["content"].(string), "lead: picked it up") {
		t.Fatalf("first poll = %v", first)
	}
	second, err := s.teamChatPullMessages("tester", uid)
	if err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second poll should dedupe to 0, got %v", second)
	}
}
