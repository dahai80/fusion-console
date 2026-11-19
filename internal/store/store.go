// Package store persists conversations, messages and cost accounting in a
// local SQLite database. Data stays on the office host; no cloud sync.
package store

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Message is one row in the messages table. Meta carries provider usage or
// agent cost/event payloads as JSON.
type Message struct {
	ID        int64
	ConvID    int64
	Role      string
	Content   string
	Meta      string
	CreatedAt time.Time
}

// Conversation is one chat or agent session.
type Conversation struct {
	ID         int64
	User       string
	Title      string
	Kind       string // "chat" or "agent"
	AgentName  string
	Workdir    string
	ProviderID string // per-conversation provider binding; empty = default
	Model      string // per-conversation model; empty = provider default
	CreatedAt  time.Time
}

// Store wraps the SQLite handle.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	log.Printf("store: opened %s", path)
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'chat',
			agent_name TEXT NOT NULL DEFAULT '',
			workdir TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conv_id INTEGER NOT NULL REFERENCES conversations(id),
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			meta TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conv_id, id)`,
		`CREATE TABLE IF NOT EXISTS failure_tags (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conv_id INTEGER NOT NULL DEFAULT 0,
			user TEXT NOT NULL,
			agent TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_failure_tags_user ON failure_tags(user, id)`,
		`CREATE TABLE IF NOT EXISTS dispatch_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conv_id INTEGER NOT NULL DEFAULT 0,
			user TEXT NOT NULL DEFAULT '',
			worker TEXT NOT NULL DEFAULT '',
			agent TEXT NOT NULL DEFAULT '',
			task TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'queued',
			started_at TEXT NOT NULL DEFAULT '',
			finished_at TEXT NOT NULL DEFAULT '',
			elapsed_s INTEGER NOT NULL DEFAULT 0,
			cost_usd REAL NOT NULL DEFAULT 0,
			tokens_in INTEGER NOT NULL DEFAULT 0,
			tokens_out INTEGER NOT NULL DEFAULT 0,
			detail TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			read INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS cost_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conv_id INTEGER NOT NULL,
			user TEXT NOT NULL,
			source TEXT NOT NULL,
			cost_usd REAL NOT NULL DEFAULT 0,
			tokens_in INTEGER NOT NULL DEFAULT 0,
			tokens_out INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		// memory_outbox buffers worker-result memory events while the
		// (optional) fusion-memory service is unreachable. FIFO, capped by
		// the writer; replayed in order once the probe reports healthy.
		`CREATE TABLE IF NOT EXISTS memory_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			worker TEXT NOT NULL DEFAULT '',
			conv_id INTEGER NOT NULL DEFAULT 0,
			kind TEXT NOT NULL DEFAULT 'worker_result',
			content TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	if err := addColumn(s.db, "dispatch_runs", "requires_repo", "ALTER TABLE dispatch_runs ADD COLUMN requires_repo INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumn(s.db, "dispatch_runs", "repo", "ALTER TABLE dispatch_runs ADD COLUMN repo TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return s.migrateConversationModelColumns()
}

// migrateConversationModelColumns adds the per-conversation provider/model
// columns to an existing database. Check-then-alter against PRAGMA
// table_info, so rerunning on every startup is a no-op — safe on restart
// and on databases created before the columns existed.
func (s *Store) migrateConversationModelColumns() error {
	rows, err := s.db.Query(`PRAGMA table_info(conversations)`)
	if err != nil {
		return fmt.Errorf("store: migrate: table_info conversations: %w", err)
	}
	defer rows.Close()
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("store: migrate: scan table_info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: migrate: table_info: %w", err)
	}
	for _, col := range []struct {
		name string
		ddl  string
	}{
		{"provider_id", `ALTER TABLE conversations ADD COLUMN provider_id TEXT NOT NULL DEFAULT ''`},
		{"model", `ALTER TABLE conversations ADD COLUMN model TEXT NOT NULL DEFAULT ''`},
	} {
		if existing[col.name] {
			continue
		}
		if _, err := s.db.Exec(col.ddl); err != nil {
			return fmt.Errorf("store: migrate: add conversations.%s: %w", col.name, err)
		}
		log.Printf("store: migrated conversations.%s column", col.name)
	}
	return nil
}

// addColumn runs ddl when column is missing from table. CREATE-IF-NOT-EXISTS
// cannot express this, and a bare ALTER fails on every startup after the
// first — hence the PRAGMA probe.
func addColumn(db *sql.DB, table, column, ddl string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil // already migrated
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(ddl)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// CreateConversation inserts a conversation and returns its ID.
func (s *Store) CreateConversation(user, title, kind, agentName, workdir string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO conversations (user, title, kind, agent_name, workdir, created_at) VALUES (?,?,?,?,?,?)`,
		user, title, kind, agentName, workdir, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("store: create conversation: %w", err)
	}
	return res.LastInsertId()
}

