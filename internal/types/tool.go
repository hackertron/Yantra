package types

import (
	"context"
	"encoding/json"
	"time"
)

// SafetyTier categorizes tools by their side-effect risk.
type SafetyTier int

const (
	// ReadOnly tools have no side effects and can be executed in parallel.
	ReadOnly SafetyTier = iota
	// SideEffecting tools modify state and must be executed sequentially.
	SideEffecting
	// Privileged tools require elevated permissions (e.g., shell exec).
	Privileged
)

func (s SafetyTier) String() string {
	switch s {
	case ReadOnly:
		return "read_only"
	case SideEffecting:
		return "side_effecting"
	case Privileged:
		return "privileged"
	default:
		return "unknown"
	}
}

// FunctionDecl is the JSON Schema description of a tool sent to the LLM.
type FunctionDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema object
}

// ToolExecutionContext carries per-turn state into tool execution.
type ToolExecutionContext struct {
	SessionID    string
	UserID       string
	WorkspaceDir string
	Progress     chan<- ProgressEvent
}

// Tool is the interface that all Yantra tools must implement.
type Tool interface {
	// Name returns the tool's unique identifier.
	Name() string

	// Description returns a human-readable description for the LLM.
	Description() string

	// Decl returns the function declaration (name + description + JSON Schema params).
	Decl() FunctionDecl

	// Execute runs the tool with the given JSON input.
	Execute(ctx context.Context, input json.RawMessage, execCtx ToolExecutionContext) (string, error)

	// SafetyTier returns the tool's safety classification.
	SafetyTier() SafetyTier

	// Timeout returns the maximum execution duration for this tool.
	Timeout() time.Duration
}
