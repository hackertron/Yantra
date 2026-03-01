package provider

import (
	"testing"

	"github.com/hackertron/Yantra/internal/types"
)

func TestResolveAPIKey_ExplicitEnv(t *testing.T) {
	t.Setenv("MY_CUSTOM_KEY", "sk-custom-123")
	entry := types.ProviderRegistryEntry{
		ProviderType: types.ProviderOpenAI,
		APIKeyEnv:    "MY_CUSTOM_KEY",
	}
	key, err := ResolveAPIKey(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "sk-custom-123" {
		t.Fatalf("expected sk-custom-123, got %s", key)
	}
}

func TestResolveAPIKey_DefaultEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-default-456")
	entry := types.ProviderRegistryEntry{
		ProviderType: types.ProviderOpenAI,
	}
	key, err := ResolveAPIKey(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "sk-default-456" {
		t.Fatalf("expected sk-default-456, got %s", key)
	}
}

func TestResolveAPIKey_Missing(t *testing.T) {
	// Unset all possible fallback keys
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("API_KEY", "")
	entry := types.ProviderRegistryEntry{
		ProviderType: types.ProviderOpenAI,
	}
	_, err := ResolveAPIKey(entry)
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestBuild_OpenAIResponsesNotImplemented(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test")
	entry := types.ProviderRegistryEntry{
		ProviderType: types.ProviderOpenAIResponses,
		APIKeyEnv:    "OPENAI_API_KEY",
	}
	_, err := Build("test", entry, "model")
	if err == nil {
		t.Fatal("expected error for openai_responses")
	}
}