// ConversationModel returns the conversation's chosen model source.
func (s *Store) ConversationModel(convID int64) (providerID, model string, err error) {
	err = s.db.QueryRow(`SELECT provider_id, model FROM conversations WHERE id = ?`, convID).
		Scan(&providerID, &model)
	if err != nil {
		return "", "", fmt.Errorf("store: conversation model: %w", err)
	}
	return providerID, model, nil
}

// ConversationProviderUsers returns the IDs of conversations bound to a
// provider — the delete-confirmation consumer list (design v2 §4.1A).
func (s *Store) ConversationProviderUsers(providerID string) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM conversations WHERE provider_id = ? LIMIT 50`, providerID)
	if err != nil {
		return nil, fmt.Errorf("store: conversation provider users: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListConversations returns conversations for a user, newest first.
func (s *Store) ListConversations(user string) ([]Conversation, error) {
	rows, err := s.db.Query(
		`SELECT id, user, title, kind, agent_name, workdir, provider_id, model, created_at
		 FROM conversations WHERE user = ? ORDER BY id DESC LIMIT 200`, user)
	if err != nil {
		return nil, fmt.Errorf("store: list conversations: %w", err)
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		var created string
		if err := rows.Scan(&c.ID, &c.User, &c.Title, &c.Kind, &c.AgentName, &c.Workdir, &c.ProviderID, &c.Model, &created); err != nil {
			return nil, fmt.Errorf("store: scan conversation: %w", err)
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Conversation returns one conversation owned by user.
func (s *Store) Conversation(user string, id int64) (*Conversation, error) {
	var c Conversation
	var created string
	err := s.db.QueryRow(
		`SELECT id, user, title, kind, agent_name, workdir, provider_id, model, created_at
		 FROM conversations WHERE id = ? AND user = ?`, id, user).
		Scan(&c.ID, &c.User, &c.Title, &c.Kind, &c.AgentName, &c.Workdir, &c.ProviderID, &c.Model, &created)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("conversation %d not found for user", id)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get conversation: %w", err)
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &c, nil
}

// SetConversationModel persists the per-conversation provider/model binding.
// Empty values clear the binding (fall back to the provider default).
func (s *Store) SetConversationModel(user string, id int64, providerID, model string) error {
	res, err := s.db.Exec(
		`UPDATE conversations SET provider_id = ?, model = ? WHERE id = ? AND user = ?`,
		providerID, model, id, user)
	if err != nil {
		return fmt.Errorf("store: set conversation model: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("conversation %d not found for user", id)
	}
	return nil
}

const insertMessage = `INSERT INTO messages (conv_id, role, content, meta, created_at) VALUES (?,?,?,?,?)`

// AppendMessage inserts a message row.
func (s *Store) AppendMessage(convID int64, role, content, meta string) (int64, error) {
	res, err := s.db.Exec(insertMessage,
		convID, role, content, meta, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("store: append message: %w", err)
	}
	return res.LastInsertId()
}

// ListMessages returns all messages of a conversation in order.
func (s *Store) ListMessages(convID int64) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, conv_id, role, content, meta, created_at FROM messages WHERE conv_id = ? ORDER BY id`, convID)
	if err != nil {
		return nil, fmt.Errorf("store: list messages: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created string
		if err := rows.Scan(&m.ID, &m.ConvID, &m.Role, &m.Content, &m.Meta, &created); err != nil {
			return nil, fmt.Errorf("store: scan message: %w", err)
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, m)
	}
	return out, rows.Err()
}

