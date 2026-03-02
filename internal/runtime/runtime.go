package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/hackertron/Yantra/internal/tool"
	"github.com/hackertron/Yantra/internal/types"
)

// AgentRuntime ties a provider and tool registry together to execute the
// think → act → observe turn loop.
type AgentRuntime struct {
	provider     types.Provider
	tools        *tool.ToolRegistry
	config       types.RuntimeConfig
	workspaceDir string
}

// RunResult is the output of a successful Run invocation.
type RunResult struct {
	FinalContent string
	TurnsUsed    int
	TotalUsage   types.Usage
}

// New creates an AgentRuntime. workspaceDir is the root directory for tool
// file-path containment checks; pass "." or an absolute path.
func New(provider types.Provider, tools *tool.ToolRegistry, config types.RuntimeConfig, workspaceDir string) *AgentRuntime {
	if workspaceDir == "" {
		workspaceDir = "."
	}
	return &AgentRuntime{
		provider:     provider,
		tools:        tools,
		config:       config,
		workspaceDir: workspaceDir,
	}
}

// Run executes the agent turn loop. It streams provider responses, dispatches
// tool calls, and repeats until the LLM produces a final text response or
// MaxTurns is exhausted. Progress events are sent on the optional progress
// channel (may be nil).
func (r *AgentRuntime) Run(ctx context.Context, systemPrompt, userMessage string, progress chan<- types.ProgressEvent) (*RunResult, error) {
	session := NewSession(systemPrompt, r.tools.Schemas(nil))

	session.Append(types.Message{
		Role:    types.RoleUser,
		Content: userMessage,
	})

	var totalUsage types.Usage
	turnsUsed := 0

	maxTurns := r.config.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 25
	}

	for turn := 0; turn < maxTurns; turn++ {
		// Per-turn timeout covers both provider streaming and tool execution.
		turnCtx, turnCancel := context.WithTimeout(ctx, r.config.TurnTimeout())

		emitProgress(progress, types.ProgressEvent{
			Kind:    types.ProgressProviderCall,
			Message: fmt.Sprintf("turn %d", turn+1),
		})

		resp, err := r.collectStream(turnCtx, session)
		if err != nil {
			turnCancel()
			return nil, r.classifyError(ctx, turnCtx, err)
		}

		// Accumulate usage.
		totalUsage.PromptTokens += resp.Usage.PromptTokens
		totalUsage.CompletionTokens += resp.Usage.CompletionTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens
		turnsUsed++

		// Append assistant message.
		session.Append(resp.Message)

		// If no tool calls, we're done.
		if len(resp.Message.ToolCalls) == 0 {
			turnCancel()
			return &RunResult{
				FinalContent: resp.Message.Content,
				TurnsUsed:    turnsUsed,
				TotalUsage:   totalUsage,
			}, nil
		}

		// Dispatch tool calls under the same turn timeout.
		toolMsgs := r.dispatchTools(turnCtx, resp.Message.ToolCalls, progress)
		turnCancel()
		for _, msg := range toolMsgs {
			session.Append(msg)
		}

		// Check if the parent context was cancelled during tool dispatch.
		if ctx.Err() != nil {
			return nil, types.ErrCancelled
		}

		r.checkContextBudget(session)
	}

	return nil, types.ErrMaxTurns
}

// collectStream reads a streaming provider response and assembles it into a
// complete Response, including fragmented tool call deltas.
func (r *AgentRuntime) collectStream(ctx context.Context, session *Session) (*types.Response, error) {
	ch := r.provider.Stream(ctx, session.Context())

	var content strings.Builder
	partials := make(map[int]*toolCallPartial)
	var usage types.Usage

	for item := range ch {
		switch item.Type {
		case types.StreamText:
			content.WriteString(item.Text)

		case types.StreamToolCallDelta:
			if item.ToolCallDelta == nil {
				continue
			}
			d := item.ToolCallDelta
			p, ok := partials[d.Index]
			if !ok {
				p = &toolCallPartial{index: d.Index}
				partials[d.Index] = p
			}
			if d.ID != "" {
				p.id = d.ID
			}
			if d.Name != "" {
				p.name = d.Name
			}
			p.args.WriteString(d.Arguments)

		case types.StreamDone:
			if item.Usage != nil {
				usage = *item.Usage
			}

		case types.StreamError:
			if item.Error != nil {
				return nil, item.Error
			}
		}
	}

	// Build tool calls ordered by index.
	toolCalls := buildToolCalls(partials)

	resp := &types.Response{
		Message: types.Message{
			Role:      types.RoleAssistant,
			Content:   content.String(),
			ToolCalls: toolCalls,
		},
		Usage: usage,
	}
	return resp, nil
}

// toolCallPartial accumulates streamed deltas for a single tool call.
type toolCallPartial struct {
	index int
	id    string
	name  string
	args  strings.Builder
}

