package provider

import (
	"testing"

	"github.com/hackertron/Yantra/internal/types"
)

func TestConvertMessagesAnthropic_MultipleSystemsConcatenated(t *testing.T) {
	msgs := []types.Message{
		{Role: types.RoleSystem, Content: "First instruction."},
		{Role: types.RoleSystem, Content: "Second instruction."},
		{Role: types.RoleUser, Content: "Hello"},
	}
	_, sys := convertMessagesAnthropic(msgs)
	if sys != "First instruction.\n\nSecond instruction." {
		t.Fatalf("expected concatenated system prompt, got %q", sys)
	}
}
