package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hackertron/Yantra/internal/types"
)

func TestEditFile(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("hello world\nfoo bar\n"), 0644)

	tool := NewEditFile()

	execCtx := types.ToolExecutionContext{WorkspaceDir: dir}

	t.Run("basic replacement", func(t *testing.T) {
		input, _ := json.Marshal(map[string]string{
			"path":     "test.txt",
			"old_text": "hello world",
			"new_text": "goodbye world",
		})

		result, err := tool.Execute(context.Background(), input, execCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == "" {
			t.Fatal("expected non-empty result")
		}

		content, _ := os.ReadFile(testFile)
		if string(content) != "goodbye world\nfoo bar\n" {
			t.Errorf("unexpected content: %q", string(content))
		}
	})

	t.Run("old_text not found", func(t *testing.T) {
		input, _ := json.Marshal(map[string]string{
			"path":     "test.txt",
			"old_text": "nonexistent text",
			"new_text": "replacement",
		})

		_, err := tool.Execute(context.Background(), input, execCtx)
		if err == nil {
			t.Fatal("expected error for missing old_text")
		}
	})

	t.Run("empty old_text", func(t *testing.T) {
		input, _ := json.Marshal(map[string]string{
			"path":     "test.txt",
			"old_text": "",
			"new_text": "replacement",
		})

		_, err := tool.Execute(context.Background(), input, execCtx)
		if err == nil {
			t.Fatal("expected error for empty old_text")
		}
	})
}
