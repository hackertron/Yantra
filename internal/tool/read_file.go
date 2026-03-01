package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

const (
	readFileDefaultLimit = 2000
	readFileTimeout      = 10 * time.Second
)

type readFileTool struct{}

func NewReadFile() types.Tool { return &readFileTool{} }

func (t *readFileTool) Name() string        { return "read_file" }
func (t *readFileTool) SafetyTier() types.SafetyTier { return types.ReadOnly }
func (t *readFileTool) Timeout() time.Duration       { return readFileTimeout }

func (t *readFileTool) Description() string {
	return "Read a file's contents with line numbers. Supports offset and limit parameters."
}

func (t *readFileTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: Schema(
			Prop{Name: "path", Type: TypeString, Description: "File path (relative to workspace or absolute)", Required: true},
			Prop{Name: "offset", Type: TypeInteger, Description: "Line number to start reading from (1-based, default 1)", Required: false},
			Prop{Name: "limit", Type: TypeInteger, Description: "Maximum number of lines to read (default 2000)", Required: false},
		),
	}
}

func (t *readFileTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	resolved := resolvePath(args.Path, execCtx.WorkspaceDir)

	f, err := os.Open(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()

	offset := args.Offset
	if offset < 1 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 {
		limit = readFileDefaultLimit
	}

	var b strings.Builder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNum := 0
	linesRead := 0
	for scanner.Scan() {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		lineNum++
		if lineNum < offset {
			continue
		}
		if linesRead >= limit {
			break
		}
		fmt.Fprintf(&b, "%6d\t%s\n", lineNum, scanner.Text())
		linesRead++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read error: %w", err)
	}

	if linesRead == 0 {
		return "(empty file or offset beyond end of file)", nil
	}
	return b.String(), nil
}

// resolvePath resolves a tool path argument relative to the workspace.
func resolvePath(path, workspace string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(workspace, path))
}
