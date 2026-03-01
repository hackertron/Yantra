package types

import (
	"context"
	"time"
)

// MemoryChunk is a stored piece of knowledge with its retrieval score.
type MemoryChunk struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Source    string    `json:"source"` // "user_saved", "conversation", "document"
	Tags      []string  `json:"tags,omitempty"`
	Score     float64   `json:"score,omitempty"` // retrieval score (0-1)
	CreatedAt time.Time `json:"created_at"`
}

// MemoryStoreRequest is the input for saving a memory chunk.
type MemoryStoreRequest struct {
	Content string   `json:"content"`
	Source  string   `json:"source"`
	Tags    []string `json:"tags,omitempty"`
}

// HybridQueryRequest configures a hybrid retrieval search.
type HybridQueryRequest struct {
	Query        string  `json:"query"`
	TopK         int     `json:"top_k"`
	VectorWeight float64 `json:"vector_weight"`
	FTSWeight    float64 `json:"fts_weight"`
}

// ScratchpadState holds per-session working memory.
type ScratchpadState struct {
	Data map[string]string `json:"data"`
}

// SessionSummary holds rolling summarization state.
type SessionSummary struct {
	Summary string `json:"summary"`
	Epoch   int64  `json:"epoch"`
}

// Memory is the basic persistence interface.
type Memory interface {
	// Store saves a chunk to persistent memory.
	Store(ctx context.Context, req MemoryStoreRequest) (string, error)

	// Recall retrieves chunks matching a query.
	Recall(ctx context.Context, query string, topK int) ([]MemoryChunk, error)

	// Forget deletes a chunk by ID.
	Forget(ctx context.Context, id string) error
}

// MemoryRetrieval extends Memory with hybrid search, summarization, and scratchpad.
type MemoryRetrieval interface {
	Memory

	// HybridQuery performs weighted vector + FTS retrieval.
	HybridQuery(ctx context.Context, req HybridQueryRequest) ([]MemoryChunk, error)

	// GetSummary returns the rolling summary for a session.
	GetSummary(ctx context.Context, sessionID string) (*SessionSummary, error)

	// SetSummary updates the rolling summary for a session.
	SetSummary(ctx context.Context, sessionID string, summary SessionSummary) error

	// GetScratchpad returns the scratchpad state for a session.
	GetScratchpad(ctx context.Context, sessionID string) (*ScratchpadState, error)

	// SetScratchpad updates the scratchpad for a session.
	SetScratchpad(ctx context.Context, sessionID string, state ScratchpadState) error

	// StoreConversationEvent persists a message to the conversation log.
	StoreConversationEvent(ctx context.Context, sessionID string, msg Message) error

	// GetConversationHistory returns the conversation history for a session.
	GetConversationHistory(ctx context.Context, sessionID string, limit int) ([]Message, error)
}

// EmbeddingBackend generates vector embeddings from text.
type EmbeddingBackend interface {
	// Embed returns a vector embedding for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// Dimensions returns the embedding vector size.
	Dimensions() int
}
