package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

const writeFileTimeout = 10 * time.Second

type writeFileTool struct{}

func NewWriteFile() types.Tool { return &writeFileTool{} }

func (t *writeFileTool) Name() string        { return "write_file" }
func (t *writeFileTool) SafetyTier() types.SafetyTier { return types.SideEffecting }
func (t *writeFileTool) Timeout() time.Duration       { return writeFileTimeout }

func (t *writeFileTool) Description() string {
	return "Write content to a file. Creates parent directories if needed. Use append mode to add to existing files."
}

func (t *writeFileTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: Schema(
			Prop{Name: "path", Type: TypeString, Description: "File path (relative to workspace or absolute)", Required: true},
			Prop{Name: "content", Type: TypeString, Description: "Content to write", Required: true},
			Prop{Name: "append", Type: TypeBoolean, Description: "Append to file instead of overwriting (default false)", Required: false},
		),
	}
}

func (t *writeFileTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Append  bool   `json:"append"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	resolved, err := ResolvePath(args.Path, execCtx.WorkspaceDir)
	if err != nil {
		return "", err
	}

	// Create parent directories.
	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create directory %q: %w", dir, err)
	}

	flag := os.O_WRONLY | os.O_CREATE
	if args.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	f, err := os.OpenFile(resolved, flag, 0o644)
	if err != nil {
		return "", fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()

	n, err := f.WriteString(args.Content)
	if err != nil {
		return "", fmt.Errorf("write error: %w", err)
	}

	mode := "wrote"
	if args.Append {
		mode = "appended"
	}
	return fmt.Sprintf("%s %d bytes to %s", mode, n, args.Path), nil
}
