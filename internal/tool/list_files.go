package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

const listFilesTimeout = 10 * time.Second

type listFilesTool struct{}

func NewListFiles() types.Tool { return &listFilesTool{} }

func (t *listFilesTool) Name() string        { return "list_files" }
func (t *listFilesTool) SafetyTier() types.SafetyTier { return types.ReadOnly }
func (t *listFilesTool) Timeout() time.Duration       { return listFilesTimeout }

func (t *listFilesTool) Description() string {
	return "List files and directories at a given path. Optionally recurse with a max depth."
}

func (t *listFilesTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: Schema(
			Prop{Name: "path", Type: TypeString, Description: "Directory path (relative to workspace or absolute)", Required: true},
			Prop{Name: "recursive", Type: TypeBoolean, Description: "List recursively (default false)", Required: false},
			Prop{Name: "max_depth", Type: TypeInteger, Description: "Maximum recursion depth (default 3, only used when recursive is true)", Required: false},
		),
	}
}

func (t *listFilesTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	var args struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		MaxDepth  int    `json:"max_depth"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	resolved := resolvePath(args.Path, execCtx.WorkspaceDir)

	if !args.Recursive {
		return listFlat(ctx, resolved)
	}

	maxDepth := args.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	return listRecursive(ctx, resolved, maxDepth)
}

func listFlat(ctx context.Context, dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("cannot read directory: %w", err)
	}

	var b strings.Builder
	for _, e := range entries {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func listRecursive(ctx context.Context, root string, maxDepth int) (string, error) {
	var b strings.Builder
	rootClean := filepath.Clean(root)

	err := filepath.WalkDir(rootClean, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil // skip entries we can't read
		}

		rel, _ := filepath.Rel(rootClean, path)
		if rel == "." {
			return nil
		}

		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		name := rel
		if d.IsDir() {
			name += "/"
		}
		b.WriteString(name)
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk error: %w", err)
	}
	return b.String(), nil
}
