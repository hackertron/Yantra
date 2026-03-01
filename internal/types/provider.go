package types

import "context"

// ModelID is a typed wrapper for model identifiers.
type ModelID string

// ProviderID is a typed wrapper for provider identifiers.
type ProviderID string

// Provider is the interface that all LLM providers must implement.
type Provider interface {
	// Complete sends a context to the LLM and returns the full response.
	Complete(ctx context.Context, c *Context) (*Response, error)

	// Stream sends a context to the LLM and returns a channel of stream items.
	Stream(ctx context.Context, c *Context) <-chan StreamItem

	// ProviderID returns the unique identifier for this provider instance.
	ProviderID() ProviderID

	// ModelID returns the model being used.
	ModelID() ModelID

	// MaxContextTokens returns the maximum context window size for the current model.
	MaxContextTokens() int
}

// ProviderType identifies the kind of LLM API.
type ProviderType string

const (
	ProviderOpenAI          ProviderType = "openai"
	ProviderAnthropic       ProviderType = "anthropic"
	ProviderGemini          ProviderType = "gemini"
	ProviderOpenAIResponses ProviderType = "openai_responses"
)
