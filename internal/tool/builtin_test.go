package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackertron/Yantra/internal/types"
)

func execCtx(t *testing.T) (types.ToolExecutionContext, string) {
	t.Helper()
	dir := t.TempDir()
	return types.ToolExecutionContext{WorkspaceDir: dir}, dir
}

// --- read_file ---

func TestReadFile_Basic(t *testing.T) {
	ctx, dir := execCtx(t)
	content := "line1\nline2\nline3\n"
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644)

	tool := NewReadFile()
	input, _ := json.Marshal(map[string]any{"path": "test.txt"})
	result, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "line1") || !strings.Contains(result, "line3") {
		t.Errorf("expected all lines, got: %s", result)
	}
	// Check line numbers are present.
	if !strings.Contains(result, "1\t") {
		t.Error("expected line numbers in output")
	}
}

func TestReadFile_OffsetAndLimit(t *testing.T) {
	ctx, dir := execCtx(t)
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	tool := NewReadFile()
	input, _ := json.Marshal(map[string]any{"path": "test.txt", "offset": 3, "limit": 2})
	result, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "line3") || !strings.Contains(result, "line4") {
		t.Errorf("expected lines 3-4, got: %s", result)
	}
	if strings.Contains(result, "line5") {
		t.Error("should not contain line5 with limit=2")
	}
}

func TestReadFile_NotFound(t *testing.T) {
	ctx, _ := execCtx(t)
	tool := NewReadFile()
	input, _ := json.Marshal(map[string]string{"path": "nonexistent.txt"})
	_, err := tool.Execute(context.Background(), input, ctx)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// --- write_file ---

func TestWriteFile_Create(t *testing.T) {
	ctx, dir := execCtx(t)
	tool := NewWriteFile()
	input, _ := json.Marshal(map[string]any{"path": "out.txt", "content": "hello world"})
	result, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "wrote") {
		t.Errorf("expected 'wrote' in result, got: %s", result)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if string(data) != "hello world" {
		t.Errorf("file content mismatch: %q", data)
	}
}

func TestWriteFile_Append(t *testing.T) {
	ctx, dir := execCtx(t)
	os.WriteFile(filepath.Join(dir, "log.txt"), []byte("first\n"), 0o644)

	tool := NewWriteFile()
	input, _ := json.Marshal(map[string]any{"path": "log.txt", "content": "second\n", "append": true})
	_, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "log.txt"))
	if string(data) != "first\nsecond\n" {
		t.Errorf("expected appended content, got: %q", data)
	}
}

func TestWriteFile_MkdirP(t *testing.T) {
	ctx, dir := execCtx(t)
	tool := NewWriteFile()
	input, _ := json.Marshal(map[string]any{"path": "a/b/c/file.txt", "content": "nested"})
	_, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "a", "b", "c", "file.txt"))
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got: %q", data)
	}
}

// --- list_files ---

func TestListFiles_NonRecursive(t *testing.T) {
	ctx, dir := execCtx(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	tool := NewListFiles()
	input, _ := json.Marshal(map[string]any{"path": "."})
	result, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "a.txt") {
		t.Error("expected a.txt in listing")
	}
	if !strings.Contains(result, "subdir/") {
		t.Error("expected subdir/ in listing")
	}
}

func TestListFiles_Recursive(t *testing.T) {
	ctx, dir := execCtx(t)
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "file.txt"), []byte("f"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "b", "deep.txt"), []byte("d"), 0o644)

	tool := NewListFiles()
	input, _ := json.Marshal(map[string]any{"path": ".", "recursive": true, "max_depth": 3})
	result, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "file.txt") {
		t.Error("expected file.txt in recursive listing")
	}
	if !strings.Contains(result, "deep.txt") {
		t.Error("expected deep.txt in recursive listing")
	}
}

// --- shell_exec ---

func TestShellExec_Echo(t *testing.T) {
	ctx, _ := execCtx(t)
	tool := NewShellExec()
	input, _ := json.Marshal(map[string]string{"command": "echo hello"})
	result, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "exit_code: 0") {
		t.Errorf("expected exit_code: 0, got: %s", result)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected 'hello' in output, got: %s", result)
	}
}

func TestShellExec_ExitCode(t *testing.T) {
	ctx, _ := execCtx(t)
	tool := NewShellExec()
	input, _ := json.Marshal(map[string]string{"command": "exit 42"})
	result, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "exit_code: 42") {
		t.Errorf("expected exit_code: 42, got: %s", result)
	}
}

// --- web_fetch ---

func TestWebFetch_HTTPTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "hello from server")
	}))
	defer srv.Close()

	ctx, _ := execCtx(t)
	tool := NewWebFetch()
	input, _ := json.Marshal(map[string]string{"url": srv.URL})
	result, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "status: 200") {
		t.Errorf("expected status 200, got: %s", result)
	}
	if !strings.Contains(result, "hello from server") {
		t.Errorf("expected body, got: %s", result)
	}
}

func TestWebFetch_POST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, "created")
	}))
	defer srv.Close()

	ctx, _ := execCtx(t)
	tool := NewWebFetch()
	input, _ := json.Marshal(map[string]any{"url": srv.URL, "method": "POST", "body": `{"key":"val"}`})
	result, err := tool.Execute(context.Background(), input, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "status: 201") {
		t.Errorf("expected status 201, got: %s", result)
	}
}

// --- RegisterBuiltins ---

func TestRegisterBuiltins(t *testing.T) {
	policy := NewWorkspacePolicy(types.ShellConfig{})
	r := NewRegistry(policy)
	if err := RegisterBuiltins(r, types.ToolsConfig{}); err != nil {
		t.Fatalf("RegisterBuiltins failed: %v", err)
	}

	expected := []string{"list_files", "read_file", "shell_exec", "web_fetch", "write_file"}
	got := r.List()
	if len(got) != len(expected) {
		t.Fatalf("expected %d tools, got %d: %v", len(expected), len(got), got)
	}
	for i, name := range expected {
		if got[i] != name {
			t.Errorf("tool[%d]: expected %q, got %q", i, name, got[i])
		}
	}
}
