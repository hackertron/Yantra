package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

type memorySaveTool struct {
	mem types.MemoryRetrieval
}

// NewMemorySave creates a memory_save tool that stores knowledge to persistent memory.
func NewMemorySave(mem types.MemoryRetrieval) types.Tool {
	return &memorySaveTool{mem: mem}
}

func (t *memorySaveTool) Name() string        { return "memory_save" }
func (t *memorySaveTool) SafetyTier() types.SafetyTier { return types.SideEffecting }
func (t *memorySaveTool) Timeout() time.Duration       { return 15 * time.Second }

func (t *memorySaveTool) Description() string {
	return "Save a piece of knowledge to persistent memory for future recall. Use this to remember important facts, preferences, or context."
}

func (t *memorySaveTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: Schema(
			Prop{Name: "content", Type: TypeString, Description: "The knowledge to store", Required: true},
			Prop{Name: "tags", Type: TypeArray, Description: "Optional tags for categorization", Required: false, Items: func() *SchemaType { s := TypeString; return &s }()},
		),
	}
}

func (t *memorySaveTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	var args struct {
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Content == "" {
		return "", fmt.Errorf("content is required")
	}

	id, err := t.mem.Store(ctx, types.MemoryStoreRequest{
		Content: args.Content,
		Source:  "user_saved",
		Tags:    args.Tags,
	})
	if err != nil {
		return "", fmt.Errorf("memory save failed: %w", err)
	}

	return fmt.Sprintf("Saved to memory (id: %s)", id), nil
}