// LogCost records cost/token accounting for a conversation.
func (s *Store) LogCost(convID int64, user, source string, costUSD float64, tokensIn, tokensOut int) error {
	_, err := s.db.Exec(
		`INSERT INTO cost_log (conv_id, user, source, cost_usd, tokens_in, tokens_out, created_at) VALUES (?,?,?,?,?,?,?)`,
		convID, user, source, costUSD, tokensIn, tokensOut, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("store: log cost: %w", err)
	}
	return nil
}

// UsageDashboard aggregates token/cost usage for the dashboard, split into
// the two consumption roles: brain (chat/planner rows, source "chat:*") and
// workers (agent dispatch rows, source "agent:*"). rangeID is one of
// today/week/month/all; the bucket granularity follows the window (hourly /
// daily / daily / monthly).
func (s *Store) UsageDashboard(user, rangeID string) (map[string]any, error) {
	var cutoff, bucketExpr, labelFmt string
	switch rangeID {
	case "today":
		cutoff = time.Now().UTC().Format("2006-01-02")
		bucketExpr, labelFmt = "substr(created_at, 12, 2)", "hour"
	case "week":
		cutoff = time.Now().UTC().AddDate(0, 0, -6).Format("2006-01-02")
		bucketExpr, labelFmt = "substr(created_at, 1, 10)", "day"
	case "month":
		cutoff = time.Now().UTC().AddDate(0, 0, -29).Format("2006-01-02")
		bucketExpr, labelFmt = "substr(created_at, 1, 10)", "day"
	case "all":
		bucketExpr, labelFmt = "substr(created_at, 1, 7)", "month"
	default:
		return nil, fmt.Errorf("store: unknown range %q", rangeID)
	}
	q := `SELECT ` + bucketExpr + ` AS bucket,
	        CASE WHEN source LIKE 'agent:%' THEN 'worker' ELSE 'brain' END AS role,
	        COUNT(*) AS calls, SUM(tokens_in) AS tokens_in, SUM(tokens_out) AS tokens_out,
	        ROUND(SUM(cost_usd), 4) AS cost_usd
	      FROM cost_log WHERE user = ? OR user = 'mcp-brain'`
	args := []any{user}
	if cutoff != "" {
		q += ` AND substr(created_at, 1, 10) >= ?`
		args = append(args, cutoff)
	}
	q += ` GROUP BY bucket, role ORDER BY bucket`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: usage dashboard: %w", err)
	}
	defer rows.Close()

	type agg struct {
		calls int64
		in    int64
		out   int64
		cost  float64
	}
	m := map[string]*[2]agg{} // bucket -> [brain, worker]
	for rows.Next() {
		var bucket, role string
		var calls int64
		var tin, tout sql.NullInt64
		var cost float64
		if err := rows.Scan(&bucket, &role, &calls, &tin, &tout, &cost); err != nil {
			return nil, fmt.Errorf("store: usage dashboard scan: %w", err)
		}
		slot := 0
		if role == "worker" {
			slot = 1
		}
		if m[bucket] == nil {
			m[bucket] = &[2]agg{}
		}
		a := &m[bucket][slot]
		a.calls, a.cost = calls, cost
		if tin.Valid {
			a.in += tin.Int64
		}
		if tout.Valid {
			a.out += tout.Int64
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: usage dashboard rows: %w", err)
	}

	type bucketRow struct {
		Bucket   string  `json:"bucket"`
		BrainIn  int64   `json:"brain_tokens_in"`
		BrainOut int64   `json:"brain_tokens_out"`
		WorkIn   int64   `json:"worker_tokens_in"`
		WorkOut  int64   `json:"worker_tokens_out"`
		BrainUSD float64 `json:"brain_cost_usd"`
		WorkUSD  float64 `json:"worker_cost_usd"`
	}
	buckets := []bucketRow{}
	tot := bucketRow{}
	for b, pair := range m {
		row := bucketRow{Bucket: b, BrainIn: pair[0].in, BrainOut: pair[0].out, WorkIn: pair[1].in, WorkOut: pair[1].out, BrainUSD: pair[0].cost, WorkUSD: pair[1].cost}
		buckets = append(buckets, row)
		tot.BrainIn += row.BrainIn
		tot.BrainOut += row.BrainOut
		tot.WorkIn += row.WorkIn
		tot.WorkOut += row.WorkOut
		tot.BrainUSD += row.BrainUSD
		tot.WorkUSD += row.WorkUSD
	}
	round4 := func(v float64) float64 { return math.Round(v*10000) / 10000 }
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Bucket < buckets[j].Bucket })
	return map[string]any{"range": rangeID, "bucket_unit": labelFmt, "buckets": buckets,
		"totals": map[string]any{
			"brain_tokens": tot.BrainIn + tot.BrainOut, "worker_tokens": tot.WorkIn + tot.WorkOut,
			"brain_cost_usd": round4(tot.BrainUSD), "worker_cost_usd": round4(tot.WorkUSD),
		}}, nil
}