// buildToolCalls converts partials into an ordered slice of ToolCall.
func buildToolCalls(partials map[int]*toolCallPartial) []types.ToolCall {
	if len(partials) == 0 {
		return nil
	}
	indices := make([]int, 0, len(partials))
	for idx := range partials {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	calls := make([]types.ToolCall, 0, len(indices))
	for _, idx := range indices {
		p := partials[idx]
		calls = append(calls, types.ToolCall{
			ID: p.id,
			Function: types.FunctionCall{
				Name:      p.name,
				Arguments: p.args.String(),
			},
		})
	}
	return calls
}

// dispatchTools executes tool calls respecting safety tiers: ReadOnly tools
// run in parallel, SideEffecting/Privileged tools run sequentially.
// Results are returned in the same order as the input calls.
func (r *AgentRuntime) dispatchTools(ctx context.Context, calls []types.ToolCall, progress chan<- types.ProgressEvent) []types.Message {
	results := make([]types.Message, len(calls))

	// Partition by safety tier.
	type indexedCall struct {
		idx  int
		call types.ToolCall
	}
	var readOnly []indexedCall
	var sequential []indexedCall

	for i, call := range calls {
		t := r.tools.Get(call.Function.Name)
		if t != nil && t.SafetyTier() == types.ReadOnly {
			readOnly = append(readOnly, indexedCall{i, call})
		} else {
			sequential = append(sequential, indexedCall{i, call})
		}
	}

	execCtx := types.ToolExecutionContext{
		WorkspaceDir: r.workspaceDir,
		Progress:     progress,
	}

	// ReadOnly: execute in parallel.
	if len(readOnly) > 0 {
		var wg sync.WaitGroup
		wg.Add(len(readOnly))
		for _, ic := range readOnly {
			go func(ic indexedCall) {
				defer wg.Done()
				results[ic.idx] = r.executeTool(ctx, ic.call, execCtx)
			}(ic)
		}
		wg.Wait()
	}

	// Sequential: execute in order.
	for _, ic := range sequential {
		results[ic.idx] = r.executeTool(ctx, ic.call, execCtx)
	}

	return results
}

// executeTool runs a single tool call and returns a tool-role message.
// Tool errors are placed in the message content (the LLM sees them),
// not returned as Go errors.
func (r *AgentRuntime) executeTool(ctx context.Context, call types.ToolCall, execCtx types.ToolExecutionContext) types.Message {
	emitProgress(execCtx.Progress, types.ProgressEvent{
		Kind:    types.ProgressToolExecution,
		Tool:    call.Function.Name,
		Message: "executing",
	})

	output, err := r.tools.Execute(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments), execCtx)
	if err != nil {
		output = "Error: " + err.Error()
	}

	return types.Message{
		Role:       types.RoleTool,
		Content:    output,
		ToolCallID: call.ID,
		ToolName:   call.Function.Name,
	}
}

// classifyError maps context errors to the appropriate runtime sentinel.
// If the parent ctx was cancelled, it's a user cancellation. If only the
// turn ctx expired, it's a turn timeout.
func (r *AgentRuntime) classifyError(parent, turn context.Context, err error) error {
	if parent.Err() != nil {
		return types.ErrCancelled
	}
	if turn.Err() == context.DeadlineExceeded {
		return types.ErrTimeout
	}
	return err
}

// checkContextBudget estimates token usage and logs a warning if the session
// is approaching the context limit. Actual summarization is deferred to Step 5.
func (r *AgentRuntime) checkContextBudget(session *Session) {
	maxTokens := r.provider.MaxContextTokens()
	if maxTokens <= 0 {
		maxTokens = r.config.ContextBudget.FallbackMaxContextTokens
	}
	if maxTokens <= 0 {
		return
	}

	triggerRatio := r.config.ContextBudget.TriggerRatio
	if triggerRatio <= 0 {
		triggerRatio = 0.85
	}

	// Rough token estimate: total chars / 4.
	totalChars := 0
	for _, msg := range session.Messages() {
		totalChars += len(msg.Content)
		for _, tc := range msg.ToolCalls {
			totalChars += len(tc.Function.Arguments)
		}
	}
	estimatedTokens := totalChars / 4

	threshold := int(float64(maxTokens) * triggerRatio)
	if estimatedTokens > threshold {
		slog.Warn("context budget warning: approaching limit",
			"estimated_tokens", estimatedTokens,
			"max_tokens", maxTokens,
			"trigger_ratio", triggerRatio,
		)
	}
}

// emitProgress sends a progress event without blocking. If the channel is nil
// or full the event is dropped.
func emitProgress(ch chan<- types.ProgressEvent, event types.ProgressEvent) {
	if ch == nil {
		return
	}
	select {
	case ch <- event:
	default:
	}
}
