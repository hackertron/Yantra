package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

// stubTool implements types.Tool for testing security policy.
type stubTool struct {
	name string
}

func (s *stubTool) Name() string                 { return s.name }
func (s *stubTool) Description() string          { return "stub" }
func (s *stubTool) Decl() types.FunctionDecl     { return types.FunctionDecl{Name: s.name} }
func (s *stubTool) SafetyTier() types.SafetyTier { return types.ReadOnly }
func (s *stubTool) Timeout() time.Duration       { return 0 }

func (s *stubTool) Execute(_ context.Context, _ json.RawMessage, _ types.ToolExecutionContext) (string, error) {
	return "", nil
}

func newExecCtx(workspace string) types.ToolExecutionContext {
	return types.ToolExecutionContext{WorkspaceDir: workspace}
}

func TestWorkspacePolicy_PathTraversalBlocked(t *testing.T) {
	policy := NewWorkspacePolicy(types.ShellConfig{})
	tool := &stubTool{name: "read_file"}
	input := mustJSON(t, map[string]string{"path": "../../etc/passwd"})

	err := policy.CheckExecution(tool, input, newExecCtx("/home/user/project"))
	if err == nil {
		t.Fatal("expected path traversal to be blocked")
	}
}

func TestWorkspacePolicy_AbsolutePathOutsideBlocked(t *testing.T) {
	policy := NewWorkspacePolicy(types.ShellConfig{})
	tool := &stubTool{name: "write_file"}
	input := mustJSON(t, map[string]string{"path": "/etc/passwd"})

	err := policy.CheckExecution(tool, input, newExecCtx("/home/user/project"))
	if err == nil {
		t.Fatal("expected absolute path outside workspace to be blocked")
	}
}

func TestWorkspacePolicy_RelativePathInsideAllowed(t *testing.T) {
	policy := NewWorkspacePolicy(types.ShellConfig{})
	tool := &stubTool{name: "read_file"}
	input := mustJSON(t, map[string]string{"path": "src/main.go"})

	err := policy.CheckExecution(tool, input, newExecCtx("/home/user/project"))
	if err != nil {
		t.Fatalf("expected relative path inside workspace to be allowed: %v", err)
	}
}

func TestWorkspacePolicy_ShellAllowlist(t *testing.T) {
	policy := NewWorkspacePolicy(types.ShellConfig{})
	tool := &stubTool{name: "shell_exec"}
	input := mustJSON(t, map[string]string{"command": "ls -la"})

	err := policy.CheckExecution(tool, input, newExecCtx("/tmp"))
	if err != nil {
		t.Fatalf("expected allowed command: %v", err)
	}
}

func TestWorkspacePolicy_ShellDenylist(t *testing.T) {
	policy := NewWorkspacePolicy(types.ShellConfig{})
	tool := &stubTool{name: "shell_exec"}
	input := mustJSON(t, map[string]string{"command": "sudo rm -rf /"})

	err := policy.CheckExecution(tool, input, newExecCtx("/tmp"))
	if err == nil {
		t.Fatal("expected denied command to be blocked")
	}
}

func TestWorkspacePolicy_DenyOverridesAllow(t *testing.T) {
	policy := NewWorkspacePolicy(types.ShellConfig{
		Allow: []string{"badcmd"},
		Deny:  []string{"badcmd"},
	})
	tool := &stubTool{name: "shell_exec"}
	input := mustJSON(t, map[string]string{"command": "badcmd foo"})

	err := policy.CheckExecution(tool, input, newExecCtx("/tmp"))
	if err == nil {
		t.Fatal("expected deny to override allow")
	}
}

func TestWorkspacePolicy_OperatorsBlocked(t *testing.T) {
	policy := NewWorkspacePolicy(types.ShellConfig{})
	tool := &stubTool{name: "shell_exec"}

	operators := []string{
		"ls | grep foo",
		"cmd1 && cmd2",
		"cmd1 || cmd2",
		"cmd1 ; cmd2",
		"echo foo > file",
	}

	for _, cmd := range operators {
		input := mustJSON(t, map[string]string{"command": cmd})
		err := policy.CheckExecution(tool, input, newExecCtx("/tmp"))
		if err == nil {
			t.Errorf("expected operator in %q to be blocked", cmd)
		}
	}
}

func TestWorkspacePolicy_OperatorsAllowed(t *testing.T) {
	policy := NewWorkspacePolicy(types.ShellConfig{AllowOperators: true})
	tool := &stubTool{name: "shell_exec"}
	input := mustJSON(t, map[string]string{"command": "ls | grep foo"})

	err := policy.CheckExecution(tool, input, newExecCtx("/tmp"))
	if err != nil {
		t.Fatalf("expected operators to be allowed: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return b
}
