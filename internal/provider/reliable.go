package provider

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

// ReliableConfig controls retry behavior.
type ReliableConfig struct {
	MaxAttempts int
	BackoffBase time.Duration
	BackoffMax  time.Duration
	Jitter      bool
}

// DefaultReliableConfig returns sensible retry defaults.
func DefaultReliableConfig() ReliableConfig {
	return ReliableConfig{
		MaxAttempts: 3,
		BackoffBase: 250 * time.Millisecond,
		BackoffMax:  2 * time.Second,
		Jitter:      true,
	}
}

// ReliableProvider wraps any Provider with retry logic for transient failures.
type ReliableProvider struct {
	inner  types.Provider
	config ReliableConfig
}

// NewReliable wraps a provider with retry/backoff.
func NewReliable(inner types.Provider, cfg ReliableConfig) *ReliableProvider {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	return &ReliableProvider{inner: inner, config: cfg}
}

func (r *ReliableProvider) ProviderID() types.ProviderID { return r.inner.ProviderID() }
func (r *ReliableProvider) ModelID() types.ModelID       { return r.inner.ModelID() }
func (r *ReliableProvider) MaxContextTokens() int        { return r.inner.MaxContextTokens() }

func (r *ReliableProvider) Complete(ctx context.Context, c *types.Context) (*types.Response, error) {
	var lastErr error
	for attempt := range r.config.MaxAttempts {
		resp, err := r.inner.Complete(ctx, c)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}
		if attempt < r.config.MaxAttempts-1 {
			delay := r.backoff(attempt)
			slog.Warn("provider call failed, retrying",
				"provider", r.inner.ProviderID(),
				"attempt", attempt+1,
				"delay", delay,
				"error", err,
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil, lastErr
}

func (r *ReliableProvider) Stream(ctx context.Context, c *types.Context) <-chan types.StreamItem {
	ch := make(chan types.StreamItem, 64)
	go func() {
		defer close(ch)

		var lastErr error
		for attempt := range r.config.MaxAttempts {
			innerCh := r.inner.Stream(ctx, c)

			first, ok := <-innerCh
			if !ok {
				lastErr = &types.ProviderError{
					Provider:  string(r.inner.ProviderID()),
					Message:   "stream closed immediately",
					Retryable: true,
				}
				if attempt < r.config.MaxAttempts-1 {
					r.sleepOrCancel(ctx, ch, attempt)
					continue
				}
				ch <- types.StreamItem{Type: types.StreamError, Error: lastErr}
				return
			}

			if first.Type == types.StreamError && isRetryable(first.Error) && attempt < r.config.MaxAttempts-1 {
				lastErr = first.Error
				for range innerCh { // drain
				}
				r.sleepOrCancel(ctx, ch, attempt)
				continue
			}

			// Stream started — forward everything
			ch <- first
			for item := range innerCh {
				ch <- item
			}
			return
		}

		ch <- types.StreamItem{Type: types.StreamError, Error: lastErr}
	}()
	return ch
}

func (r *ReliableProvider) sleepOrCancel(ctx context.Context, ch chan<- types.StreamItem, attempt int) {
	delay := r.backoff(attempt)
	slog.Warn("stream failed, retrying",
		"provider", r.inner.ProviderID(),
		"attempt", attempt+1,
		"delay", delay,
	)
	select {
	case <-ctx.Done():
		ch <- types.StreamItem{Type: types.StreamError, Error: ctx.Err()}
	case <-time.After(delay):
	}
}

func (r *ReliableProvider) backoff(attempt int) time.Duration {
	delay := r.config.BackoffBase * time.Duration(math.Pow(2, float64(attempt)))
	if delay > r.config.BackoffMax {
		delay = r.config.BackoffMax
	}
	if r.config.Jitter {
		jitter := time.Duration(rand.Int64N(int64(delay / 4)))
		delay += jitter
	}
	return delay
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	var pe *types.ProviderError
	if errors.As(err, &pe) {
		if pe.Retryable {
			return true
		}
		if pe.StatusCode == 429 || pe.StatusCode >= 500 {
			return true
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "timeout")
}

var _ types.Provider = (*ReliableProvider)(nil)
