package server

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dahai80/fusion-console/internal/store"
)

// weeklyReportRange resolves the [start, end) window from start/end query
// dates (YYYY-MM-DD). Missing values default to the last 7 days including
// today, matching the usage dashboard's "week" range.
func weeklyReportRange(startQ, endQ string) (time.Time, time.Time, error) {
	end := time.Now().UTC().AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -7)
	if startQ != "" {
		t, err := time.ParseInLocation(time.DateOnly, startQ, time.UTC)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start %q: use YYYY-MM-DD", startQ)
		}
		start = t
	}
	if endQ != "" {
		t, err := time.ParseInLocation(time.DateOnly, endQ, time.UTC)
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
		"start":   start.Format(time.DateOnly),
		"end":     end.AddDate(0, 0, -1).Format(time.DateOnly),
		"workers": reports,
	})
}

// weeklyReportText renders the report as compact text for the planner tool.
func weeklyReportText(start, end time.Time, reports []store.WeeklyWorkerReport) string {
	header := fmt.Sprintf("Weekly report %s to %s", start.Format(time.DateOnly), end.AddDate(0, 0, -1).Format(time.DateOnly))
	if len(reports) == 0 {
		return header + ": no dispatch activity."
	}
	out := header
	for _, r := range reports {
		out += fmt.Sprintf("\n%s: %d runs (done %d, error %d, blocked %d, open %d), tokens_in %d, tokens_out %d, cost $%.4f, elapsed %ds",
			r.Worker, r.Runs, r.Statuses["done"], r.Statuses["error"], r.Statuses["blocked"], r.Statuses["open"],
			r.TokensIn, r.TokensOut, r.CostUSD, r.ElapsedS)
		for _, t := range r.Tasks {
			out += fmt.Sprintf("\n  - #%d [%s] %s", t.ID, t.Status, t.Task)
		}
	}
	return out
}
