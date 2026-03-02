package memory

import (
	"context"
	"math"
	"testing"

	"github.com/hackertron/Yantra/internal/types"
)

// mockEmbedder returns deterministic 8-dimensional vectors for testing.
// It creates a different vector based on the first character of the input.
type mockEmbedder struct{}

func (m *mockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	// Generate a deterministic vector from the text.
	vec := make([]float32, 8)
	for i := range vec {
		if i < len(text) {
			vec[i] = float32(text[i]) / 255.0
		} else {
			vec[i] = float32(i) * 0.1
		}
	}
	// Normalize.
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec, nil
}

func (m *mockEmbedder) Dimensions() int { return 8 }

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB(:memory:) failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenDB_CreateAndMigrate(t *testing.T) {
	db := openTestDB(t)

	// Verify tables exist.
	expectedTables := []string{"chunks", "chunks_fts", "sessions", "conversation_events", "session_summaries", "scratchpads"}
	for _, table := range expectedTables {
		var name string
		err := db.Conn().QueryRow(
			`SELECT name FROM sqlite_master WHERE type IN ('table', 'shadow') AND name = ?`, table).Scan(&name)
		if err != nil {
			// FTS5 tables may not show directly; try a query.
			if table == "chunks_fts" {
				_, err = db.Conn().Exec(`SELECT * FROM chunks_fts LIMIT 0`)
				if err != nil {
					t.Errorf("table %q not accessible: %v", table, err)
				}
				continue
			}
			t.Errorf("table %q not found in sqlite_master: %v", table, err)
		}
	}
}

func TestStore_StoreAndRecall(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db, &mockEmbedder{}, types.RetrievalConfig{TopK: 5, VectorWeight: 0.7, FTSWeight: 0.3})
	ctx := context.Background()

	// Store 3 chunks.
	_, err := store.Store(ctx, types.MemoryStoreRequest{Content: "Go is a compiled language", Source: "test", Tags: []string{"programming"}})
	if err != nil {
		t.Fatalf("Store 1 failed: %v", err)
	}
	_, err = store.Store(ctx, types.MemoryStoreRequest{Content: "Python is an interpreted language", Source: "test", Tags: []string{"programming"}})
	if err != nil {
		t.Fatalf("Store 2 failed: %v", err)
	}
	_, err = store.Store(ctx, types.MemoryStoreRequest{Content: "Cats are fluffy animals", Source: "test", Tags: []string{"animals"}})
	if err != nil {
		t.Fatalf("Store 3 failed: %v", err)
	}

	// Recall with a query.
	results, err := store.Recall(ctx, "programming language", 5)
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}
	// At least some results should be returned.
	if len(results) > 5 {
		t.Errorf("expected at most 5 results, got %d", len(results))
	}
}

func TestStore_Forget(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db, &mockEmbedder{}, types.RetrievalConfig{TopK: 5, VectorWeight: 0.7, FTSWeight: 0.3})
	ctx := context.Background()

	id, err := store.Store(ctx, types.MemoryStoreRequest{Content: "temporary fact", Source: "test"})
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	if err := store.Forget(ctx, id); err != nil {
		t.Fatalf("Forget failed: %v", err)
	}

	// Verify the chunk is gone.
	var count int
	db.Conn().QueryRow(`SELECT COUNT(*) FROM chunks WHERE id = ?`, id).Scan(&count)
	if count != 0 {
		t.Errorf("expected chunk to be deleted, found %d", count)
	}

	db.Conn().QueryRow(`SELECT COUNT(*) FROM chunks_fts WHERE id = ?`, id).Scan(&count)
	if count != 0 {
		t.Errorf("expected FTS entry to be deleted, found %d", count)
	}
}

func TestStore_HybridQuery(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db, &mockEmbedder{}, types.RetrievalConfig{TopK: 5, VectorWeight: 0.5, FTSWeight: 0.5})
	ctx := context.Background()

	store.Store(ctx, types.MemoryStoreRequest{Content: "Go programming language features", Source: "test"})
	store.Store(ctx, types.MemoryStoreRequest{Content: "Rust memory safety", Source: "test"})
	store.Store(ctx, types.MemoryStoreRequest{Content: "JavaScript frontend development", Source: "test"})

	results, err := store.HybridQuery(ctx, types.HybridQueryRequest{
		Query:        "programming language",
		TopK:         3,
		VectorWeight: 0.5,
		FTSWeight:    0.5,
	})
	if err != nil {
		t.Fatalf("HybridQuery failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from hybrid query")
	}
	// Verify scores are positive.
	for _, r := range results {
		if r.Score <= 0 {
			t.Errorf("expected positive score, got %f for %q", r.Score, r.Content)
		}
	}
}

