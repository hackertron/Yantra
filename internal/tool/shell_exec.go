package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

const shellExecTimeout = 60 * time.Second

type shellExecTool struct{}

func NewShellExec() types.Tool { return &shellExecTool{} }

func (t *shellExecTool) Name() string        { return "shell_exec" }
func (t *shellExecTool) SafetyTier() types.SafetyTier { return types.Privileged }
func (t *shellExecTool) Timeout() time.Duration       { return shellExecTimeout }

func (t *shellExecTool) Description() string {
	return "Execute a shell command and return its stdout, stderr, and exit code."
}

func (t *shellExecTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: Schema(
			Prop{Name: "command", Type: TypeString, Description: "Shell command to execute", Required: true},
		),
	}
}

func (t *shellExecTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", args.Command)
	cmd.Dir = execCtx.WorkspaceDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var result string
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("exec error: %w", err)
		}
	}

	result = fmt.Sprintf("exit_code: %d\n", exitCode)
	if stdout.Len() > 0 {
		result += "stdout:\n" + stdout.String()
	}
	if stderr.Len() > 0 {
		result += "stderr:\n" + stderr.String()
	}

	return result, nil
}
