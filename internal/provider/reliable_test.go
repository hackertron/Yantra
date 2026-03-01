package provider

import (
	"context"
	"testing"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

// mockProvider is a test double that returns configurable results.
type mockProvider struct {
	completeFunc func(ctx context.Context, c *types.Context) (*types.Response, error)
	streamFunc   func(ctx context.Context, c *types.Context) <-chan types.StreamItem
	calls        int
}

func (m *mockProvider) ProviderID() types.ProviderID { return "mock" }
func (m *mockProvider) ModelID() types.ModelID       { return "mock-model" }
func (m *mockProvider) MaxContextTokens() int        { return 4096 }

func (m *mockProvider) Complete(ctx context.Context, c *types.Context) (*types.Response, error) {
	m.calls++
	return m.completeFunc(ctx, c)
}

func (m *mockProvider) Stream(ctx context.Context, c *types.Context) <-chan types.StreamItem {
	m.calls++
	return m.streamFunc(ctx, c)
}

func TestReliable_RetriesOnRetryableError(t *testing.T) {
	attempt := 0
	mock := &mockProvider{
		completeFunc: func(ctx context.Context, c *types.Context) (*types.Response, error) {
			attempt++
			if attempt < 3 {
				return nil, &types.ProviderError{Provider: "mock", Message: "rate limit", StatusCode: 429, Retryable: true}
			}
			return &types.Response{Message: types.Message{Content: "finally"}}, nil
		},
	}
	rp := NewReliable(mock, ReliableConfig{MaxAttempts: 3, BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond})
	resp, err := rp.Complete(context.Background(), &types.Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "finally" {
		t.Fatalf("expected 'finally', got %q", resp.Message.Content)
	}
}

func TestReliable_NoRetryOnNonRetryable(t *testing.T) {
	mock := &mockProvider{
		completeFunc: func(ctx context.Context, c *types.Context) (*types.Response, error) {
			return nil, &types.ProviderError{Provider: "mock", Message: "bad request", StatusCode: 400}
		},
	}
	rp := NewReliable(mock, ReliableConfig{MaxAttempts: 3, BackoffBase: time.Millisecond})
	_, err := rp.Complete(context.Background(), &types.Context{})
	if err == nil {
		t.Fatal("expected error")
	}
	if mock.calls != 1 {
		t.Fatalf("expected 1 call (no retry), got %d", mock.calls)
	}
}

func TestReliable_RespectsContextCancellation(t *testing.T) {
	mock := &mockProvider{
		completeFunc: func(ctx context.Context, c *types.Context) (*types.Response, error) {
			return nil, &types.ProviderError{Provider: "mock", Message: "server error", StatusCode: 500, Retryable: true}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	rp := NewReliable(mock, ReliableConfig{MaxAttempts: 5, BackoffBase: time.Second})
	_, err := rp.Complete(ctx, &types.Context{})
	if err == nil {
		t.Fatal("expected error")
	}
}
