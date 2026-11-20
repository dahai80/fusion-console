package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestUsageSeriesLocalBuckets(t *testing.T) {
	s := openTestStore(t)
	insert := func(user, source, at string, in, out int) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO cost_log (conv_id, user, source, cost_usd, tokens_in, tokens_out, created_at) VALUES (0,?,?,0,?,?,?)`,
			user, source, in, out, at); err != nil {
			t.Fatal(err)
		}
	}
	// Taipei (UTC+8). "now" is 2026-11-20 15:00 local = 07:00Z.
	now := time.Date(2026, 11, 20, 7, 0, 0, 0, time.UTC)
	insert("admin", "chat:gw", "2026-11-19T17:30:00Z", 100, 50)     // local 11-20 01:xx → today hour 1
	insert("admin", "agent:coder", "2026-11-20T02:10:00Z", 1000, 0) // local 11-20 10:xx → today hour 10
	insert("admin", "agent:coder", "2026-11-19T03:00:00Z", 10, 10)  // local 11-19 11:xx → yesterday hour 11
	insert("admin", "agent:review", "2026-11-19T15:59:59Z", 5, 0)   // local 11-19 23:xx → yesterday hour 23
	insert("mcp-brain", "mcp:brain", "2026-11-18T00:00:00Z", 7, 0)  // shared MCP brain row → brain
	insert("other", "chat:gw", "2026-11-20T01:00:00Z", 999999, 0)   // another user: excluded
	insert("admin", "chat:gw", "2025-01-01T00:00:00Z", 3, 0)        // outside the window, sets first_day
	d, err := s.usageSeriesAt(now, "admin", 480, "idle-worker")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(d)
	var got struct {
		Today    string `json:"today"`
		Start    string `json:"start"`
		FirstDay string `json:"first_day"`
		Series   []struct {
			Name  string `json:"name"`
			Role  string `json:"role"`
			Total int64  `json:"total"`
		} `json:"series"`
		Days []struct {
			Day    string  `json:"day"`
			Total  int64   `json:"total"`
			Values []int64 `json:"values"`
		} `json:"days"`
		Hours struct{ Today, Yesterday []int64 } `json:"hours"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Today != "2026-11-20" || got.FirstDay != "2025-01-01" || got.Start != "2025-11-15" {
		t.Fatalf("today/start/first_day = %s %s %s", got.Today, got.Start, got.FirstDay)
	}
	names := []string{}
	for _, s := range got.Series {
		names = append(names, s.Name)
	}
	// Brain first, then workers by total (coder 1020 > review 5 > idle-worker 0).
	if len(names) != 4 || names[0] != "Brain" || names[1] != "coder" || names[2] != "review" || names[3] != "idle-worker" {
		t.Fatalf("series order = %v", names)
	}
	if got.Series[0].Total != 157 || got.Series[1].Total != 1020 {
		t.Fatalf("series totals = %+v", got.Series)
	}
	if got.Hours.Today[1] != 150 || got.Hours.Today[10] != 1000 || got.Hours.Yesterday[11] != 20 || got.Hours.Yesterday[23] != 5 {
		t.Fatalf("hours today=%v yesterday=%v", got.Hours.Today, got.Hours.Yesterday)
	}
	want := map[string][]int64{"2026-11-18": {7, 0, 0, 0}, "2026-11-19": {0, 20, 5, 0}, "2026-11-20": {150, 1000, 0, 0}}
	if len(got.Days) != len(want) {
		t.Fatalf("days = %+v", got.Days)
	}
	for _, day := range got.Days {
		w := want[day.Day]
		for i := range w {
			if day.Values[i] != w[i] {
				t.Fatalf("%s values = %v, want %v", day.Day, day.Values, w)
			}
		}
	}
	// UTC viewer: the 17:30Z row falls on 11-19.
	d0, _ := s.usageSeriesAt(now, "admin", 0)
	raw0, _ := json.Marshal(d0["hours"])
	var h0 struct{ Today, Yesterday []int64 }
	_ = json.Unmarshal(raw0, &h0)
	if h0.Yesterday[17] != 150 || h0.Today[2] != 1000 {
		t.Fatalf("utc hours = %s", raw0)
	}
	if _, err := s.usageSeriesAt(now, "admin", 15*60); err == nil {
		t.Fatal("out-of-range tz accepted")
	}
}
