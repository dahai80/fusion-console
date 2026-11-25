package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// WeeklyWorkerReport aggregates one worker's dispatch activity.
type WeeklyWorkerReport struct {
	Worker    string         `json:"worker"`
	Runs      int            `json:"runs"`
	Statuses  map[string]int `json:"statuses"`
	TokensIn  int64          `json:"tokens_in"`
	TokensOut int64          `json:"tokens_out"`
	CostUSD   float64        `json:"cost_usd"`
	ElapsedS  int64          `json:"elapsed_s"`
	// Tasks lists the newest runs, oldest first; TasksOmitted counts the
	// earlier runs the per-worker cap left out.
	Tasks        []WeeklyTaskSummary `json:"tasks"`
	TasksOmitted int                 `json:"tasks_omitted"`
}

// WeeklyTaskSummary is one dispatch line in the weekly report. Task and
// Outcome are single-line excerpts, not the full prompt and result.
type WeeklyTaskSummary struct {
	ID        int64   `json:"id"`
	Task      string  `json:"task"`
	Status    string  `json:"status"`
	StartedAt string  `json:"started_at"`
	ElapsedS  int     `json:"elapsed_s"`
	CostUSD   float64 `json:"cost_usd"`
	Outcome   string  `json:"outcome"`
}

const (
	// maxWeeklyTasksPerWorker bounds each worker's task list so one busy
	// worker cannot crowd the others out of the report.
	maxWeeklyTasksPerWorker = 50
	weeklyExcerptRunes      = 300
)

// WeeklyReport aggregates one user's dispatch activity over [start, end).
// Run counts, outcomes and elapsed time come from dispatch_runs (dismissed
// runs included). Tokens and cost come from cost_log, the accounting ledger:
// it is what every dispatch books under "agent:<worker>", so it also covers
// foreground dispatches and runs whose own row predates per-run accounting.
// A worker with booked usage but no background run in the window still gets
// a row. The workers filter is optional: empty means every worker.
func (s *Store) WeeklyReport(user string, start, end time.Time, workers []string) ([]WeeklyWorkerReport, error) {
	from, to := start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)
	query := `SELECT worker, COUNT(*),
	        SUM(CASE WHEN status='done' THEN 1 ELSE 0 END),
	        SUM(CASE WHEN status='error' THEN 1 ELSE 0 END),
	        SUM(CASE WHEN status='blocked' THEN 1 ELSE 0 END),
	        SUM(CASE WHEN status NOT IN ('done','error','blocked') THEN 1 ELSE 0 END),
	        SUM(elapsed_s)
	      FROM dispatch_runs
	      WHERE user = ? AND started_at >= ? AND started_at < ?`
	args := []any{user, from, to}
	if len(workers) > 0 {
		query += fmt.Sprintf(" AND worker IN (%s)", placeholders(len(workers)))
		for _, w := range workers {
			args = append(args, w)
		}
	}
	query += " GROUP BY worker"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: weekly report: %w", err)
	}
	defer rows.Close()
	byWorker := map[string]*WeeklyWorkerReport{}
	get := func(worker string) *WeeklyWorkerReport {
		r := byWorker[worker]
		if r == nil {
			r = &WeeklyWorkerReport{Worker: worker, Tasks: []WeeklyTaskSummary{},
				Statuses: map[string]int{"done": 0, "error": 0, "blocked": 0, "open": 0}}
			byWorker[worker] = r
		}
		return r
	}
	for rows.Next() {
		var worker string
		var runs, done, failed, blocked, open int
		var elapsed int64
		if err := rows.Scan(&worker, &runs, &done, &failed, &blocked, &open, &elapsed); err != nil {
			return nil, fmt.Errorf("store: weekly report scan: %w", err)
		}
		r := get(worker)
		r.Runs, r.ElapsedS = runs, elapsed
		r.Statuses["done"], r.Statuses["error"], r.Statuses["blocked"], r.Statuses["open"] = done, failed, blocked, open
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: weekly report rows: %w", err)
	}

	// Usage from the ledger. Source is "agent:<worker>".
	const prefix = "agent:"
	uquery := `SELECT source, SUM(tokens_in), SUM(tokens_out), SUM(cost_usd)
	      FROM cost_log
	      WHERE user = ? AND created_at >= ? AND created_at < ?`
	uargs := []any{user, from, to}
	if len(workers) > 0 {
		uquery += fmt.Sprintf(" AND source IN (%s)", placeholders(len(workers)))
		for _, w := range workers {
			uargs = append(uargs, prefix+w)
		}
	} else {
		uquery += " AND source LIKE 'agent:%'"
	}
	uquery += " GROUP BY source"
	urows, err := s.db.Query(uquery, uargs...)
	if err != nil {
		return nil, fmt.Errorf("store: weekly usage: %w", err)
	}
	defer urows.Close()
	for urows.Next() {
		var source string
		var tin, tout int64
		var cost float64
		if err := urows.Scan(&source, &tin, &tout, &cost); err != nil {
			return nil, fmt.Errorf("store: weekly usage scan: %w", err)
		}
		r := get(strings.TrimPrefix(source, prefix))
		r.TokensIn, r.TokensOut, r.CostUSD = tin, tout, cost
	}
	if err := urows.Err(); err != nil {
		return nil, fmt.Errorf("store: weekly usage rows: %w", err)
	}

	if err := s.weeklyTasks(user, from, to, workers, byWorker); err != nil {
		return nil, err
	}
	out := make([]WeeklyWorkerReport, 0, len(byWorker))
	for _, r := range byWorker {
		r.TasksOmitted = r.Runs - len(r.Tasks)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Worker < out[j].Worker })
	return out, nil
}

