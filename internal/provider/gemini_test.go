package provider

import (
	"testing"

	"github.com/hackertron/Yantra/internal/types"
)

func TestConvertMessagesGemini_MultipleSystemsConcatenated(t *testing.T) {
	msgs := []types.Message{
		{Role: types.RoleSystem, Content: "Part one."},
		{Role: types.RoleSystem, Content: "Part two."},
		{Role: types.RoleUser, Content: "Hello"},
	}
	_, sys := convertMessagesGemini(msgs)
	if sys != "Part one.\n\nPart two." {
		t.Fatalf("expected concatenated system prompt, got %q", sys)
	}
}

func TestConvertMessagesGemini_ToolResponseUsesToolName(t *testing.T) {
	msgs := []types.Message{
		{
			Role:       types.RoleTool,
			Content:    `{"result":"sunny"}`,
			ToolCallID: "call_get_weather_0",
			ToolName:   "get_weather",
		},
	}
	contents, _ := convertMessagesGemini(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	fr := contents[0].Parts[0].FunctionResponse
	if fr == nil {
		t.Fatal("expected FunctionResponse part")
	}
	if fr.Name != "get_weather" {
		t.Fatalf("expected function name 'get_weather', got %q", fr.Name)
	}
}
