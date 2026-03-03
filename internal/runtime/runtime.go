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
// think -> act -> observe turn loop.
type AgentRuntime struct {
	provider     types.Provider
	tools        *tool.ToolRegistry
	config       types.RuntimeConfig
	workspaceDir string
	memory       types.MemoryRetrieval
	sessionID    string
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

// SetMemory configures persistent memory and session ID for the runtime.
func (r *AgentRuntime) SetMemory(mem types.MemoryRetrieval, sessionID string) {
	r.memory = mem
	r.sessionID = sessionID
}

// Run executes the agent turn loop. It streams provider responses, dispatches
// tool calls, and repeats until the LLM produces a final text response or
// MaxTurns is exhausted. Progress events are sent on the optional progress
// channel (may be nil).
func (r *AgentRuntime) Run(ctx context.Context, systemPrompt, userMessage string, progress chan<- types.ProgressEvent) (*RunResult, error) {
	session := NewSession(systemPrompt, r.tools.Schemas(nil))

	// If memory has a prior summary, prepend it.
	if r.memory != nil && r.sessionID != "" {
		if summary, err := r.memory.GetSummary(ctx, r.sessionID); err == nil && summary != nil && summary.Summary != "" {
			session.Append(types.Message{
				Role:    types.RoleUser,
				Content: fmt.Sprintf("[Conversation Summary]\n%s", summary.Summary),
			})
			session.Append(types.Message{
				Role:    types.RoleAssistant,
				Content: "I have the context from our previous conversation. How can I help you?",
			})
		}
	}

	userMsg := types.Message{
		Role:    types.RoleUser,
		Content: userMessage,
	}
	session.Append(userMsg)
	r.persistEvent(ctx, userMsg)

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
		r.persistEvent(turnCtx, resp.Message)

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
		for _, msg := range toolMsgs {
			session.Append(msg)
			r.persistEvent(turnCtx, msg)
		}

		// Check if the parent context was cancelled during tool dispatch.
		if ctx.Err() != nil {
			turnCancel()
			return nil, types.ErrCancelled
		}

		r.checkContextBudget(turnCtx, session, progress)
		turnCancel()
	}

	return nil, types.ErrMaxTurns
}

