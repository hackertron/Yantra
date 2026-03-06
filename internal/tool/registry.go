package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hackertron/Yantra/internal/types"
)

const defaultMaxOutputBytes = 128 * 1024 // 128 KB

// ToolRegistry holds registered tools, applies security policy, and executes tool calls.
type ToolRegistry struct {
	tools          map[string]types.Tool
	policy         SecurityPolicy
	scrubber       *Scrubber
	maxOutputBytes int
}

// NewRegistry creates a ToolRegistry with the given security policy.
func NewRegistry(policy SecurityPolicy) *ToolRegistry {
	return &ToolRegistry{
		tools:          make(map[string]types.Tool),
		policy:         policy,
		maxOutputBytes: defaultMaxOutputBytes,
	}
}

// SetScrubber configures output scrubbing for all tool results.
func (r *ToolRegistry) SetScrubber(s *Scrubber) {
	r.scrubber = s
}

// SetMaxOutputBytes overrides the default output truncation limit.
func (r *ToolRegistry) SetMaxOutputBytes(n int) {
	if n > 0 {
		r.maxOutputBytes = n
	}
}

// Register adds a tool to the registry. Returns an error if the name is already registered.
func (r *ToolRegistry) Register(t types.Tool) error {
	name := t.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q is already registered", name)
	}
	r.tools[name] = t
	return nil
}

// Get returns the tool with the given name, or nil if not found.
func (r *ToolRegistry) Get(name string) types.Tool {
	return r.tools[name]
}

// List returns the names of all registered tools, sorted alphabetically.
func (r *ToolRegistry) List() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Schemas returns FunctionDecl entries for registered tools.
// If filter is non-empty, only tools whose names appear in filter are returned.
// This accepts AgentDefinition.Tools directly for per-agent filtering.
func (r *ToolRegistry) Schemas(filter []string) []types.FunctionDecl {
	var names []string
	if len(filter) > 0 {
		allowed := make(map[string]bool, len(filter))
		for _, f := range filter {
			allowed[f] = true
		}
		for name := range r.tools {
			if allowed[name] {
				names = append(names, name)
			}
		}
	} else {
		for name := range r.tools {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	decls := make([]types.FunctionDecl, 0, len(names))
	for _, name := range names {
		decls = append(decls, r.tools[name].Decl())
	}
	return decls
}

// Execute runs a tool by name with input JSON, applying policy checks and timeouts.
func (r *ToolRegistry) Execute(ctx context.Context, name string, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	t := r.tools[name]
	if t == nil {
		return "", &types.ToolError{Tool: name, Message: "tool not found"}
	}

	// Security policy check.
	if r.policy != nil {
		if err := r.policy.CheckExecution(t, input, execCtx); err != nil {
			return "", &types.ToolError{Tool: name, Message: "policy violation", Err: err}
		}
	}

	// Emit progress event (non-blocking to avoid deadlock on full channel).
	if execCtx.Progress != nil {
		select {
		case execCtx.Progress <- types.ProgressEvent{
			Kind:    types.ProgressToolExecution,
			Tool:    name,
			Message: "executing",
		}:
		default:
		}
	}

	// Apply tool-specific timeout.
	timeout := t.Timeout()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	output, err := t.Execute(ctx, input, execCtx)
	if err != nil {
		return "", &types.ToolError{Tool: name, Message: "execution failed", Err: err}
	}

	// Truncate output at line boundary if too large.
	output = truncateOutput(output, r.maxOutputBytes)

	// Scrub sensitive data (host paths, credential keywords, high-entropy tokens).
	if r.scrubber != nil {
		output = r.scrubber.Scrub(output)
	}

	return output, nil
}

// truncateOutput truncates s to at most maxBytes, cutting at a line boundary.
func truncateOutput(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}

	// Find the last newline before the limit.
	cut := strings.LastIndex(s[:maxBytes], "\n")
	if cut < 0 {
		cut = maxBytes
	}

	return s[:cut] + "\n... [output truncated]"
}
