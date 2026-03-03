package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

type memorySearchTool struct {
	mem types.MemoryRetrieval
}

// NewMemorySearch creates a memory_search tool that queries persistent memory.
func NewMemorySearch(mem types.MemoryRetrieval) types.Tool {
	return &memorySearchTool{mem: mem}
}

func (t *memorySearchTool) Name() string        { return "memory_search" }
func (t *memorySearchTool) SafetyTier() types.SafetyTier { return types.ReadOnly }
func (t *memorySearchTool) Timeout() time.Duration       { return 15 * time.Second }

func (t *memorySearchTool) Description() string {
	return "Search persistent memory for stored knowledge. Returns relevant chunks ranked by relevance."
}

func (t *memorySearchTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: Schema(
			Prop{Name: "query", Type: TypeString, Description: "Search query", Required: true},
			Prop{Name: "top_k", Type: TypeInteger, Description: "Maximum number of results to return (default 5)", Required: false},
		),
	}
}

func (t *memorySearchTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	var args struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.TopK <= 0 {
		args.TopK = 5
	}

	chunks, err := t.mem.Recall(ctx, args.Query, args.TopK)
	if err != nil {
		return "", fmt.Errorf("memory search failed: %w", err)
	}

	if len(chunks) == 0 {
		return "No matching memories found.", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d memories:\n\n", len(chunks))
	for i, c := range chunks {
		fmt.Fprintf(&b, "%d. [score: %.4f] %s", i+1, c.Score, c.Content)
		if len(c.Tags) > 0 {
			fmt.Fprintf(&b, " (tags: %s)", strings.Join(c.Tags, ", "))
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
