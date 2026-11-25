package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dahai80/fusion-console/internal/agent"
	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/store"
)

func TestWeeklyReportRange(t *testing.T) {
	// The window is calendar days in the server's zone, not UTC: with the
	// host at UTC+8 a run started 07:00 local on the 25th (23:00Z on the
	// 24th) belongs to the 25th.
	prev := time.Local
	time.Local = time.FixedZone("UTC+8", 8*3600)
	defer func() { time.Local = prev }()

	start, end, err := weeklyReportRange("", "")
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	now := time.Now().In(time.Local)
	if end.Sub(start) != 7*24*time.Hour || start.Hour() != 0 || end.Location() != time.Local ||
		!end.After(now) || end.Sub(now) > 24*time.Hour {
		t.Fatalf("default window must be the last 7 local days through today: %v..%v", start, end)
	}
	// End date is inclusive on the wire: end=day X becomes < X+1.
	start2, end2, err := weeklyReportRange("2026-11-16", "2026-11-22")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if start2.Format(time.RFC3339) != "2026-11-16T00:00:00+08:00" || end2.Format(time.RFC3339) != "2026-11-23T00:00:00+08:00" {
		t.Fatalf("window wrong: %v..%v", start2, end2)
	}
	if got := start2.UTC().Format(time.RFC3339); got != "2026-11-15T16:00:00Z" {
		t.Fatalf("local midnight must reach the store as %s, want 2026-11-15T16:00:00Z", got)
	}
	if _, _, err := weeklyReportRange("2026-11-22", "2026-11-16"); err == nil {
		t.Fatal("inverted window accepted")
	}
	if _, _, err := weeklyReportRange("Nov 16", ""); err == nil {
		t.Fatal("bad date accepted")
	}
}

func TestWeeklyReportHandler(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	row := &store.DispatchRunRow{ConvID: 1, User: "admin", Worker: "claude", Agent: "claude",
		Task: "fix issue", Status: "done", StartedAt: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), ElapsedS: 15}
	if _, err := st.CreateDispatchRun(row); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := st.LogCost(1, "admin", "agent:claude", 0.25, 10, 20); err != nil {
		t.Fatalf("log cost: %v", err)
	}

	s := New(&config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, t.TempDir(), st)
	cookie := loginCookie(t, s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/report/weekly?worker=claude", nil)
	req.AddCookie(cookie)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Start     string `json:"start"`
		End       string `json:"end"`
		UTCOffset string `json:"utc_offset"`
		Workers   []struct {
			Worker   string `json:"worker"`
			Runs     int    `json:"runs"`
			TokensIn int64  `json:"tokens_in"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Workers) != 1 || body.Workers[0].Worker != "claude" || body.Workers[0].Runs != 1 || body.Workers[0].TokensIn != 10 {
		t.Fatalf("body wrong: %s", rec.Body.String())
	}
	if body.End != time.Now().Format(time.DateOnly) || body.UTCOffset != time.Now().Format("-07:00") {
		t.Fatalf("window must be reported in local time: %s", rec.Body.String())
	}

	// Unfiltered: same run still appears once; bad dates fail loudly.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/report/weekly", nil)
	req.AddCookie(cookie)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "claude") {
		t.Fatalf("unfiltered report wrong: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/report/weekly?start=bad", nil)
	req.AddCookie(cookie)
	s.mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("bad date status = %d, want 400", rec.Code)
	}
}

func TestWeeklyReportText(t *testing.T) {
	prev := time.Local
	time.Local = time.FixedZone("UTC+8", 8*3600)
	defer func() { time.Local = prev }()
	start := time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 7)
	out := weeklyReportText(start, end, nil)
	if !strings.Contains(out, "no dispatch activity") {
		t.Fatalf("empty text wrong: %q", out)
	}
	out = weeklyReportText(start, end, []store.WeeklyWorkerReport{
		{Worker: "claude", Runs: 52, TokensIn: 100, TokensOut: 20, CostUSD: 1.5, ElapsedS: 60, TasksOmitted: 51,
			Statuses: map[string]int{"done": 51, "error": 1, "blocked": 0, "open": 0},
			Tasks: []store.WeeklyTaskSummary{{ID: 7, Task: "fix issue", Status: "done", ElapsedS: 40,
				StartedAt: "2026-11-17T23:30:00Z", Outcome: "PR opened"}}},
		{Worker: "codex", TokensIn: 5, Statuses: map[string]int{}},
	})
	for _, want := range []string{
		"2026-11-16 to 2026-11-22 (calendar days, UTC+08:00)",
		"All workers: 52 runs (done 51, error 1, blocked 0, open 0), tokens_in 105, tokens_out 20, cost $1.5000",
		"claude: 52 runs",
		"(51 earlier runs not listed)",
		"#7 11-18 [done, 40s] fix issue => PR opened", // 23:30Z is the 18th at UTC+8
		"codex: 0 runs",
		"foreground dispatches",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("text missing %q:\n%s", want, out)
		}
	}
}

// A background run used to keep cost and tokens at zero on its own row.
func TestBackgroundRunRecordsUsage(t *testing.T) {
	t.Setenv("FUSION_CONSOLE_DIR", t.TempDir())
	st, err := store.Open(t.TempDir() + "/usage.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{Orchestrator: config.OrchestratorConfig{Mode: "off", WorkerMemory: "off", Workers: []config.WorkerConfig{
		{Name: "worker", Agent: "fixture", Enabled: true, Isolation: "standalone", WorkDir: t.TempDir()}}}}
	s := New(cfg, t.TempDir(), st)
	s.agents["fixture"] = &dispatchTestAgent{starts: make(chan agent.RunOpts, 1), events: []agent.Event{
		{Type: agent.EventUsage, CostUSD: 0.5, TokensIn: 120, TokensOut: 30},
		{Type: agent.EventResult, Text: "all done"}}}
	convID, err := st.CreateConversation("admin", "usage", "chat", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cli, wk := s.resolveWorkerTarget("worker")
	if _, err := s.enqueueBackgroundDispatch(convID, "admin", dispatchArgs{Agent: "worker", Task: "do it", Background: true}, wk, cli, s.agentSettings(cli), 0); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := st.ListDispatchRuns(10)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].Status == "done" {
			if rows[0].CostUSD != 0.5 || rows[0].TokensIn != 120 || rows[0].TokensOut != 30 {
				t.Fatalf("usage not stored on the run row: %+v", rows[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %+v", rows)
		}
		time.Sleep(5 * time.Millisecond)
	}
	reports, err := st.WeeklyReport("admin", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), nil)
	if err != nil || len(reports) != 1 || reports[0].Runs != 1 || reports[0].TokensIn != 120 || reports[0].Tasks[0].CostUSD != 0.5 {
		t.Fatalf("weekly report after a real run: %+v err %v", reports, err)
	}
}
