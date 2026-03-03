package gateway

import (
	"context"
	"log/slog"
	"testing"

	"github.com/hackertron/Yantra/internal/memory"
	"github.com/hackertron/Yantra/internal/tool"
	"github.com/hackertron/Yantra/internal/types"
)

// newTestServer creates a Server backed by an in-memory SQLite database.
func newTestServer(t *testing.T, cfg types.GatewayConfig) *Server {
	t.Helper()

	db, err := memory.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	sessStore := memory.NewSessionStore(db)
	prov := &stubProvider{}
	reg := tool.NewRegistry(nil)

	fullCfg := types.DefaultConfig()
	fullCfg.Gateway = cfg

	return NewServer(cfg, &fullCfg, prov, reg, nil, sessStore, ".", slog.Default())
}

// stubProvider is a minimal provider for session tests that don't run turns.
type stubProvider struct{}

func (p *stubProvider) ProviderID() types.ProviderID  { return "stub" }
func (p *stubProvider) ModelID() types.ModelID         { return "stub-model" }
func (p *stubProvider) MaxContextTokens() int          { return 128000 }
func (p *stubProvider) Complete(ctx context.Context, c *types.Context) (*types.Response, error) {
	return &types.Response{Message: types.Message{Role: types.RoleAssistant, Content: "ok"}}, nil
}
func (p *stubProvider) Stream(ctx context.Context, c *types.Context) <-chan types.StreamItem {
	ch := make(chan types.StreamItem, 2)
	go func() {
		defer close(ch)
		ch <- types.StreamItem{Type: types.StreamText, Text: "ok"}
		ch <- types.StreamItem{Type: types.StreamDone, Usage: &types.Usage{TotalTokens: 1}}
	}()
	return ch
}

func TestSessionManager_GetOrCreate_NewSession(t *testing.T) {
	s := newTestServer(t, types.GatewayConfig{MaxSessions: 10, MaxConcurrentTurns: 5})

	// Create a new session via the session store so we have a valid ID.
	ctx := context.Background()
	ms, err := s.sessMgr.newSession(ctx, "test")
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	if ms.Record.ID == "" {
		t.Fatal("expected non-empty session ID")
	}

	// GetOrCreate with same ID should return cached instance.
	ms2, err := s.sessMgr.GetOrCreate(ctx, ms.Record.ID)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if ms2 != ms {
		t.Error("expected same ManagedSession instance from cache")
	}
}

func TestSessionManager_MaxSessions(t *testing.T) {
	s := newTestServer(t, types.GatewayConfig{MaxSessions: 2, MaxConcurrentTurns: 5})
	ctx := context.Background()

	_, err := s.sessMgr.newSession(ctx, "s1")
	if err != nil {
		t.Fatalf("s1: %v", err)
	}
	_, err = s.sessMgr.newSession(ctx, "s2")
	if err != nil {
		t.Fatalf("s2: %v", err)
	}

	_, err = s.sessMgr.newSession(ctx, "s3")
	if err == nil {
		t.Fatal("expected error when exceeding MaxSessions")
	}
}

func TestSessionManager_AcquireTurnSlot(t *testing.T) {
	s := newTestServer(t, types.GatewayConfig{MaxSessions: 10, MaxConcurrentTurns: 2})

	ctx := context.Background()

	if !s.sessMgr.AcquireTurnSlot(ctx) {
		t.Fatal("expected slot 1 to be acquired")
	}
	if !s.sessMgr.AcquireTurnSlot(ctx) {
		t.Fatal("expected slot 2 to be acquired")
	}

	// Third slot should block; use a cancelled context to verify.
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if s.sessMgr.AcquireTurnSlot(cancelCtx) {
		t.Fatal("expected slot acquisition to fail with cancelled context")
	}

	// Release one and try again.
	s.sessMgr.ReleaseTurnSlot()
	if !s.sessMgr.AcquireTurnSlot(ctx) {
		t.Fatal("expected slot to be available after release")
	}

	// Clean up.
	s.sessMgr.ReleaseTurnSlot()
	s.sessMgr.ReleaseTurnSlot()
}
