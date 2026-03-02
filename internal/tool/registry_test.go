package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

// blockingPolicy denies all tool executions.
type blockingPolicy struct{}

func (p *blockingPolicy) CheckExecution(_ types.Tool, _ json.RawMessage, _ types.ToolExecutionContext) error {
	return errors.New("blocked by policy")
}

func TestRegistry_DuplicateRegistration(t *testing.T) {
	r := NewRegistry(nil)
	tool := &stubTool{name: "dup"}
	if err := r.Register(tool); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if err := r.Register(tool); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
}

func TestRegistry_GetAndList(t *testing.T) {
	r := NewRegistry(nil)
	_ = r.Register(&stubTool{name: "beta"})
	_ = r.Register(&stubTool{name: "alpha"})

	if r.Get("alpha") == nil {
		t.Error("expected to find 'alpha'")
	}
	if r.Get("missing") != nil {
		t.Error("expected nil for missing tool")
	}

	list := r.List()
	if len(list) != 2 || list[0] != "alpha" || list[1] != "beta" {
		t.Errorf("expected sorted [alpha beta], got %v", list)
	}
}

func TestRegistry_SchemasWithFilter(t *testing.T) {
	r := NewRegistry(nil)
	_ = r.Register(NewReadFile())
	_ = r.Register(NewWriteFile())
	_ = r.Register(NewListFiles())

	// No filter returns all.
	all := r.Schemas(nil)
	if len(all) != 3 {
		t.Errorf("expected 3 schemas, got %d", len(all))
	}

	// Filter to specific tools.
	filtered := r.Schemas([]string{"read_file", "list_files"})
	if len(filtered) != 2 {
		t.Errorf("expected 2 filtered schemas, got %d", len(filtered))
	}

	names := make(map[string]bool)
	for _, d := range filtered {
		names[d.Name] = true
	}
	if !names["read_file"] || !names["list_files"] {
		t.Errorf("unexpected filtered result: %v", names)
	}
}

func TestRegistry_ExecuteWithPolicyBlock(t *testing.T) {
	r := NewRegistry(&blockingPolicy{})
	_ = r.Register(&stubTool{name: "test"})

	_, err := r.Execute(context.Background(), "test", []byte(`{}`), types.ToolExecutionContext{})
	if err == nil {
		t.Fatal("expected policy block error")
	}
	var toolErr *types.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected ToolError, got %T: %v", err, err)
	}
	if toolErr.Tool != "test" {
		t.Errorf("expected tool name 'test', got %q", toolErr.Tool)
	}
}

func TestRegistry_ExecuteNotFound(t *testing.T) {
	r := NewRegistry(nil)
	_, err := r.Execute(context.Background(), "nonexistent", []byte(`{}`), types.ToolExecutionContext{})
	if err == nil {
		t.Fatal("expected not found error")
	}
}

// slowTool sleeps for a duration then returns.
type slowTool struct {
	stubTool
	delay time.Duration
}

func (s *slowTool) Timeout() time.Duration { return 50 * time.Millisecond }

func (s *slowTool) Execute(ctx context.Context, _ json.RawMessage, _ types.ToolExecutionContext) (string, error) {
	select {
	case <-time.After(s.delay):
		return "done", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestRegistry_ExecuteWithTimeout(t *testing.T) {
	r := NewRegistry(nil)
	_ = r.Register(&slowTool{stubTool: stubTool{name: "slow"}, delay: 500 * time.Millisecond})

	_, err := r.Execute(context.Background(), "slow", []byte(`{}`), types.ToolExecutionContext{})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRegistry_OutputTruncation(t *testing.T) {
	r := NewRegistry(nil)
	r.SetMaxOutputBytes(50)

	// Create a tool that returns a long output.
	big := &outputTool{stubTool: stubTool{name: "big"}, output: strings.Repeat("line\n", 100)}
	_ = r.Register(big)

	result, err := r.Execute(context.Background(), "big", []byte(`{}`), types.ToolExecutionContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[output truncated]") {
		t.Error("expected truncation marker")
	}
	if len(result) > 100 { // generous margin for the truncation message
		t.Errorf("output too large after truncation: %d bytes", len(result))
	}
}

// outputTool returns a fixed output string.
type outputTool struct {
	stubTool
	output string
}

func (o *outputTool) Execute(_ context.Context, _ json.RawMessage, _ types.ToolExecutionContext) (string, error) {
	return o.output, nil
}
