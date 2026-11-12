// Package store persists conversations, messages and cost accounting in a
// local SQLite database. Data stays on the office host; no cloud sync.
package store

import (
	"database/sql"
	"fmt"
	"log"
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
	ID        int64
	User      string
	Title     string
	Kind      string // "chat" or "agent"
	AgentName string
	Workdir   string
	CreatedAt time.Time
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
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return nil
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

// ListConversations returns conversations for a user, newest first.
func (s *Store) ListConversations(user string) ([]Conversation, error) {
	rows, err := s.db.Query(
		`SELECT id, user, title, kind, agent_name, workdir, created_at
		 FROM conversations WHERE user = ? ORDER BY id DESC LIMIT 200`, user)
	if err != nil {
		return nil, fmt.Errorf("store: list conversations: %w", err)
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		var created string
		if err := rows.Scan(&c.ID, &c.User, &c.Title, &c.Kind, &c.AgentName, &c.Workdir, &created); err != nil {
			return nil, fmt.Errorf("store: scan conversation: %w", err)
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, c)
	}
	return out, rows.Err()
}

// AppendMessage inserts a message row.
func (s *Store) AppendMessage(convID int64, role, content, meta string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO messages (conv_id, role, content, meta, created_at) VALUES (?,?,?,?,?)`,
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
