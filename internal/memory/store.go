package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

// Store implements types.MemoryRetrieval backed by SQLite and an optional
// embedding backend. If no embedder is configured, retrieval falls back to
// FTS-only search.
type Store struct {
	db       *DB
	embedder types.EmbeddingBackend // may be nil
	cfg      types.RetrievalConfig
}

// NewStore creates a new memory store.
func NewStore(db *DB, embedder types.EmbeddingBackend, cfg types.RetrievalConfig) *Store {
	if cfg.TopK <= 0 {
		cfg.TopK = 8
	}
	if cfg.VectorWeight <= 0 && cfg.FTSWeight <= 0 {
		cfg.VectorWeight = 0.7
		cfg.FTSWeight = 0.3
	}
	return &Store{db: db, embedder: embedder, cfg: cfg}
}

// Store saves a chunk to persistent memory.
func (s *Store) Store(ctx context.Context, req types.MemoryStoreRequest) (string, error) {
	id := generateID()

	var embedding []byte
	if s.embedder != nil {
		vec, err := s.embedder.Embed(ctx, req.Content)
		if err != nil {
			return "", &types.MemoryError{Op: "store", Message: "embedding failed", Err: err}
		}
		embedding = encodeFloat32s(vec)
	}

	tags := joinTags(req.Tags)
	source := req.Source
	if source == "" {
		source = "user_saved"
	}

	tx, err := s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return "", &types.MemoryError{Op: "store", Message: "begin tx", Err: err}
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO chunks (id, content, source, tags, embedding) VALUES (?, ?, ?, ?, ?)`,
		id, req.Content, source, tags, embedding)
	if err != nil {
		return "", &types.MemoryError{Op: "store", Message: "insert chunk", Err: err}
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO chunks_fts (id, content) VALUES (?, ?)`,
		id, req.Content)
	if err != nil {
		return "", &types.MemoryError{Op: "store", Message: "insert fts", Err: err}
	}

	if err := tx.Commit(); err != nil {
		return "", &types.MemoryError{Op: "store", Message: "commit", Err: err}
	}
	return id, nil
}

// Recall retrieves chunks matching a query using hybrid search with default weights.
func (s *Store) Recall(ctx context.Context, query string, topK int) ([]types.MemoryChunk, error) {
	if topK <= 0 {
		topK = s.cfg.TopK
	}
	return s.HybridQuery(ctx, types.HybridQueryRequest{
		Query:        query,
		TopK:         topK,
		VectorWeight: s.cfg.VectorWeight,
		FTSWeight:    s.cfg.FTSWeight,
	})
}

// Forget deletes a chunk by ID from both chunks and FTS tables.
func (s *Store) Forget(ctx context.Context, id string) error {
	tx, err := s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return &types.MemoryError{Op: "forget", Message: "begin tx", Err: err}
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts WHERE id = ?`, id); err != nil {
		return &types.MemoryError{Op: "forget", Message: "delete fts", Err: err}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE id = ?`, id); err != nil {
		return &types.MemoryError{Op: "forget", Message: "delete chunk", Err: err}
	}
	if err := tx.Commit(); err != nil {
		return &types.MemoryError{Op: "forget", Message: "commit", Err: err}
	}
	return nil
}

// HybridQuery performs weighted vector + FTS retrieval with reciprocal rank fusion.
func (s *Store) HybridQuery(ctx context.Context, req types.HybridQueryRequest) ([]types.MemoryChunk, error) {
	topK := req.TopK
	if topK <= 0 {
		topK = s.cfg.TopK
	}

	// Fetch more candidates from each source to feed into RRF.
	candidateN := topK * 3

	var vectorResults []scoredChunk
	if s.embedder != nil && req.VectorWeight > 0 {
		queryVec, err := s.embedder.Embed(ctx, req.Query)
		if err != nil {
			return nil, &types.MemoryError{Op: "hybrid_query", Message: "embed query", Err: err}
		}
		vectorResults, err = vectorSearch(ctx, s.db, queryVec, candidateN)
		if err != nil {
			return nil, &types.MemoryError{Op: "hybrid_query", Message: "vector search", Err: err}
		}
	}

	var ftsResults []scoredChunk
	if req.FTSWeight > 0 {
		var err error
		ftsResults, err = ftsSearch(ctx, s.db, req.Query, candidateN)
		if err != nil {
			// FTS can fail on malformed queries; fall back to vector-only.
			ftsResults = nil
		}
	}

	// If we only have one result set, use it directly.
	if len(vectorResults) == 0 && len(ftsResults) == 0 {
		return nil, nil
	}
	if len(vectorResults) == 0 {
		return toChunks(ftsResults, topK), nil
	}
	if len(ftsResults) == 0 {
		return toChunks(vectorResults, topK), nil
	}

	merged := reciprocalRankFusion(vectorResults, ftsResults, req.VectorWeight, req.FTSWeight, topK)
	return toChunks(merged, topK), nil
}