func TestStore_FTSOnly(t *testing.T) {
	db := openTestDB(t)
	// No embedder — FTS only.
	store := NewStore(db, nil, types.RetrievalConfig{TopK: 5, VectorWeight: 0, FTSWeight: 1.0})
	ctx := context.Background()

	store.Store(ctx, types.MemoryStoreRequest{Content: "machine learning algorithms", Source: "test"})
	store.Store(ctx, types.MemoryStoreRequest{Content: "deep learning neural networks", Source: "test"})
	store.Store(ctx, types.MemoryStoreRequest{Content: "cooking recipes for pasta", Source: "test"})

	results, err := store.Recall(ctx, "learning", 5)
	if err != nil {
		t.Fatalf("Recall (FTS only) failed: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results for 'learning', got %d", len(results))
	}
	// Verify learning-related content came back.
	found := false
	for _, r := range results {
		if r.Content == "machine learning algorithms" || r.Content == "deep learning neural networks" {
			found = true
		}
	}
	if !found {
		t.Error("expected learning-related chunks in results")
	}
}

func TestStore_SummaryRoundTrip(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db, nil, types.RetrievalConfig{})
	ctx := context.Background()

	// Ensure session exists.
	db.Conn().Exec(`INSERT INTO sessions (id, name) VALUES ('s1', 'test')`)

	err := store.SetSummary(ctx, "s1", types.SessionSummary{Summary: "User likes Go", Epoch: 1})
	if err != nil {
		t.Fatalf("SetSummary failed: %v", err)
	}

	summary, err := store.GetSummary(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSummary failed: %v", err)
	}
	if summary == nil {
		t.Fatal("expected summary, got nil")
	}
	if summary.Summary != "User likes Go" {
		t.Errorf("summary mismatch: %q", summary.Summary)
	}
	if summary.Epoch != 1 {
		t.Errorf("epoch mismatch: %d", summary.Epoch)
	}

	// Update summary.
	err = store.SetSummary(ctx, "s1", types.SessionSummary{Summary: "User likes Go and Rust", Epoch: 2})
	if err != nil {
		t.Fatalf("SetSummary (update) failed: %v", err)
	}
	summary, _ = store.GetSummary(ctx, "s1")
	if summary.Summary != "User likes Go and Rust" {
		t.Errorf("updated summary mismatch: %q", summary.Summary)
	}
}

func TestStore_ScratchpadRoundTrip(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db, nil, types.RetrievalConfig{})
	ctx := context.Background()

	db.Conn().Exec(`INSERT INTO sessions (id, name) VALUES ('s1', 'test')`)

	state := types.ScratchpadState{
		Data: map[string]string{"key1": "value1", "key2": "value2"},
	}
	err := store.SetScratchpad(ctx, "s1", state)
	if err != nil {
		t.Fatalf("SetScratchpad failed: %v", err)
	}

	got, err := store.GetScratchpad(ctx, "s1")
	if err != nil {
		t.Fatalf("GetScratchpad failed: %v", err)
	}
	if got.Data["key1"] != "value1" || got.Data["key2"] != "value2" {
		t.Errorf("scratchpad data mismatch: %v", got.Data)
	}
}

