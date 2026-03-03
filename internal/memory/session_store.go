package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

// SQLiteSessionStore implements types.SessionStore backed by SQLite.
type SQLiteSessionStore struct {
	db *DB
}

// NewSessionStore creates a new session store.
func NewSessionStore(db *DB) *SQLiteSessionStore {
	return &SQLiteSessionStore{db: db}
}

func (s *SQLiteSessionStore) Create(ctx context.Context, name string) (*types.SessionRecord, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, &types.MemoryError{Op: "session_create", Message: "generate id", Err: err}
	}
	now := time.Now().UTC()

	_, err = s.db.conn.ExecContext(ctx,
		`INSERT INTO sessions (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		id, name, now.Format(time.DateTime), now.Format(time.DateTime))
	if err != nil {
		return nil, &types.MemoryError{Op: "session_create", Message: "insert", Err: err}
	}

	return &types.SessionRecord{
		ID:        id,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *SQLiteSessionStore) Get(ctx context.Context, id string) (*types.SessionRecord, error) {
	var name, createdAt, updatedAt string
	var msgCount int
	var archived int

	err := s.db.conn.QueryRowContext(ctx,
		`SELECT name, created_at, updated_at, message_count, archived FROM sessions WHERE id = ?`, id).
		Scan(&name, &createdAt, &updatedAt, &msgCount, &archived)
	if err != nil {
		return nil, &types.MemoryError{Op: "session_get", Message: "not found", Err: err}
	}

	rec := &types.SessionRecord{
		ID:           id,
		Name:         name,
		MessageCount: msgCount,
		Archived:     archived != 0,
	}
	if t, err := time.Parse(time.DateTime, createdAt); err == nil {
		rec.CreatedAt = t
	}
	if t, err := time.Parse(time.DateTime, updatedAt); err == nil {
		rec.UpdatedAt = t
	}
	return rec, nil
}

func (s *SQLiteSessionStore) List(ctx context.Context, includeArchived bool) ([]types.SessionRecord, error) {
	query := `SELECT id, name, created_at, updated_at, message_count, archived FROM sessions`
	if !includeArchived {
		query += ` WHERE archived = 0`
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := s.db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, &types.MemoryError{Op: "session_list", Message: "query", Err: err}
	}
	defer rows.Close()

	var records []types.SessionRecord
	for rows.Next() {
		var id, name, createdAt, updatedAt string
		var msgCount, archived int
		if err := rows.Scan(&id, &name, &createdAt, &updatedAt, &msgCount, &archived); err != nil {
			return nil, &types.MemoryError{Op: "session_list", Message: "scan", Err: err}
		}
		rec := types.SessionRecord{
			ID:           id,
			Name:         name,
			MessageCount: msgCount,
			Archived:     archived != 0,
		}
		if t, err := time.Parse(time.DateTime, createdAt); err == nil {
			rec.CreatedAt = t
		}
		if t, err := time.Parse(time.DateTime, updatedAt); err == nil {
			rec.UpdatedAt = t
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (s *SQLiteSessionStore) Update(ctx context.Context, record *types.SessionRecord) error {
	_, err := s.db.conn.ExecContext(ctx,
		`UPDATE sessions SET name = ?, message_count = ?, updated_at = ? WHERE id = ?`,
		record.Name, record.MessageCount, time.Now().UTC().Format(time.DateTime), record.ID)
	if err != nil {
		return &types.MemoryError{Op: "session_update", Message: "update", Err: err}
	}
	return nil
}

func (s *SQLiteSessionStore) Archive(ctx context.Context, id string) error {
	_, err := s.db.conn.ExecContext(ctx,
		`UPDATE sessions SET archived = 1, updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.DateTime), id)
	if err != nil {
		return &types.MemoryError{Op: "session_archive", Message: "update", Err: err}
	}
	return nil
}

func generateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("ses_%s", hex.EncodeToString(b)), nil
}

var _ types.SessionStore = (*SQLiteSessionStore)(nil)