// GetSummary returns the rolling summary for a session.
func (s *Store) GetSummary(ctx context.Context, sessionID string) (*types.SessionSummary, error) {
	var summary string
	var epoch int64
	err := s.db.conn.QueryRowContext(ctx,
		`SELECT summary, epoch FROM session_summaries WHERE session_id = ?`, sessionID).
		Scan(&summary, &epoch)
	if err != nil {
		return nil, nil // no summary yet
	}
	return &types.SessionSummary{Summary: summary, Epoch: epoch}, nil
}

// SetSummary updates the rolling summary for a session.
func (s *Store) SetSummary(ctx context.Context, sessionID string, summary types.SessionSummary) error {
	_, err := s.db.conn.ExecContext(ctx,
		`INSERT INTO session_summaries (session_id, summary, epoch) VALUES (?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET summary = excluded.summary, epoch = excluded.epoch`,
		sessionID, summary.Summary, summary.Epoch)
	if err != nil {
		return &types.MemoryError{Op: "set_summary", Message: "upsert", Err: err}
	}
	return nil
}

// GetScratchpad returns the scratchpad state for a session.
func (s *Store) GetScratchpad(ctx context.Context, sessionID string) (*types.ScratchpadState, error) {
	var data string
	err := s.db.conn.QueryRowContext(ctx,
		`SELECT data FROM scratchpads WHERE session_id = ?`, sessionID).
		Scan(&data)
	if err != nil {
		return &types.ScratchpadState{Data: make(map[string]string)}, nil
	}
	var state types.ScratchpadState
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return &types.ScratchpadState{Data: make(map[string]string)}, nil
	}
	if state.Data == nil {
		state.Data = make(map[string]string)
	}
	return &state, nil
}

// SetScratchpad updates the scratchpad for a session.
func (s *Store) SetScratchpad(ctx context.Context, sessionID string, state types.ScratchpadState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return &types.MemoryError{Op: "set_scratchpad", Message: "marshal", Err: err}
	}
	_, err = s.db.conn.ExecContext(ctx,
		`INSERT INTO scratchpads (session_id, data) VALUES (?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET data = excluded.data`,
		sessionID, string(data))
	if err != nil {
		return &types.MemoryError{Op: "set_scratchpad", Message: "upsert", Err: err}
	}
	return nil
}

// StoreConversationEvent persists a message to the conversation log.
func (s *Store) StoreConversationEvent(ctx context.Context, sessionID string, msg types.Message) error {
	toolCallsJSON := "[]"
	if len(msg.ToolCalls) > 0 {
		b, err := json.Marshal(msg.ToolCalls)
		if err != nil {
			return &types.MemoryError{Op: "store_event", Message: "marshal tool_calls", Err: err}
		}
		toolCallsJSON = string(b)
	}

	_, err := s.db.conn.ExecContext(ctx,
		`INSERT INTO conversation_events (session_id, role, content, tool_calls, tool_call_id, tool_name)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, string(msg.Role), msg.Content, toolCallsJSON, msg.ToolCallID, msg.ToolName)
	if err != nil {
		return &types.MemoryError{Op: "store_event", Message: "insert", Err: err}
	}

	// Bump session message count.
	_, err = s.db.conn.ExecContext(ctx,
		`UPDATE sessions SET message_count = message_count + 1, updated_at = datetime('now') WHERE id = ?`,
		sessionID)
	if err != nil {
		return &types.MemoryError{Op: "store_event", Message: "update session count", Err: err}
	}
	return nil
}

// GetConversationHistory returns the conversation history for a session.
func (s *Store) GetConversationHistory(ctx context.Context, sessionID string, limit int) ([]types.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.conn.QueryContext(ctx,
		`SELECT role, content, tool_calls, tool_call_id, tool_name, created_at
		 FROM conversation_events
		 WHERE session_id = ?
		 ORDER BY id ASC
		 LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, &types.MemoryError{Op: "get_history", Message: "query", Err: err}
	}
	defer rows.Close()

	var msgs []types.Message
	for rows.Next() {
		var role, content, toolCallsJSON, toolCallID, toolName, createdAt string
		if err := rows.Scan(&role, &content, &toolCallsJSON, &toolCallID, &toolName, &createdAt); err != nil {
			return nil, &types.MemoryError{Op: "get_history", Message: "scan", Err: err}
		}

		msg := types.Message{
			Role:       types.MessageRole(role),
			Content:    content,
			ToolCallID: toolCallID,
			ToolName:   toolName,
		}
		if toolCallsJSON != "[]" && toolCallsJSON != "" {
			var tcs []types.ToolCall
			if err := json.Unmarshal([]byte(toolCallsJSON), &tcs); err == nil {
				msg.ToolCalls = tcs
			}
		}
		if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
			msg.CreatedAt = t
		}
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

// toChunks converts scored results to MemoryChunks, applying topK limit.
func toChunks(sc []scoredChunk, topK int) []types.MemoryChunk {
	if len(sc) > topK {
		sc = sc[:topK]
	}
	out := make([]types.MemoryChunk, len(sc))
	for i, s := range sc {
		c := s.chunk
		c.Score = s.score
		out[i] = c
	}
	return out
}

// generateID creates a random hex ID.
func generateID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var _ types.MemoryRetrieval = (*Store)(nil)