func TestStore_ConversationEvents(t *testing.T) {
	db := openTestDB(t)
	store := NewStore(db, nil, types.RetrievalConfig{})
	ctx := context.Background()

	db.Conn().Exec(`INSERT INTO sessions (id, name) VALUES ('s1', 'test')`)

	msgs := []types.Message{
		{Role: types.RoleUser, Content: "Hello"},
		{Role: types.RoleAssistant, Content: "Hi there!"},
		{Role: types.RoleUser, Content: "What is Go?"},
	}
	for _, msg := range msgs {
		if err := store.StoreConversationEvent(ctx, "s1", msg); err != nil {
			t.Fatalf("StoreConversationEvent failed: %v", err)
		}
	}

	history, err := store.GetConversationHistory(ctx, "s1", 10)
	if err != nil {
		t.Fatalf("GetConversationHistory failed: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 events, got %d", len(history))
	}
	if history[0].Content != "Hello" {
		t.Errorf("first event content: %q", history[0].Content)
	}
	if history[1].Role != types.RoleAssistant {
		t.Errorf("second event role: %q", history[1].Role)
	}
	if history[2].Content != "What is Go?" {
		t.Errorf("third event content: %q", history[2].Content)
	}

	// Verify session message count was bumped.
	var count int
	db.Conn().QueryRow(`SELECT message_count FROM sessions WHERE id = 's1'`).Scan(&count)
	if count != 3 {
		t.Errorf("expected message_count=3, got %d", count)
	}
}

func TestSessionStore_CRUD(t *testing.T) {
	db := openTestDB(t)
	ss := NewSessionStore(db)
	ctx := context.Background()

	// Create.
	rec, err := ss.Create(ctx, "test session")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if rec.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if rec.Name != "test session" {
		t.Errorf("name mismatch: %q", rec.Name)
	}

	// Get.
	got, err := ss.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Name != "test session" {
		t.Errorf("get name mismatch: %q", got.Name)
	}

	// List (no archived).
	list, err := ss.List(ctx, false)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 session, got %d", len(list))
	}

	// Update.
	got.Name = "renamed"
	got.MessageCount = 5
	if err := ss.Update(ctx, got); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	got2, _ := ss.Get(ctx, rec.ID)
	if got2.Name != "renamed" {
		t.Errorf("update name mismatch: %q", got2.Name)
	}
	if got2.MessageCount != 5 {
		t.Errorf("update count mismatch: %d", got2.MessageCount)
	}

	// Archive.
	if err := ss.Archive(ctx, rec.ID); err != nil {
		t.Fatalf("Archive failed: %v", err)
	}

	// List without archived should be empty.
	list, _ = ss.List(ctx, false)
	if len(list) != 0 {
		t.Errorf("expected 0 non-archived sessions, got %d", len(list))
	}

	// List with archived should include it.
	list, _ = ss.List(ctx, true)
	if len(list) != 1 {
		t.Errorf("expected 1 session (including archived), got %d", len(list))
	}
}

func TestCosineSimilarity(t *testing.T) {
	// Identical vectors -> similarity = 1.0
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	sim := cosineSimilarity(a, b)
	if math.Abs(sim-1.0) > 1e-6 {
		t.Errorf("identical vectors: expected 1.0, got %f", sim)
	}

	// Orthogonal vectors -> similarity = 0.0
	a = []float32{1, 0, 0}
	b = []float32{0, 1, 0}
	sim = cosineSimilarity(a, b)
	if math.Abs(sim) > 1e-6 {
		t.Errorf("orthogonal vectors: expected 0.0, got %f", sim)
	}

	// Opposite vectors -> similarity = -1.0
	a = []float32{1, 0, 0}
	b = []float32{-1, 0, 0}
	sim = cosineSimilarity(a, b)
	if math.Abs(sim+1.0) > 1e-6 {
		t.Errorf("opposite vectors: expected -1.0, got %f", sim)
	}

	// Empty vectors -> 0.
	sim = cosineSimilarity(nil, nil)
	if sim != 0 {
		t.Errorf("empty vectors: expected 0, got %f", sim)
	}
}

func TestEncodeDecodeFloat32s(t *testing.T) {
	original := []float32{1.0, -0.5, 3.14159, 0, 1e-7, 1e7}
	encoded := encodeFloat32s(original)

	if len(encoded) != len(original)*4 {
		t.Fatalf("encoded length: expected %d, got %d", len(original)*4, len(encoded))
	}

	decoded := decodeFloat32s(encoded)
	if len(decoded) != len(original) {
		t.Fatalf("decoded length: expected %d, got %d", len(original), len(decoded))
	}

	for i, v := range original {
		if decoded[i] != v {
			t.Errorf("index %d: expected %f, got %f", i, v, decoded[i])
		}
	}

	// Invalid input.
	bad := decodeFloat32s([]byte{1, 2, 3})
	if bad != nil {
		t.Errorf("expected nil for invalid input, got %v", bad)
	}
}
