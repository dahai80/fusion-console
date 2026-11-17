package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
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

func TestUsageDashboard(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateConversation("admin", "c", "chat", "", "")
	if err := s.LogCost(id, "admin", "chat:gw", 0.02, 1000, 500); err != nil {
		t.Fatalf("LogCost brain: %v", err)
	}
	if err := s.LogCost(id, "admin", "agent:claude", 0.10, 4000, 2000); err != nil {
		t.Fatalf("LogCost worker: %v", err)
	}
	for _, r := range []string{"today", "week", "month", "all"} {
		d, err := s.UsageDashboard("admin", r)
		if err != nil {
			t.Fatalf("UsageDashboard(%s): %v", r, err)
		}
		tot := d["totals"].(map[string]any)
		if tot["brain_tokens"].(int64) != 1500 || tot["worker_tokens"].(int64) != 6000 {
			t.Errorf("%s totals = %v", r, tot)
		}
		rawB, _ := json.Marshal(d["buckets"])
		var bk []map[string]any
		if err := json.Unmarshal(rawB, &bk); err != nil || len(bk) == 0 {
			t.Errorf("%s buckets empty: %s", r, rawB)
		}
	}
	if _, err := s.UsageDashboard("admin", "bogus"); err == nil {
		t.Error("bogus range should error")
	}
	// user isolation
	other, _ := s.UsageDashboard("other", "all")
	rawOther, _ := json.Marshal(other["buckets"])
	if string(rawOther) != "[]" {
		t.Errorf("user isolation broken: %s", rawOther)
	}
}

func TestDeleteAndRenameConversation(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateConversation("admin", "old", "chat", "", "")
	if _, err := s.AppendMessage(id, "user", "hi", "{}"); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.RenameConversation("admin", id, "new"); err != nil {
		t.Fatalf("RenameConversation: %v", err)
	}
	convs, _ := s.ListConversations("admin")
	if convs[0].Title != "new" {
		t.Errorf("title = %s", convs[0].Title)
	}
	if err := s.RenameConversation("other", id, "x"); err == nil {
		t.Error("cross-user rename allowed")
	}
	if err := s.DeleteConversation("admin", id); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if msgs, _ := s.ListMessages(id); len(msgs) != 0 {
		t.Error("messages not cascade-deleted")
	}
	if err := s.DeleteConversation("other", id); err == nil {
		t.Error("cross-user delete allowed")
	}
}

func TestDispatchRunPersistence(t *testing.T) {
	s := openTestStore(t)
	row := &DispatchRunRow{ConvID: 1, User: "admin", Worker: "fusion-site", Agent: "claude",
		Task: "fix issue", Status: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	id, err := s.CreateDispatchRun(row)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	row.ID = id
	row.Status, row.Result, row.Read, row.ElapsedS = "done", "all good", true, 42
	if err := s.SaveDispatchRun(row); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.ListDispatchRuns(10)
	if err != nil || len(got) != 1 {
		t.Fatalf("list: %v %d", err, len(got))
	}
	if got[0].Status != "done" || got[0].Result != "all good" || !got[0].Read || got[0].ElapsedS != 42 {
		t.Fatalf("round trip mismatch: %+v", got[0])
	}
	if err := s.AckDispatchRuns(1); err != nil {
		t.Fatalf("ack: %v", err)
	}
}

func TestConversationModelMigrationIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, err := s.CreateConversation("admin", "bound", "chat", "", "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := s.SetConversationModel("admin", id, "p-direct", "glm-5"); err != nil {
		t.Fatalf("SetConversationModel: %v", err)
	}
	if err := s.SetConversationModel("admin", id, "p-direct", "glm-5"); err != nil {
		t.Fatalf("SetConversationModel repeat: %v", err)
	}
	s.Close()

	// Restart on the same file: migration must be a no-op, binding survives.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	conv, err := s2.Conversation("admin", id)
	if err != nil {
		t.Fatalf("Conversation: %v", err)
	}
	if conv.ProviderID != "p-direct" || conv.Model != "glm-5" {
		t.Fatalf("binding not persisted: %+v", conv)
	}
	convs, err := s2.ListConversations("admin")
	if err != nil || len(convs) != 1 || convs[0].ProviderID != "p-direct" || convs[0].Model != "glm-5" {
		t.Fatalf("list missing binding: %+v err=%v", convs, err)
	}
}

func TestConversationModelOwnership(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateConversation("admin", "mine", "chat", "", "")
	if err := s.SetConversationModel("other", id, "p", "m"); err == nil {
		t.Fatal("other user may set binding")
	}
	if err := s.SetConversationModel("admin", 99999, "p", "m"); err == nil {
		t.Fatal("unknown conversation accepted")
	}
	if err := s.SetConversationModel("admin", id, "", ""); err != nil {
		t.Fatalf("clearing binding failed: %v", err)
	}
	conv, err := s.Conversation("admin", id)
	if err != nil || conv.ProviderID != "" || conv.Model != "" {
		t.Fatalf("binding not cleared: %+v err=%v", conv, err)
	}
}
