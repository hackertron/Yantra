package types

import "errors"

// Sentinel errors for the runtime.
var (
	ErrCancelled        = errors.New("turn cancelled")
	ErrBudgetExceeded   = errors.New("budget exceeded")
	ErrMaxTurns         = errors.New("max turns reached")
	ErrTimeout          = errors.New("turn timed out")
	ErrSessionNotFound  = errors.New("session not found")
)

// ProviderError represents an error from an LLM provider.
type ProviderError struct {
	Provider   string
	StatusCode int
	Message    string
	Retryable  bool
	Err        error
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return e.Provider + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Provider + ": " + e.Message
}

func (e *ProviderError) Unwrap() error { return e.Err }

// ToolError represents a tool execution failure.
type ToolError struct {
	Tool    string
	Message string
	Err     error
}

func (e *ToolError) Error() string {
	if e.Err != nil {
		return "tool " + e.Tool + ": " + e.Message + ": " + e.Err.Error()
	}
	return "tool " + e.Tool + ": " + e.Message
}

func (e *ToolError) Unwrap() error { return e.Err }

// MemoryError represents a memory operation failure.
type MemoryError struct {
	Op      string
	Message string
	Err     error
}

func (e *MemoryError) Error() string {
	if e.Err != nil {
		return "memory " + e.Op + ": " + e.Message + ": " + e.Err.Error()
	}
	return "memory " + e.Op + ": " + e.Message
}

func (e *MemoryError) Unwrap() error { return e.Err }

// GatewayError represents a gateway-level error.
type GatewayError struct {
	Code    string
	Message string
	Err     error
}

func (e *GatewayError) Error() string {
	if e.Err != nil {
		return "gateway [" + e.Code + "]: " + e.Message + ": " + e.Err.Error()
	}
	return "gateway [" + e.Code + "]: " + e.Message
}

func (e *GatewayError) Unwrap() error { return e.Err }
