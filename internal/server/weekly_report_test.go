package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dahai80/fusion-console/internal/config"
	"github.com/dahai80/fusion-console/internal/store"
)

func TestWeeklyReportRange(t *testing.T) {
	start, end, err := weeklyReportRange("", "")
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if !start.Before(end) {
		t.Fatalf("default window empty: %v..%v", start, end)
	}
	// End date is inclusive on the wire: end=day X becomes < X+1.
	start2, end2, err := weeklyReportRange("2026-11-16", "2026-11-22")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if start2.Format(time.DateOnly) != "2026-11-16" ||
		end2.Format(time.DateOnly) != "2026-11-23" {
		t.Fatalf("window wrong: %v..%v", start2, end2)
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
	now := time.Now().UTC()
	row := &store.DispatchRunRow{ConvID: 1, User: "admin", Worker: "claude", Agent: "claude",
		Task: "fix issue", Status: "done", StartedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
		CostUSD: 0.25, TokensIn: 10, TokensOut: 20, ElapsedS: 15}
	if _, err := st.CreateDispatchRun(row); err != nil {
		t.Fatalf("create run: %v", err)
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
		Start   string `json:"start"`
		End     string `json:"end"`
		Workers []struct {
			Worker string `json:"worker"`
			Runs   int    `json:"runs"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Workers) != 1 || body.Workers[0].Worker != "claude" || body.Workers[0].Runs != 1 {
		t.Fatalf("body wrong: %s", rec.Body.String())
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
	start := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	out := weeklyReportText(start, end, nil)
	if !strings.Contains(out, "no dispatch activity") {
		t.Fatalf("empty text wrong: %q", out)
	}
	out = weeklyReportText(start, end, []store.WeeklyWorkerReport{{
		Worker: "claude", Runs: 2,
		Statuses: map[string]int{"done": 1, "error": 1, "blocked": 0, "open": 0},
		Tasks:    []store.WeeklyTaskSummary{{ID: 7, Task: "fix issue", Status: "done"}},
	}})
	for _, want := range []string{"2026-11-16", "2026-11-22", "claude: 2 runs", "#7 [done] fix issue"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text missing %q: %s", want, out)
		}
	}
}
