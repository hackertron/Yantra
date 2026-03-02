package memory

import (
	"context"
	"fmt"

	"github.com/hackertron/Yantra/internal/types"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// OpenAIEmbedder implements EmbeddingBackend using OpenAI's embedding API.
type OpenAIEmbedder struct {
	client *openai.Client
	model  string
	dims   int
}

// NewOpenAIEmbedder creates an OpenAI embedding backend.
func NewOpenAIEmbedder(apiKey, model string) (*OpenAIEmbedder, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("memory: OPENAI_API_KEY is required for OpenAI embeddings")
	}
	if model == "" {
		model = "text-embedding-3-small"
	}

	client := openai.NewClient(option.WithAPIKey(apiKey))

	// Dimension mapping for known models.
	dims := 1536
	switch model {
	case "text-embedding-3-small":
		dims = 1536
	case "text-embedding-3-large":
		dims = 3072
	case "text-embedding-ada-002":
		dims = 1536
	}

	return &OpenAIEmbedder{
		client: &client,
		model:  model,
		dims:   dims,
	}, nil
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := e.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(e.model),
		Input: openai.EmbeddingNewParamsInputUnion{
			OfString: openai.String(text),
		},
	})
	if err != nil {
		return nil, &types.MemoryError{Op: "embed", Message: "openai embedding failed", Err: err}
	}

	if len(resp.Data) == 0 {
		return nil, &types.MemoryError{Op: "embed", Message: "no embedding data returned"}
	}

	// Convert float64 → float32.
	f64 := resp.Data[0].Embedding
	out := make([]float32, len(f64))
	for i, v := range f64 {
		out[i] = float32(v)
	}
	return out, nil
}

func (e *OpenAIEmbedder) Dimensions() int {
	return e.dims
}

var _ types.EmbeddingBackend = (*OpenAIEmbedder)(nil)
