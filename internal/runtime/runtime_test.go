package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hackertron/Yantra/internal/tool"
	"github.com/hackertron/Yantra/internal/types"
)

// ---------------------------------------------------------------------------
// Mock provider
// ---------------------------------------------------------------------------

// scriptedProvider returns pre-configured responses. Stream() emits text
// char-by-char and tool call deltas in chunks to exercise the accumulator.
type scriptedProvider struct {
	responses []*types.Response
	callIdx   int
}

func (p *scriptedProvider) ProviderID() types.ProviderID { return "test" }
func (p *scriptedProvider) ModelID() types.ModelID       { return "test-model" }
func (p *scriptedProvider) MaxContextTokens() int        { return 128000 }

func (p *scriptedProvider) Complete(ctx context.Context, c *types.Context) (*types.Response, error) {
	if p.callIdx >= len(p.responses) {
		return nil, errors.New("no more scripted responses")
	}
	resp := p.responses[p.callIdx]
	p.callIdx++
	return resp, nil
}

func (p *scriptedProvider) Stream(ctx context.Context, c *types.Context) <-chan types.StreamItem {
	ch := make(chan types.StreamItem, 128)
	if p.callIdx >= len(p.responses) {
		go func() {
			defer close(ch)
			ch <- types.StreamItem{Type: types.StreamError, Error: errors.New("no more scripted responses")}
		}()
		return ch
	}
	resp := p.responses[p.callIdx]
	p.callIdx++

	go func() {
		defer close(ch)

		// Emit text char-by-char.
		for _, r := range resp.Message.Content {
			ch <- types.StreamItem{Type: types.StreamText, Text: string(r)}
		}

		// Emit tool calls as chunked deltas.
		for i, tc := range resp.Message.ToolCalls {
			// First chunk: ID + Name + first half of args.
			args := tc.Function.Arguments
			mid := len(args) / 2
			ch <- types.StreamItem{
				Type: types.StreamToolCallDelta,
				ToolCallDelta: &types.ToolCallDelta{
					Index: i,
					ID:    tc.ID,
					Name:  tc.Function.Name,
				},
			}
			if mid > 0 {
				ch <- types.StreamItem{
					Type: types.StreamToolCallDelta,
					ToolCallDelta: &types.ToolCallDelta{
						Index:     i,
						Arguments: args[:mid],
					},
				}
			}
			ch <- types.StreamItem{
				Type: types.StreamToolCallDelta,
				ToolCallDelta: &types.ToolCallDelta{
					Index:     i,
					Arguments: args[mid:],
				},
			}
		}

		// Done with usage.
		ch <- types.StreamItem{
			Type:  types.StreamDone,
			Usage: &resp.Usage,
		}
	}()
	return ch
}

// blockingProvider blocks until the context is cancelled.
type blockingProvider struct{}

func (p *blockingProvider) ProviderID() types.ProviderID { return "blocking" }
func (p *blockingProvider) ModelID() types.ModelID       { return "blocking-model" }
func (p *blockingProvider) MaxContextTokens() int        { return 128000 }

func (p *blockingProvider) Complete(ctx context.Context, c *types.Context) (*types.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingProvider) Stream(ctx context.Context, c *types.Context) <-chan types.StreamItem {
	ch := make(chan types.StreamItem, 1)
	go func() {
		defer close(ch)
		<-ctx.Done()
		ch <- types.StreamItem{Type: types.StreamError, Error: ctx.Err()}
	}()
	return ch
}

// ---------------------------------------------------------------------------
// Mock tool
// ---------------------------------------------------------------------------

type mockTool struct {
	name      string
	tier      types.SafetyTier
	execFunc  func(ctx context.Context, input json.RawMessage) (string, error)
	callCount atomic.Int32
}

func (t *mockTool) Name() string        { return t.name }
func (t *mockTool) Description() string { return "mock tool " + t.name }
func (t *mockTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.name,
		Description: t.Description(),
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}
func (t *mockTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	t.callCount.Add(1)
	if t.execFunc != nil {
		return t.execFunc(ctx, input)
	}
	return "ok", nil
}
func (t *mockTool) SafetyTier() SafetyTier   { return t.tier }
func (t *mockTool) Timeout() time.Duration   { return 30 * time.Second }

type SafetyTier = types.SafetyTier

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestRegistry(tools ...types.Tool) *tool.ToolRegistry {
	reg := tool.NewRegistry(nil)
	for _, t := range tools {
		_ = reg.Register(t)
	}
	return reg
}

func defaultRuntimeConfig() types.RuntimeConfig {
	return types.RuntimeConfig{
		MaxTurns:        10,
		TurnTimeoutSecs: 30,
		ContextBudget: types.ContextBudgetConfig{
			TriggerRatio:             0.85,
			FallbackMaxContextTokens: 128000,
		},
	}
}

