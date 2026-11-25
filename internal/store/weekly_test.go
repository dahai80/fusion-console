package store

import (
	"testing"
	"time"
)

func TestWeeklyReport(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	mk := func(user, worker, task, status string, started time.Time, cost float64, tin, tout int) {
		row := &DispatchRunRow{ConvID: 1, User: user, Worker: worker, Agent: "claude",
			Task: task, Status: status, StartedAt: started.Format(time.RFC3339),
			CostUSD: cost, TokensIn: tin, TokensOut: tout, ElapsedS: 30}
		if _, err := s.CreateDispatchRun(row); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	mk("admin", "claude", "fix issue #1", "done", now.AddDate(0, 0, -2), 0.5, 100, 200)
	mk("admin", "claude", "fix issue #2", "error", now.AddDate(0, 0, -1), 0.25, 50, 60)
	mk("admin", "codex", "write docs", "done", now.AddDate(0, 0, -3), 0.1, 10, 20)
	// Outside the window and another user's run: must be excluded.
	mk("admin", "claude", "old task", "done", now.AddDate(0, 0, -30), 9, 1, 1)
	mk("ops", "claude", "someone else", "done", now.AddDate(0, 0, -1), 9, 1, 1)

	reports, err := s.WeeklyReport("admin", now.AddDate(0, 0, -6), now.AddDate(0, 0, 1), nil)
	if err != nil {
		t.Fatalf("weekly report: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("want 2 workers, got %d: %+v", len(reports), reports)
	}
	var claude, codex *WeeklyWorkerReport
	for i := range reports {
		switch reports[i].Worker {
		case "claude":
			claude = &reports[i]
		case "codex":
			codex = &reports[i]
		}
	}
	if claude == nil || codex == nil {
		t.Fatalf("missing worker report: %+v", reports)
	}
	if claude.Runs != 2 || claude.Statuses["done"] != 1 || claude.Statuses["error"] != 1 {
		t.Fatalf("claude aggregation wrong: %+v", claude)
	}
	if claude.TokensIn != 150 || claude.TokensOut != 260 || claude.CostUSD != 0.75 || claude.ElapsedS != 60 {
		t.Fatalf("claude totals wrong: %+v", claude)
	}
	if len(claude.Tasks) != 2 || claude.Tasks[0].Task != "fix issue #1" {
		t.Fatalf("claude tasks wrong: %+v", claude.Tasks)
	}
	if codex.Runs != 1 {
		t.Fatalf("codex aggregation wrong: %+v", codex)
	}

	// Worker filter: only the selected worker appears (issue #114).
	filtered, err := s.WeeklyReport("admin", now.AddDate(0, 0, -6), now.AddDate(0, 0, 1), []string{"codex"})
	if err != nil {
		t.Fatalf("filtered report: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Worker != "codex" {
		t.Fatalf("worker filter wrong: %+v", filtered)
	}

	// Empty range: no rows, no error.
	empty, err := s.WeeklyReport("admin", now.AddDate(0, 0, -6), now.AddDate(0, 0, -5), nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty window: %v %+v", err, empty)
	}
}
