package provider

import (
	"testing"

	"github.com/hackertron/Yantra/internal/types"
)

func TestConvertMessagesOpenAI_AssistantWithToolCalls(t *testing.T) {
	msgs := []types.Message{
		{
			Role:    types.RoleAssistant,
			Content: "Let me search",
			ToolCalls: []types.ToolCall{
				{
					ID: "call_123",
					Function: types.FunctionCall{
						Name:      "search",
						Arguments: `{"q":"test"}`,
					},
				},
			},
		},
	}
	out := convertMessagesOpenAI(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if out[0].OfAssistant == nil {
		t.Fatal("expected assistant message")
	}
	if len(out[0].OfAssistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(out[0].OfAssistant.ToolCalls))
	}
}

func TestBuildParams_HasStreamOptions(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test")
	p, err := NewOpenAI("openai", "test-key", "gpt-4o", types.ProviderRegistryEntry{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx := &types.Context{
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	params := p.buildParams(ctx)
	if !params.StreamOptions.IncludeUsage.Value {
		t.Fatal("expected StreamOptions.IncludeUsage to be true")
	}
}