// CostReport aggregates cost per day for a user.
func (s *Store) CostReport(user string) ([]map[string]any, error) {
	rows, err := s.db.Query(
		`SELECT substr(created_at, 1, 10) AS day, source, COUNT(*) AS calls,
		        ROUND(SUM(cost_usd), 4) AS cost_usd, SUM(tokens_in) AS tokens_in, SUM(tokens_out) AS tokens_out
		 FROM cost_log WHERE user = ? GROUP BY day, source ORDER BY day DESC LIMIT 60`, user)
	if err != nil {
		return nil, fmt.Errorf("store: cost report: %w", err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var day, source string
		var calls int
		var cost float64
		var tin, tout sql.NullInt64
		if err := rows.Scan(&day, &source, &calls, &cost, &tin, &tout); err != nil {
			return nil, fmt.Errorf("store: scan cost report: %w", err)
		}
		out = append(out, map[string]any{
			"day": day, "source": source, "calls": calls, "cost_usd": cost,
			"tokens_in": tin.Int64, "tokens_out": tout.Int64,
		})
	}
	return out, rows.Err()
}

// WorkerSpend aggregates per-worker cost rows (source "agent:<name>") over
// all time, keyed by source, for the dashboard Workers tab.
func (s *Store) WorkerSpend(user string) (map[string]map[string]any, error) {
	// Legacy rows dispatched by the brain's MCP sidecar carry user
	// "mcp-brain" (no auth context there); count them for the requesting
	// user so historical worker spend is not invisible.
	rows, err := s.db.Query(
		`SELECT source, COUNT(*) AS calls, ROUND(SUM(cost_usd), 4) AS cost_usd,
		        SUM(tokens_in) AS tokens_in, SUM(tokens_out) AS tokens_out,
		        MAX(created_at) AS last_run
		 FROM cost_log
		 WHERE source LIKE 'agent:%' AND (user = ? OR user = 'mcp-brain')
		 GROUP BY source`, user)
	if err != nil {
		return nil, fmt.Errorf("store: worker spend: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var source, last string
		var calls int
		var cost float64
		var tin, tout sql.NullInt64
		if err := rows.Scan(&source, &calls, &cost, &tin, &tout, &last); err != nil {
			return nil, fmt.Errorf("store: scan worker spend: %w", err)
		}
		out[source] = map[string]any{
			"calls": calls, "cost_usd": cost,
			"tokens_in": tin.Int64, "tokens_out": tout.Int64, "last_run": last,
		}
	}
	return out, rows.Err()
}

// LogFailure records one failure-tag row (MAST taxonomy, issue #4):
// category is fc1_brief | fc2_handoff | fc3_tool | other.
func (s *Store) LogFailure(convID int64, user, agentName, category, note string) error {
	if _, err := s.db.Exec(
		`INSERT INTO failure_tags (conv_id, user, agent, category, note, created_at) VALUES (?,?,?,?,?,?)`,
		convID, user, agentName, category, note, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("store: log failure: %w", err)
	}
	return nil
}

// ListFailures returns recent failure tags for a user, newest first.
func (s *Store) ListFailures(user string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, conv_id, agent, category, note, created_at
		 FROM failure_tags WHERE user = ? ORDER BY id DESC LIMIT ?`, user, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list failures: %w", err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, convID int64
		var agentName, category, note, created string
		if err := rows.Scan(&id, &convID, &agentName, &category, &note, &created); err != nil {
			return nil, fmt.Errorf("store: scan failures: %w", err)
		}
		out = append(out, map[string]any{
			"id": id, "conv_id": convID, "agent": agentName,
			"category": category, "note": note, "created_at": created,
		})
	}
	return out, rows.Err()
}

// FailuresForExport returns every failure tag for a user in chronological
// order (JSONL export feed).
func (s *Store) FailuresForExport(user string) ([]map[string]any, error) {
	rows, err := s.db.Query(
		`SELECT id, conv_id, agent, category, note, created_at
		 FROM failure_tags WHERE user = ? ORDER BY id ASC`, user)
	if err != nil {
		return nil, fmt.Errorf("store: export failures: %w", err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, convID int64
		var agentName, category, note, created string
		if err := rows.Scan(&id, &convID, &agentName, &category, &note, &created); err != nil {
			return nil, fmt.Errorf("store: scan export failures: %w", err)
		}
		out = append(out, map[string]any{
			"id": id, "conv_id": convID, "agent": agentName,
			"category": category, "note": note, "created_at": created,
		})
	}
	return out, rows.Err()
}

// DeleteConversation removes a conversation and its messages.
func (s *Store) DeleteConversation(user string, id int64) error {
	if _, err := s.db.Exec(`DELETE FROM messages WHERE conv_id = ?`, id); err != nil {
		return fmt.Errorf("store: delete messages: %w", err)
	}
	res, err := s.db.Exec(`DELETE FROM conversations WHERE id = ? AND user = ?`, id, user)
	if err != nil {
		return fmt.Errorf("store: delete conversation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: conversation %d not found for user", id)
	}
	return nil
}

// RenameConversation sets a new title on a conversation owned by user.
func (s *Store) RenameConversation(user string, id int64, title string) error {
	res, err := s.db.Exec(`UPDATE conversations SET title = ? WHERE id = ? AND user = ?`, title, id, user)
	if err != nil {
		return fmt.Errorf("store: rename conversation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: conversation %d not found for user", id)
	}
	return nil
}

// DispatchRunRow is one background dispatch, persisted so the run history
// and unread state survive console restarts.
type DispatchRunRow struct {
	ID           int64
	ConvID       int64
	User         string
	Worker       string
	Agent        string
	Task         string
	RequiresRepo bool
	Repo         string
	Status       string
	StartedAt    string
	FinishedAt   string
	ElapsedS     int
	CostUSD      float64
	TokensIn     int
	TokensOut    int
	Detail       string
	Result       string
	Error        string
	Read         bool
}

// CreateDispatchRun inserts a row and returns its ID.
func (s *Store) CreateDispatchRun(r *DispatchRunRow) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO dispatch_runs (conv_id, user, worker, agent, task, status, started_at, finished_at,
		  elapsed_s, cost_usd, tokens_in, tokens_out, detail, result, error, read, requires_repo, repo)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ConvID, r.User, r.Worker, r.Agent, r.Task, r.Status, r.StartedAt, r.FinishedAt,
		r.ElapsedS, r.CostUSD, r.TokensIn, r.TokensOut, r.Detail, r.Result, r.Error, r.Read, r.RequiresRepo, r.Repo)
	if err != nil {
		return 0, fmt.Errorf("store: create dispatch run: %w", err)
	}
	return res.LastInsertId()
}

// SaveDispatchRun writes the full row back (status transitions and final
// accounting are the only writers).
func (s *Store) SaveDispatchRun(r *DispatchRunRow) error {
	return saveDispatchRun(s.db.Exec, r)
}

// FinishDispatchWithReceipt makes a chat receipt visible in the same commit
// as its terminal card, including after a console restart.
func (s *Store) FinishDispatchWithReceipt(r *DispatchRunRow, content, meta string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveDispatchRun(tx.Exec, r); err != nil {
		return err
	}
	if _, err := tx.Exec(insertMessage, r.ConvID, "assistant", content, meta, r.FinishedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func saveDispatchRun(exec func(string, ...any) (sql.Result, error), r *DispatchRunRow) error {
	_, err := exec(
		`UPDATE dispatch_runs SET status=?, started_at=?, finished_at=?, elapsed_s=?, cost_usd=?,
		  tokens_in=?, tokens_out=?, detail=?, result=?, error=?, read=? WHERE id=?`,
		r.Status, r.StartedAt, r.FinishedAt, r.ElapsedS, r.CostUSD,
		r.TokensIn, r.TokensOut, r.Detail, r.Result, r.Error, r.Read, r.ID)
	if err != nil {
		return fmt.Errorf("store: save dispatch run %d: %w", r.ID, err)
	}
	return nil
}

// AckDispatchRuns marks every finished run of one conversation as seen.
func (s *Store) AckDispatchRuns(convID int64) error {
	_, err := s.db.Exec(`UPDATE dispatch_runs SET read=1 WHERE conv_id=? AND status IN ('done','blocked')`, convID)
	if err != nil {
		return fmt.Errorf("store: ack dispatch runs: %w", err)
	}
	return nil
}

// ListDispatchRuns returns the most recent runs, oldest first (list order is
// the caller's concern; the registry wants append order).
func (s *Store) ListDispatchRuns(limit int) ([]*DispatchRunRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.Query(
		`SELECT id, conv_id, user, worker, agent, task, status, started_at, finished_at,
		  elapsed_s, cost_usd, tokens_in, tokens_out, detail, result, error, read, requires_repo, repo
		 FROM dispatch_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list dispatch runs: %w", err)
	}
	defer rows.Close()
	var out []*DispatchRunRow
	for rows.Next() {
		var r DispatchRunRow
		if err := rows.Scan(&r.ID, &r.ConvID, &r.User, &r.Worker, &r.Agent, &r.Task, &r.Status,
			&r.StartedAt, &r.FinishedAt, &r.ElapsedS, &r.CostUSD, &r.TokensIn, &r.TokensOut,
			&r.Detail, &r.Result, &r.Error, &r.Read, &r.RequiresRepo, &r.Repo); err != nil {
			return nil, fmt.Errorf("store: scan dispatch run: %w", err)
		}
		out = append(out, &r)
	}
	// reverse into ascending id order
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// DeleteDispatchRun removes one run row (dismiss).
func (s *Store) DeleteDispatchRun(id int64) error {
	_, err := s.db.Exec(`DELETE FROM dispatch_runs WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete dispatch run %d: %w", id, err)
	}
	return nil
}

// ClearFinishedDispatchRuns removes all terminal run rows.
func (s *Store) ClearFinishedDispatchRuns() error {
	_, err := s.db.Exec(`DELETE FROM dispatch_runs WHERE status IN ('done','blocked','error')`)
	if err != nil {
		return fmt.Errorf("store: clear finished dispatch runs: %w", err)
	}
	return nil
}

// MemoryEvent is one worker-result lesson queued for the (optional)
// fusion-memory service. Buffered in memory_outbox while the service is
// unreachable, replayed FIFO once it is healthy again.
type MemoryEvent struct {
	ID        int64
	Worker    string
	ConvID    int64
	Kind      string
	Content   string
	Tags      string
	CreatedAt string
}

const memoryOutboxCap = 1000

// EnqueueMemoryEvent buffers an event for later delivery. The cap keeps a
// long offline stretch from growing the table without bound — oldest rows
// are trimmed first (FIFO loss is acceptable for observability-grade data).
func (s *Store) EnqueueMemoryEvent(e *MemoryEvent) error {
	if _, err := s.db.Exec(
		`INSERT INTO memory_outbox (worker, conv_id, kind, content, tags, created_at)
		 VALUES (?,?,?,?,?,?)`,
		e.Worker, e.ConvID, e.Kind, e.Content, e.Tags, e.CreatedAt); err != nil {
		return fmt.Errorf("store: enqueue memory event: %w", err)
	}
	if _, err := s.db.Exec(
		`DELETE FROM memory_outbox WHERE id <= (
			SELECT id FROM memory_outbox ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		memoryOutboxCap); err != nil {
		return fmt.Errorf("store: trim memory outbox: %w", err)
	}
	return nil
}

// PendingMemoryEvents returns buffered events in FIFO order.
func (s *Store) PendingMemoryEvents(limit int) ([]MemoryEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, worker, conv_id, kind, content, tags, created_at
		 FROM memory_outbox ORDER BY id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: pending memory events: %w", err)
	}
	defer rows.Close()
	var out []MemoryEvent
	for rows.Next() {
		var e MemoryEvent
		if err := rows.Scan(&e.ID, &e.Worker, &e.ConvID, &e.Kind, &e.Content, &e.Tags, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan memory event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteMemoryEvents removes replayed rows.
func (s *Store) DeleteMemoryEvents(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	q := `DELETE FROM memory_outbox WHERE id IN (` +
		strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := s.db.Exec(q, args...); err != nil {
		return fmt.Errorf("store: delete memory events: %w", err)
	}
	return nil
}
