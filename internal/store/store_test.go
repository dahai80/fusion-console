package store

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestConversationAndMessages(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateConversation("admin", "first", "chat", "", "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if _, err := s.AppendMessage(id, "user", "hi", "{}"); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := s.AppendMessage(id, "assistant", "hello", `{"model":"glm-5"}`); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	msgs, err := s.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Content != "hello" {
		t.Fatalf("messages wrong: %+v", msgs)
	}
	convs, err := s.ListConversations("admin")
	if err != nil || len(convs) != 1 || convs[0].Title != "first" {
		t.Fatalf("conversations wrong: %+v err=%v", convs, err)
	}
	if _, err := s.ListConversations("other"); err != nil || len(convs) == 0 {
		t.Fatalf("user isolation broken")
	}
}

func TestCostReport(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateConversation("admin", "c", "agent", "claude", "")
	if err := s.LogCost(id, "admin", "agent:claude", 0.1234, 100, 50); err != nil {
		t.Fatalf("LogCost: %v", err)
	}
	if err := s.LogCost(id, "admin", "agent:claude", 0.2, 10, 5); err != nil {
		t.Fatalf("LogCost: %v", err)
	}
	rep, err := s.CostReport("admin")
	if err != nil {
		t.Fatalf("CostReport: %v", err)
	}
	if len(rep) != 1 {
		t.Fatalf("want 1 row, got %d", len(rep))
	}
	if rep[0]["calls"].(int) != 2 {
		t.Errorf("calls = %v", rep[0]["calls"])
	}
}
