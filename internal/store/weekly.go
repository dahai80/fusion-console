package store

import (
	"fmt"
	"time"
)

// WeeklyWorkerReport aggregates one worker's dispatch activity.
type WeeklyWorkerReport struct {
	Worker    string              `json:"worker"`
	Runs      int                 `json:"runs"`
	Statuses  map[string]int      `json:"statuses"`
	TokensIn  int64               `json:"tokens_in"`
	TokensOut int64               `json:"tokens_out"`
	CostUSD   float64             `json:"cost_usd"`
	ElapsedS  int64               `json:"elapsed_s"`
	Tasks     []WeeklyTaskSummary `json:"tasks"`
}

// WeeklyTaskSummary is one dispatch line in the weekly report.
type WeeklyTaskSummary struct {
	ID       int64   `json:"id"`
	Task     string  `json:"task"`
	Status   string  `json:"status"`
	ElapsedS int     `json:"elapsed_s"`
	CostUSD  float64 `json:"cost_usd"`
}

// WeeklyReport aggregates dispatch_runs for one user over [start, end)
// (RFC3339 timestamps in, same layout as dispatch_runs.started_at). The
// workers filter is optional: empty means every worker. Task summaries are
// capped at maxWeeklyTasks to keep the payload bounded.
const maxWeeklyTasks = 200

func (s *Store) WeeklyReport(user string, start, end time.Time, workers []string) ([]WeeklyWorkerReport, error) {
	query := `SELECT worker, COUNT(*),
	        SUM(CASE WHEN status='done' THEN 1 ELSE 0 END),
	        SUM(CASE WHEN status='error' THEN 1 ELSE 0 END),
	        SUM(CASE WHEN status='blocked' THEN 1 ELSE 0 END),
	        SUM(CASE WHEN status NOT IN ('done','error','blocked') THEN 1 ELSE 0 END),
	        SUM(tokens_in), SUM(tokens_out), SUM(cost_usd), SUM(elapsed_s)
	      FROM dispatch_runs
	      WHERE user = ? AND started_at >= ? AND started_at < ?`
	args := []any{user, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)}
	if len(workers) > 0 {
		query += fmt.Sprintf(" AND worker IN (%s)", placeholders(len(workers)))
		for _, w := range workers {
			args = append(args, w)
		}
	}
	query += " GROUP BY worker ORDER BY worker"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: weekly report: %w", err)
	}
	defer rows.Close()
	var out []WeeklyWorkerReport
	for rows.Next() {
		var r WeeklyWorkerReport
		r.Statuses = map[string]int{}
		var done, failed, blocked, open int
		if err := rows.Scan(&r.Worker, &r.Runs, &done, &failed, &blocked, &open,
			&r.TokensIn, &r.TokensOut, &r.CostUSD, &r.ElapsedS); err != nil {
			return nil, fmt.Errorf("store: weekly report scan: %w", err)
		}
		r.Statuses["done"], r.Statuses["error"], r.Statuses["blocked"], r.Statuses["open"] = done, failed, blocked, open
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: weekly report rows: %w", err)
	}
	if err := s.weeklyTasks(user, start, end, workers, out); err != nil {
		return nil, err
	}
	return out, nil
}

// weeklyTasks fills in the per-worker task lists (bounded per worker).
func (s *Store) weeklyTasks(user string, start, end time.Time, workers []string, reports []WeeklyWorkerReport) error {
	byWorker := map[string]*WeeklyWorkerReport{}
	for i := range reports {
		byWorker[reports[i].Worker] = &reports[i]
	}
	query := `SELECT worker, id, task, status, elapsed_s, cost_usd
	      FROM dispatch_runs
	      WHERE user = ? AND started_at >= ? AND started_at < ?`
	args := []any{user, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)}
	if len(workers) > 0 {
		query += fmt.Sprintf(" AND worker IN (%s)", placeholders(len(workers)))
		for _, w := range workers {
			args = append(args, w)
		}
	}
	query += " ORDER BY id LIMIT ?"
	args = append(args, maxWeeklyTasks)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("store: weekly tasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var worker string
		var t WeeklyTaskSummary
		if err := rows.Scan(&worker, &t.ID, &t.Task, &t.Status, &t.ElapsedS, &t.CostUSD); err != nil {
			return fmt.Errorf("store: weekly task scan: %w", err)
		}
		if r := byWorker[worker]; r != nil {
			r.Tasks = append(r.Tasks, t)
		}
	}
	return rows.Err()
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
