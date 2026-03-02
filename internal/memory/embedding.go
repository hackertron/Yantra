package memory

import (
	"fmt"
	"os"

	"github.com/hackertron/Yantra/internal/types"
)

// NewEmbeddingBackend creates an EmbeddingBackend based on config.
// Returns nil (not an error) if no embedding backend can be configured.
func NewEmbeddingBackend(cfg types.MemoryConfig) (types.EmbeddingBackend, error) {
	switch cfg.EmbeddingBackend {
	case "openai", "":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return nil, nil // graceful: no embeddings available
		}
		model := cfg.Embedding.Model
		return NewOpenAIEmbedder(apiKey, model)
	default:
		return nil, fmt.Errorf("memory: unsupported embedding backend: %q", cfg.EmbeddingBackend)
	}
}
