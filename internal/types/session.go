package types

import (
	"context"
	"time"
)

// SessionRecord represents a persistent session.
type SessionRecord struct {
	ID           string    `json:"id"`
	Name         string    `json:"name,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	Archived     bool      `json:"archived"`
}

// SessionStore manages session persistence.
type SessionStore interface {
	Create(ctx context.Context, name string) (*SessionRecord, error)
	Get(ctx context.Context, id string) (*SessionRecord, error)
	List(ctx context.Context, includeArchived bool) ([]SessionRecord, error)
	Update(ctx context.Context, record *SessionRecord) error
	Archive(ctx context.Context, id string) error
}
