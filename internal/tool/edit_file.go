package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

const editFileTimeout = 10 * time.Second

type editFileTool struct{}

func NewEditFile() types.Tool { return &editFileTool{} }

func (t *editFileTool) Name() string                  { return "file_edit" }
func (t *editFileTool) SafetyTier() types.SafetyTier  { return types.SideEffecting }
func (t *editFileTool) Timeout() time.Duration         { return editFileTimeout }

func (t *editFileTool) Description() string {
	return "Find and replace text in a file. Replaces the first occurrence of old_text with new_text. Use for surgical edits instead of rewriting entire files."
}

func (t *editFileTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: Schema(
			Prop{Name: "path", Type: TypeString, Description: "File path (relative to workspace or absolute)", Required: true},
			Prop{Name: "old_text", Type: TypeString, Description: "Exact text to find (must match exactly, including whitespace)", Required: true},
			Prop{Name: "new_text", Type: TypeString, Description: "Replacement text", Required: true},
		),
	}
}

func (t *editFileTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.OldText == "" {
		return "", fmt.Errorf("old_text is required and must not be empty")
	}

	resolved, err := ResolvePath(args.Path, execCtx.WorkspaceDir)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}

	original := string(content)
	if !strings.Contains(original, args.OldText) {
		return "", fmt.Errorf("old_text not found in %s", args.Path)
	}

	// Count occurrences for informational message.
	count := strings.Count(original, args.OldText)

	// Replace first occurrence only.
	updated := strings.Replace(original, args.OldText, args.NewText, 1)

	if err := os.WriteFile(resolved, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("cannot write file: %w", err)
	}

	msg := fmt.Sprintf("edited %s: replaced 1 occurrence", args.Path)
	if count > 1 {
		msg += fmt.Sprintf(" (%d total found, only first replaced)", count)
	}
	return msg, nil
}
