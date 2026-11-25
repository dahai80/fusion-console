package server

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dahai80/fusion-console/internal/store"
)

// weeklyReportRange resolves the [start, end) window from start/end query
// dates (YYYY-MM-DD). Dates are calendar days in the server's local time
// zone — the console runs on the user's machine, so "this week" has to mean
// their week, not UTC's. Missing values default to the last 7 days including
// today.
func weeklyReportRange(startQ, endQ string) (time.Time, time.Time, error) {
	now := time.Now()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -7)
	if startQ != "" {
		t, err := time.ParseInLocation(time.DateOnly, startQ, time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start %q: use YYYY-MM-DD", startQ)
		}
		start = t
	}
	if endQ != "" {
		t, err := time.ParseInLocation(time.DateOnly, endQ, time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end %q: use YYYY-MM-DD", endQ)
		}
		end = t.AddDate(0, 0, 1) // end date is inclusive on the wire
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("start must be before end")
	}
	return start, end, nil
}

// handleWeeklyReport serves GET /api/report/weekly — the deterministic
// weekly work report (issue #114): dispatch activity per worker over a date
// range, optionally filtered to selected workers.
func (s *Server) handleWeeklyReport(w http.ResponseWriter, r *http.Request) {
	start, end, err := weeklyReportRange(r.URL.Query().Get("start"), r.URL.Query().Get("end"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	user := s.auth.User(r)
	reports, err := s.store.WeeklyReport(user, start, end, r.URL.Query()["worker"])
	if err != nil {
		log.Printf("server: weekly report: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "weekly report failed"})
		return
	}
	if reports == nil {
		reports = []store.WeeklyWorkerReport{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"start":      start.Format(time.DateOnly),
		"end":        end.AddDate(0, 0, -1).Format(time.DateOnly),
		"utc_offset": start.Format("-07:00"),
		"workers":    reports,
	})
}

// weeklyReportText renders the report as compact text for the planner tool.
func weeklyReportText(start, end time.Time, reports []store.WeeklyWorkerReport) string {
	header := fmt.Sprintf("Weekly report %s to %s (calendar days, UTC%s)",
		start.Format(time.DateOnly), end.AddDate(0, 0, -1).Format(time.DateOnly), start.Format("-07:00"))
	if len(reports) == 0 {
		return header + ": no dispatch activity."
	}
	var b strings.Builder
	b.WriteString(header)
	total := store.WeeklyWorkerReport{Statuses: map[string]int{}}
	for _, r := range reports {
		total.Runs += r.Runs
		total.TokensIn += r.TokensIn
		total.TokensOut += r.TokensOut
		total.CostUSD += r.CostUSD
		total.ElapsedS += r.ElapsedS
		for k, v := range r.Statuses {
			total.Statuses[k] += v
		}
	}
	line := func(name string, r store.WeeklyWorkerReport) {
		fmt.Fprintf(&b, "\n%s: %d runs (done %d, error %d, blocked %d, open %d), tokens_in %d, tokens_out %d, cost $%.4f, elapsed %ds",
			name, r.Runs, r.Statuses["done"], r.Statuses["error"], r.Statuses["blocked"], r.Statuses["open"],
			r.TokensIn, r.TokensOut, r.CostUSD, r.ElapsedS)
	}
	line("All workers", total)
	for _, r := range reports {
		line(r.Worker, r)
		if r.TasksOmitted > 0 {
			fmt.Fprintf(&b, "\n  (%d earlier runs not listed)", r.TasksOmitted)
		}
		for _, t := range r.Tasks {
			fmt.Fprintf(&b, "\n  - #%d %s[%s, %ds] %s", t.ID, weeklyTaskDay(t.StartedAt), t.Status, t.ElapsedS, t.Task)
			if t.Outcome != "" {
				b.WriteString(" => " + t.Outcome)
			}
		}
	}
	b.WriteString("\nRuns are background dispatches, including ones dismissed from the task rail. Tokens and cost are the usage booked to each worker in the window; that also covers foreground dispatches, which are not listed as runs.")
	return b.String()
}

// weeklyTaskDay renders a run's stored UTC start as a local "MM-DD " prefix.
func weeklyTaskDay(startedAt string) string {
	t, err := time.Parse(time.RFC3339, startedAt)
	if err != nil || t.Year() < 2000 {
		return ""
	}
	return t.Local().Format("01-02") + " "
}