func textResponse(content string, promptTok, completionTok int) *types.Response {
	return &types.Response{
		Message: types.Message{
			Role:    types.RoleAssistant,
			Content: content,
		},
		Usage: types.Usage{
			PromptTokens:     promptTok,
			CompletionTokens: completionTok,
			TotalTokens:      promptTok + completionTok,
		},
	}
}

func toolCallResponse(calls []types.ToolCall, promptTok, completionTok int) *types.Response {
	return &types.Response{
		Message: types.Message{
			Role:      types.RoleAssistant,
			ToolCalls: calls,
		},
		Usage: types.Usage{
			PromptTokens:     promptTok,
			CompletionTokens: completionTok,
			TotalTokens:      promptTok + completionTok,
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSession_ContextIncludesSystemPrompt(t *testing.T) {
	tools := []types.FunctionDecl{
		{Name: "test_tool", Description: "a test", Parameters: json.RawMessage(`{}`)},
	}
	s := NewSession("You are helpful.", tools)
	s.Append(types.Message{Role: types.RoleUser, Content: "hello"})
	s.Append(types.Message{Role: types.RoleAssistant, Content: "hi"})

	ctx := s.Context()

	if len(ctx.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(ctx.Messages))
	}
	if ctx.Messages[0].Role != types.RoleSystem {
		t.Errorf("first message should be system, got %s", ctx.Messages[0].Role)
	}
	if ctx.Messages[0].Content != "You are helpful." {
		t.Errorf("system prompt mismatch: %q", ctx.Messages[0].Content)
	}
	if ctx.Messages[1].Role != types.RoleUser {
		t.Errorf("second message should be user, got %s", ctx.Messages[1].Role)
	}
	if ctx.Messages[2].Role != types.RoleAssistant {
		t.Errorf("third message should be assistant, got %s", ctx.Messages[2].Role)
	}
	if len(ctx.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(ctx.Tools))
	}
	if s.Len() != 2 {
		t.Errorf("Len() should be 2 (excludes system), got %d", s.Len())
	}
}

func TestRun_SimpleTextResponse(t *testing.T) {
	prov := &scriptedProvider{
		responses: []*types.Response{
			textResponse("The answer is 4.", 10, 5),
		},
	}
	reg := newTestRegistry()
	rt := New(prov, reg, defaultRuntimeConfig(), "")

	result, err := rt.Run(context.Background(), "system", "What is 2+2?", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalContent != "The answer is 4." {
		t.Errorf("unexpected content: %q", result.FinalContent)
	}
	if result.TurnsUsed != 1 {
		t.Errorf("expected 1 turn, got %d", result.TurnsUsed)
	}
	if result.TotalUsage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", result.TotalUsage.TotalTokens)
	}
}

func TestRun_ToolCallThenResponse(t *testing.T) {
	mt := &mockTool{
		name: "add",
		tier: types.ReadOnly,
		execFunc: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "4", nil
		},
	}

	prov := &scriptedProvider{
		responses: []*types.Response{
			// Turn 1: tool call
			toolCallResponse([]types.ToolCall{
				{ID: "call_1", Function: types.FunctionCall{Name: "add", Arguments: `{"a":2,"b":2}`}},
			}, 10, 5),
			// Turn 2: final text
			textResponse("The answer is 4.", 20, 5),
		},
	}
	reg := newTestRegistry(mt)
	rt := New(prov, reg, defaultRuntimeConfig(), "")

	result, err := rt.Run(context.Background(), "system", "add 2+2", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalContent != "The answer is 4." {
		t.Errorf("unexpected content: %q", result.FinalContent)
	}
	if result.TurnsUsed != 2 {
		t.Errorf("expected 2 turns, got %d", result.TurnsUsed)
	}
	if mt.callCount.Load() != 1 {
		t.Errorf("expected tool called once, got %d", mt.callCount.Load())
	}
	if result.TotalUsage.PromptTokens != 30 {
		t.Errorf("expected 30 prompt tokens, got %d", result.TotalUsage.PromptTokens)
	}
}

func TestRun_MaxTurnsExceeded(t *testing.T) {
	// Provider always returns a tool call — never finishes.
	mt := &mockTool{name: "loop", tier: types.ReadOnly}

	responses := make([]*types.Response, 5)
	for i := range responses {
		responses[i] = toolCallResponse([]types.ToolCall{
			{ID: "call", Function: types.FunctionCall{Name: "loop", Arguments: `{}`}},
		}, 10, 5)
	}

	prov := &scriptedProvider{responses: responses}
	reg := newTestRegistry(mt)
	cfg := defaultRuntimeConfig()
	cfg.MaxTurns = 3
	rt := New(prov, reg, cfg, "")

	_, err := rt.Run(context.Background(), "system", "go", nil)
	if !errors.Is(err, types.ErrMaxTurns) {
		t.Errorf("expected ErrMaxTurns, got %v", err)
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	prov := &blockingProvider{}
	reg := newTestRegistry()
	rt := New(prov, reg, defaultRuntimeConfig(), "")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := rt.Run(ctx, "system", "hello", nil)
	if !errors.Is(err, types.ErrCancelled) {
		t.Errorf("expected ErrCancelled, got %v", err)
	}
}

func TestRun_ToolDispatchOrdering(t *testing.T) {
	// Two ReadOnly tools should overlap; one SideEffecting tool is sequential.
	var (
		roStartA, roEndA time.Time
		roStartB, roEndB time.Time
		seStart, seEnd   time.Time
	)

	toolA := &mockTool{
		name: "ro_a",
		tier: types.ReadOnly,
		execFunc: func(ctx context.Context, input json.RawMessage) (string, error) {
			roStartA = time.Now()
			time.Sleep(50 * time.Millisecond)
			roEndA = time.Now()
			return "a", nil
		},
	}
	toolB := &mockTool{
		name: "ro_b",
		tier: types.ReadOnly,
		execFunc: func(ctx context.Context, input json.RawMessage) (string, error) {
			roStartB = time.Now()
			time.Sleep(50 * time.Millisecond)
			roEndB = time.Now()
			return "b", nil
		},
	}
	toolC := &mockTool{
		name: "se_c",
		tier: types.SideEffecting,
		execFunc: func(ctx context.Context, input json.RawMessage) (string, error) {
			seStart = time.Now()
			time.Sleep(10 * time.Millisecond)
			seEnd = time.Now()
			return "c", nil
		},
	}

	prov := &scriptedProvider{
		responses: []*types.Response{
			toolCallResponse([]types.ToolCall{
				{ID: "1", Function: types.FunctionCall{Name: "ro_a", Arguments: `{}`}},
				{ID: "2", Function: types.FunctionCall{Name: "ro_b", Arguments: `{}`}},
				{ID: "3", Function: types.FunctionCall{Name: "se_c", Arguments: `{}`}},
			}, 10, 5),
			textResponse("done", 10, 5),
		},
	}
	reg := newTestRegistry(toolA, toolB, toolC)
	rt := New(prov, reg, defaultRuntimeConfig(), "")

	result, err := rt.Run(context.Background(), "system", "go", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalContent != "done" {
		t.Errorf("unexpected content: %q", result.FinalContent)
	}

	// ReadOnly tools should overlap: A starts before B ends and vice versa.
	if roStartA.After(roEndB) || roStartB.After(roEndA) {
		t.Error("ReadOnly tools should execute in parallel (timing overlap)")
	}

	// SideEffecting tool runs after ReadOnly tools complete.
	if seStart.Before(roEndA) || seStart.Before(roEndB) {
		t.Error("SideEffecting tool should start after all ReadOnly tools complete")
	}
	_ = seEnd // used only for timing validation above
}

func TestCollectStream_FragmentedToolCalls(t *testing.T) {
	// Build a response with 2 tool calls, each split into 3+ chunks.
	prov := &scriptedProvider{
		responses: []*types.Response{
			{
				Message: types.Message{
					Role: types.RoleAssistant,
					ToolCalls: []types.ToolCall{
						{ID: "tc_0", Function: types.FunctionCall{Name: "read_file", Arguments: `{"path":"main.go"}`}},
						{ID: "tc_1", Function: types.FunctionCall{Name: "list_files", Arguments: `{"dir":"src"}`}},
					},
				},
				Usage: types.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
			},
		},
	}

	// We test collectStream directly by creating a runtime with a session.
	reg := newTestRegistry()
	rt := New(prov, reg, defaultRuntimeConfig(), "")

	session := NewSession("sys", nil)
	session.Append(types.Message{Role: types.RoleUser, Content: "test"})

	resp, err := rt.collectStream(context.Background(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.Message.ToolCalls))
	}

	tc0 := resp.Message.ToolCalls[0]
	if tc0.ID != "tc_0" || tc0.Function.Name != "read_file" {
		t.Errorf("tc0: unexpected id=%q name=%q", tc0.ID, tc0.Function.Name)
	}
	if tc0.Function.Arguments != `{"path":"main.go"}` {
		t.Errorf("tc0: unexpected args=%q", tc0.Function.Arguments)
	}

	tc1 := resp.Message.ToolCalls[1]
	if tc1.ID != "tc_1" || tc1.Function.Name != "list_files" {
		t.Errorf("tc1: unexpected id=%q name=%q", tc1.ID, tc1.Function.Name)
	}
	if tc1.Function.Arguments != `{"dir":"src"}` {
		t.Errorf("tc1: unexpected args=%q", tc1.Function.Arguments)
	}

	if resp.Usage.TotalTokens != 120 {
		t.Errorf("expected 120 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestRun_ToolExecutionError(t *testing.T) {
	mt := &mockTool{
		name: "failing",
		tier: types.ReadOnly,
		execFunc: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "", errors.New("disk full")
		},
	}

	prov := &scriptedProvider{
		responses: []*types.Response{
			toolCallResponse([]types.ToolCall{
				{ID: "call_1", Function: types.FunctionCall{Name: "failing", Arguments: `{}`}},
			}, 10, 5),
			textResponse("I see the error, let me try another way.", 20, 10),
		},
	}
	reg := newTestRegistry(mt)
	rt := New(prov, reg, defaultRuntimeConfig(), "")

	result, err := rt.Run(context.Background(), "system", "do it", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.FinalContent, "try another way") {
		t.Errorf("unexpected content: %q", result.FinalContent)
	}
	if result.TurnsUsed != 2 {
		t.Errorf("expected 2 turns, got %d", result.TurnsUsed)
	}
}

func TestRun_ProgressEvents(t *testing.T) {
	prov := &scriptedProvider{
		responses: []*types.Response{
			toolCallResponse([]types.ToolCall{
				{ID: "call_1", Function: types.FunctionCall{Name: "mytool", Arguments: `{}`}},
			}, 10, 5),
			textResponse("done", 10, 5),
		},
	}
	mt := &mockTool{name: "mytool", tier: types.ReadOnly}
	reg := newTestRegistry(mt)
	rt := New(prov, reg, defaultRuntimeConfig(), "")

	progress := make(chan types.ProgressEvent, 32)
	_, err := rt.Run(context.Background(), "system", "go", progress)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	close(progress)

	var providerCalls, toolExecs int
	for ev := range progress {
		switch ev.Kind {
		case types.ProgressProviderCall:
			providerCalls++
		case types.ProgressToolExecution:
			toolExecs++
		}
	}

	if providerCalls < 2 {
		t.Errorf("expected at least 2 ProgressProviderCall events, got %d", providerCalls)
	}
	if toolExecs < 1 {
		t.Errorf("expected at least 1 ProgressToolExecution event, got %d", toolExecs)
	}
}

func TestRun_TurnTimeout(t *testing.T) {
	// The blocking provider never completes, so the 1-second turn timeout
	// fires. The runtime should return ErrTimeout (not ErrCancelled)
	// because the parent context is still alive.
	prov := &blockingProvider{}
	rt := New(prov, newTestRegistry(), types.RuntimeConfig{
		MaxTurns:        10,
		TurnTimeoutSecs: 1,
		ContextBudget: types.ContextBudgetConfig{
			TriggerRatio:             0.85,
			FallbackMaxContextTokens: 128000,
		},
	}, "")

	start := time.Now()
	_, err := rt.Run(context.Background(), "system", "hello", nil)
	elapsed := time.Since(start)

	if !errors.Is(err, types.ErrTimeout) {
		t.Errorf("expected ErrTimeout, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("expected turn to timeout around 1s, took %v", elapsed)
	}
}

func TestRun_TurnTimeoutDuringToolExecution(t *testing.T) {
	// Provider streams instantly, but the tool blocks for 2s.
	// With a 1-second turn timeout covering both phases, the tool
	// should be interrupted and the error surfaced to the LLM.
	slowTool := &mockTool{
		name: "slow",
		tier: types.ReadOnly,
		execFunc: func(ctx context.Context, input json.RawMessage) (string, error) {
			select {
			case <-time.After(2 * time.Second):
				return "done", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}

	prov := &scriptedProvider{
		responses: []*types.Response{
			toolCallResponse([]types.ToolCall{
				{ID: "call_1", Function: types.FunctionCall{Name: "slow", Arguments: `{}`}},
			}, 10, 5),
			// After tool timeout, the tool result (with error) goes back to
			// the provider, which returns a final text response.
			textResponse("tool timed out, moving on", 20, 5),
		},
	}
	reg := newTestRegistry(slowTool)
	rt := New(prov, reg, types.RuntimeConfig{
		MaxTurns:        10,
		TurnTimeoutSecs: 1,
		ContextBudget: types.ContextBudgetConfig{
			TriggerRatio:             0.85,
			FallbackMaxContextTokens: 128000,
		},
	}, "")

	result, err := rt.Run(context.Background(), "system", "go", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalContent != "tool timed out, moving on" {
		t.Errorf("unexpected content: %q", result.FinalContent)
	}
	if result.TurnsUsed != 2 {
		t.Errorf("expected 2 turns, got %d", result.TurnsUsed)
	}
}