// weeklyTasks fills in each worker's newest maxWeeklyTasksPerWorker runs,
// oldest first.
func (s *Store) weeklyTasks(user, from, to string, workers []string, byWorker map[string]*WeeklyWorkerReport) error {
	inner := `SELECT worker, id, substr(task, 1, 2000) AS task, status, started_at, elapsed_s, cost_usd,
	        substr(result, 1, 2000) AS result, substr(error, 1, 2000) AS error, substr(detail, 1, 2000) AS detail,
	        ROW_NUMBER() OVER (PARTITION BY worker ORDER BY id DESC) AS rn
	      FROM dispatch_runs
	      WHERE user = ? AND started_at >= ? AND started_at < ?`
	args := []any{user, from, to}
	if len(workers) > 0 {
		inner += fmt.Sprintf(" AND worker IN (%s)", placeholders(len(workers)))
		for _, w := range workers {
			args = append(args, w)
		}
	}
	query := `SELECT worker, id, task, status, started_at, elapsed_s, cost_usd, result, error, detail
	      FROM (` + inner + `) WHERE rn <= ? ORDER BY id`
	args = append(args, maxWeeklyTasksPerWorker)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("store: weekly tasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var worker, result, runErr, detail string
		var t WeeklyTaskSummary
		if err := rows.Scan(&worker, &t.ID, &t.Task, &t.Status, &t.StartedAt, &t.ElapsedS, &t.CostUSD,
			&result, &runErr, &detail); err != nil {
			return fmt.Errorf("store: weekly task scan: %w", err)
		}
		t.Task = excerpt(t.Task, weeklyExcerptRunes)
		switch t.Status {
		case "done":
			t.Outcome = excerpt(result, weeklyExcerptRunes)
		case "error":
			t.Outcome = excerpt(runErr, weeklyExcerptRunes)
		default:
			t.Outcome = excerpt(detail, weeklyExcerptRunes)
		}
		if r := byWorker[worker]; r != nil {
			r.Tasks = append(r.Tasks, t)
		}
	}
	return rows.Err()
}

// excerpt folds text onto one line and cuts it to n runes.
func excerpt(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func placeholders(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			s += ","
		}
		s += "?"
	}
	return s
}
