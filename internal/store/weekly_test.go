package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func weeklyRun(t *testing.T, s *Store, user, worker, task, status string, started time.Time) int64 {
	t.Helper()
	id, err := s.CreateDispatchRun(&DispatchRunRow{ConvID: 1, User: user, Worker: worker, Agent: "claude",
		Task: task, Status: status, StartedAt: started.UTC().Format(time.RFC3339), ElapsedS: 30,
		Result: "result of " + task, Error: "error of " + task, Detail: "detail of " + task})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return id
}

func weeklyCost(t *testing.T, s *Store, user, source string, at time.Time, cost float64, tin, tout int) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO cost_log (conv_id, user, source, cost_usd, tokens_in, tokens_out, created_at) VALUES (1,?,?,?,?,?,?)`,
		user, source, cost, tin, tout, at.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("cost row: %v", err)
	}
}

func weeklyFind(t *testing.T, reports []WeeklyWorkerReport, worker string) WeeklyWorkerReport {
	t.Helper()
	for _, r := range reports {
		if r.Worker == worker {
			return r
		}
	}
	t.Fatalf("worker %q missing: %+v", worker, reports)
	return WeeklyWorkerReport{}
}

func TestWeeklyReport(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	weeklyRun(t, s, "admin", "claude", "fix issue #1", "done", now.AddDate(0, 0, -2))
	weeklyRun(t, s, "admin", "claude", "fix issue #2", "error", now.AddDate(0, 0, -1))
	weeklyRun(t, s, "admin", "codex", "write docs", "done", now.AddDate(0, 0, -3))
	// Outside the window and another user's run: must be excluded.
	weeklyRun(t, s, "admin", "claude", "old task", "done", now.AddDate(0, 0, -30))
	weeklyRun(t, s, "ops", "claude", "someone else", "done", now.AddDate(0, 0, -1))

	// Usage is booked in cost_log, never on the run rows above.
	weeklyCost(t, s, "admin", "agent:claude", now.AddDate(0, 0, -2), 0.5, 100, 200)
	weeklyCost(t, s, "admin", "agent:claude", now.AddDate(0, 0, -1), 0.25, 50, 60)
	weeklyCost(t, s, "admin", "agent:codex", now.AddDate(0, 0, -3), 0.1, 10, 20)
	weeklyCost(t, s, "admin", "agent:opencode", now.AddDate(0, 0, -1), 0.3, 7, 8) // foreground only
	weeklyCost(t, s, "admin", "agent:claude", now.AddDate(0, 0, -30), 9, 1, 1)    // out of window
	weeklyCost(t, s, "ops", "agent:claude", now.AddDate(0, 0, -1), 9, 1, 1)       // other user
	weeklyCost(t, s, "admin", "chat:gw", now.AddDate(0, 0, -1), 9, 1, 1)          // planner, not a worker

	start, end := now.AddDate(0, 0, -6), now.AddDate(0, 0, 1)
	reports, err := s.WeeklyReport("admin", start, end, nil)
	if err != nil {
		t.Fatalf("weekly report: %v", err)
	}
	if len(reports) != 3 || reports[0].Worker != "claude" || reports[1].Worker != "codex" || reports[2].Worker != "opencode" {
		t.Fatalf("want claude, codex, opencode in order, got %+v", reports)
	}
	claude := weeklyFind(t, reports, "claude")
	if claude.Runs != 2 || claude.Statuses["done"] != 1 || claude.Statuses["error"] != 1 || claude.ElapsedS != 60 {
		t.Fatalf("claude aggregation wrong: %+v", claude)
	}
	if claude.TokensIn != 150 || claude.TokensOut != 260 || claude.CostUSD != 0.75 {
		t.Fatalf("claude usage must come from cost_log: %+v", claude)
	}
	if len(claude.Tasks) != 2 || claude.TasksOmitted != 0 || claude.Tasks[0].Task != "fix issue #1" {
		t.Fatalf("claude tasks wrong: %+v", claude.Tasks)
	}
	if claude.Tasks[0].Outcome != "result of fix issue #1" || claude.Tasks[1].Outcome != "error of fix issue #2" {
		t.Fatalf("outcome must follow the status: %+v", claude.Tasks)
	}
	// A worker that only ran in the foreground has usage but no run rows.
	fg := weeklyFind(t, reports, "opencode")
	if fg.Runs != 0 || fg.TokensIn != 7 || fg.Tasks == nil || len(fg.Tasks) != 0 {
		t.Fatalf("usage-only worker wrong: %+v", fg)
	}

	// Worker filter: only the selected worker appears (issue #114).
	filtered, err := s.WeeklyReport("admin", start, end, []string{"codex"})
	if err != nil {
		t.Fatalf("filtered report: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Worker != "codex" || filtered[0].TokensIn != 10 {
		t.Fatalf("worker filter wrong: %+v", filtered)
	}

	// Empty range: no rows, no error.
	empty, err := s.WeeklyReport("admin", now.AddDate(0, 0, -20), now.AddDate(0, 0, -19), nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty window: %v %+v", err, empty)
	}
}

// Dismissing or clearing cards on the task rail must not erase the work from
// the weekly report.
func TestWeeklyReportKeepsDismissedRuns(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	first := weeklyRun(t, s, "admin", "claude", "one", "done", now.Add(-3*time.Hour))
	weeklyRun(t, s, "admin", "claude", "two", "error", now.Add(-2*time.Hour))
	weeklyRun(t, s, "admin", "claude", "three", "running", now.Add(-time.Hour))
	if err := s.DismissDispatchRun(first); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if err := s.ClearFinishedDispatchRuns(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	rail, err := s.ListDispatchRuns(10)
	if err != nil || len(rail) != 1 || rail[0].Task != "three" {
		t.Fatalf("rail must keep only the running run: %+v err %v", rail, err)
	}
	reports, err := s.WeeklyReport("admin", now.AddDate(0, 0, -1), now.AddDate(0, 0, 1), nil)
	if err != nil {
		t.Fatalf("weekly report: %v", err)
	}
	r := weeklyFind(t, reports, "claude")
	if r.Runs != 3 || r.Statuses["done"] != 1 || r.Statuses["error"] != 1 || r.Statuses["open"] != 1 || len(r.Tasks) != 3 {
		t.Fatalf("dismissed runs dropped from the report: %+v", r)
	}
}

// Each worker keeps its newest runs; a long prompt is folded to one line.
func TestWeeklyReportTaskCapAndExcerpt(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	var firstBusy int64
	for i := 0; i < maxWeeklyTasksPerWorker+3; i++ {
		id := weeklyRun(t, s, "admin", "busy", fmt.Sprintf("task %d", i), "done", now.Add(-time.Hour))
		if i == 0 {
			firstBusy = id
		}
	}
	weeklyRun(t, s, "admin", "quiet", "line one\n\nline two "+strings.Repeat("字", 400), "done", now.Add(-time.Hour))
	reports, err := s.WeeklyReport("admin", now.AddDate(0, 0, -1), now.AddDate(0, 0, 1), nil)
	if err != nil {
		t.Fatalf("weekly report: %v", err)
	}
	busy := weeklyFind(t, reports, "busy")
	if busy.Runs != maxWeeklyTasksPerWorker+3 || len(busy.Tasks) != maxWeeklyTasksPerWorker || busy.TasksOmitted != 3 {
		t.Fatalf("cap wrong: runs %d tasks %d omitted %d", busy.Runs, len(busy.Tasks), busy.TasksOmitted)
	}
	if busy.Tasks[0].ID != firstBusy+3 || busy.Tasks[0].ID > busy.Tasks[1].ID {
		t.Fatalf("cap must keep the newest runs, oldest first: first id %d", busy.Tasks[0].ID)
	}
	quiet := weeklyFind(t, reports, "quiet")
	task := quiet.Tasks[0].Task
	if len(quiet.Tasks) != 1 || strings.Contains(task, "\n") || !strings.HasPrefix(task, "line one line two ") ||
		len([]rune(task)) != weeklyExcerptRunes+1 || !strings.HasSuffix(task, "…") {
		t.Fatalf("excerpt wrong (%d runes): %q", len([]rune(task)), task)
	}
}