// persistEvent stores a message to conversation history if memory is configured.
func (r *AgentRuntime) persistEvent(ctx context.Context, msg types.Message) {
	if r.memory == nil || r.sessionID == "" {
		return
	}
	if err := r.memory.StoreConversationEvent(ctx, r.sessionID, msg); err != nil {
		slog.Warn("failed to persist conversation event", "error", err)
	}
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

// dispatchTools executes tool calls respecting both the model-provided order
// and safety tiers. Contiguous runs of ReadOnly calls execute in parallel;
// any SideEffecting/Privileged call executes sequentially at its original
// position (after flushing any pending ReadOnly block). This preserves
// ordering for cross-tool dependencies (e.g., write_file then read_file)
// while retaining parallelism where it's safe.
func (r *AgentRuntime) dispatchTools(ctx context.Context, calls []types.ToolCall, progress chan<- types.ProgressEvent) []types.Message {
	results := make([]types.Message, len(calls))

	execCtx := types.ToolExecutionContext{
		SessionID:    r.sessionID,
		WorkspaceDir: r.workspaceDir,
		Progress:     progress,
	}

	type indexedCall struct {
		idx  int
		call types.ToolCall
	}

	// flushReadOnly executes a contiguous block of ReadOnly calls in parallel.
	flushReadOnly := func(block []indexedCall) {
		if len(block) == 0 {
			return
		}
		var wg sync.WaitGroup
		wg.Add(len(block))
		for _, ic := range block {
			go func(ic indexedCall) {
				defer wg.Done()
				results[ic.idx] = r.executeTool(ctx, ic.call, execCtx)
			}(ic)
		}
		wg.Wait()
	}

	var readOnlyBlock []indexedCall

	for i, call := range calls {
		t := r.tools.Get(call.Function.Name)
		if t != nil && t.SafetyTier() == types.ReadOnly {
			readOnlyBlock = append(readOnlyBlock, indexedCall{i, call})
			continue
		}
		// Non-ReadOnly: flush any pending ReadOnly block first, then run sequentially.
		flushReadOnly(readOnlyBlock)
		readOnlyBlock = nil
		results[i] = r.executeTool(ctx, call, execCtx)
	}
	// Flush any trailing ReadOnly block.
	flushReadOnly(readOnlyBlock)

	return results
}

// executeTool runs a single tool call and returns a tool-role message.
// Tool errors are placed in the message content (the LLM sees them),
// not returned as Go errors.
func (r *AgentRuntime) executeTool(ctx context.Context, call types.ToolCall, execCtx types.ToolExecutionContext) types.Message {
	// Progress emission is handled by ToolRegistry.Execute to avoid duplicates.
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

// checkContextBudget estimates token usage and triggers summarization when the
// session is approaching the context limit.
func (r *AgentRuntime) checkContextBudget(ctx context.Context, session *Session, progress chan<- types.ProgressEvent) {
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
	if estimatedTokens <= threshold {
		return
	}

	// Check minimum turns before summarizing.
	minTurns := r.config.Summarization.MinTurns
	if minTurns <= 0 {
		minTurns = 6
	}
	if session.Len() < minTurns {
		slog.Warn("context budget warning: approaching limit but too few turns to summarize",
			"estimated_tokens", estimatedTokens,
			"session_len", session.Len(),
			"min_turns", minTurns,
		)
		return
	}

	// No memory/provider means we can't summarize — just warn.
	if r.memory == nil {
		slog.Warn("context budget warning: approaching limit, no memory configured for summarization",
			"estimated_tokens", estimatedTokens,
			"max_tokens", maxTokens,
		)
		return
	}

	slog.Info("context budget: triggering summarization",
		"estimated_tokens", estimatedTokens,
		"max_tokens", maxTokens,
		"trigger_ratio", triggerRatio,
	)

	emitProgress(progress, types.ProgressEvent{
		Kind:    types.ProgressSummarization,
		Message: "compacting conversation history",
	})

	// Build summarization prompt from messages to be compacted.
	targetRatio := r.config.Summarization.TargetRatio
	if targetRatio <= 0 {
		targetRatio = 0.5
	}
	keepCount := int(float64(session.Len()) * (1.0 - targetRatio))
	if keepCount < 2 {
		keepCount = 2
	}

	msgs := session.Messages()
	toSummarize := msgs[:len(msgs)-keepCount]

	// Get existing summary if available.
	var existingSummary string
	if r.sessionID != "" {
		if summary, err := r.memory.GetSummary(ctx, r.sessionID); err == nil && summary != nil {
			existingSummary = summary.Summary
		}
	}

	summaryPrompt := buildSummarizationPrompt(toSummarize, existingSummary)

	// Use the provider to generate the summary.
	summaryCtx := &types.Context{
		Messages: []types.Message{
			{Role: types.RoleSystem, Content: "You are a summarization assistant. Produce a concise summary of the conversation that captures all key facts, decisions, and context needed to continue the conversation."},
			{Role: types.RoleUser, Content: summaryPrompt},
		},
	}

	resp, err := r.provider.Complete(ctx, summaryCtx)
	if err != nil {
		slog.Error("summarization failed", "error", err)
		return
	}

	summary := resp.Message.Content
	if summary == "" {
		return
	}

	// Store summary in memory.
	if r.sessionID != "" {
		epoch := int64(0)
		if existing, err := r.memory.GetSummary(ctx, r.sessionID); err == nil && existing != nil {
			epoch = existing.Epoch + 1
		}
		if err := r.memory.SetSummary(ctx, r.sessionID, types.SessionSummary{
			Summary: summary,
			Epoch:   epoch,
		}); err != nil {
			slog.Error("failed to store summary", "error", err)
		}
	}

	// Compact the session.
	session.CompactWithSummary(summary, keepCount)

	slog.Info("summarization complete",
		"kept_messages", keepCount,
		"summary_len", len(summary),
	)
}

// buildSummarizationPrompt constructs the prompt for the summarization LLM call.
func buildSummarizationPrompt(messages []types.Message, existingSummary string) string {
	var b strings.Builder

	if existingSummary != "" {
		b.WriteString("Previous conversation summary:\n")
		b.WriteString(existingSummary)
		b.WriteString("\n\n")
	}

	b.WriteString("Summarize the following conversation, preserving all important facts, decisions, user preferences, and context:\n\n")

	for _, msg := range messages {
		b.WriteString(fmt.Sprintf("[%s]: %s\n", msg.Role, msg.Content))
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				b.WriteString(fmt.Sprintf("  -> called %s(%s)\n", tc.Function.Name, tc.Function.Arguments))
			}
		}
	}

	return b.String()
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
