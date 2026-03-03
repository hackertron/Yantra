package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection with memory-specific operations.
type DB struct {
	conn *sql.DB
}

// OpenDB opens (or creates) a SQLite database at dbPath with WAL mode,
// busy timeout, and runs schema migrations.
func OpenDB(dbPath string) (*DB, error) {
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("memory: mkdir %s: %w", dir, err)
		}
	}

	conn, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("memory: open db: %w", err)
	}

	// Verify connectivity.
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("memory: ping db: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("memory: migrate: %w", err)
	}
	return db, nil
}

// Close closes the underlying database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn returns the underlying *sql.DB.
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// migrate creates all required tables if they don't exist.
func (db *DB) migrate() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS chunks (
			id         TEXT PRIMARY KEY,
			content    TEXT NOT NULL,
			source     TEXT NOT NULL DEFAULT 'user_saved',
			tags       TEXT NOT NULL DEFAULT '',
			embedding  BLOB,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,

		`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
			id,
			content,
			tokenize = 'porter unicode61'
		)`,

		`CREATE TABLE IF NOT EXISTS sessions (
			id            TEXT PRIMARY KEY,
			name          TEXT NOT NULL DEFAULT '',
			created_at    DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at    DATETIME NOT NULL DEFAULT (datetime('now')),
			message_count INTEGER NOT NULL DEFAULT 0,
			archived      INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS conversation_events (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id  TEXT NOT NULL REFERENCES sessions(id),
			role        TEXT NOT NULL,
			content     TEXT NOT NULL DEFAULT '',
			tool_calls  TEXT NOT NULL DEFAULT '[]',
			tool_call_id TEXT NOT NULL DEFAULT '',
			tool_name   TEXT NOT NULL DEFAULT '',
			created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,

		`CREATE INDEX IF NOT EXISTS idx_conversation_events_session
			ON conversation_events(session_id, id)`,

		`CREATE TABLE IF NOT EXISTS session_summaries (
			session_id TEXT PRIMARY KEY REFERENCES sessions(id),
			summary    TEXT NOT NULL DEFAULT '',
			epoch      INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS scratchpads (
			session_id TEXT PRIMARY KEY REFERENCES sessions(id),
			data       TEXT NOT NULL DEFAULT '{}'
		)`,
	}

	for _, stmt := range statements {
		if _, err := db.conn.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:40], err)
		}
	}
	return nil
}
