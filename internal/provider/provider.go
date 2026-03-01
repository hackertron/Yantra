package provider

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/hackertron/Yantra/internal/types"
)

// Build constructs a Provider from a registry entry and selection.
func Build(name string, entry types.ProviderRegistryEntry, model string) (types.Provider, error) {
	apiKey, err := ResolveAPIKey(entry)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", name, err)
	}

	var p types.Provider

	switch entry.ProviderType {
	case types.ProviderOpenAI, types.ProviderOpenAIResponses:
		p, err = NewOpenAI(name, apiKey, model, entry)
	case types.ProviderAnthropic:
		p, err = NewAnthropic(name, apiKey, model, entry)
	case types.ProviderGemini:
		p, err = NewGemini(name, apiKey, model, entry)
	default:
		return nil, fmt.Errorf("provider %s: unknown type %q", name, entry.ProviderType)
	}
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", name, err)
	}

	slog.Info("provider ready", "name", name, "type", entry.ProviderType, "model", model)
	return p, nil
}

// BuildFromConfig constructs a Provider from the full Yantra config.
func BuildFromConfig(cfg *types.YantraConfig) (types.Provider, error) {
	sel := cfg.Selection
	entry, ok := cfg.Providers.Registry[sel.Provider]
	if !ok {
		return nil, fmt.Errorf("provider %q not found in registry", sel.Provider)
	}
	return Build(sel.Provider, entry, sel.Model)
}

// ResolveAPIKey resolves the API key for a provider entry.
// Resolution order: explicit api_key_env → provider-type default env → generic API_KEY.
func ResolveAPIKey(entry types.ProviderRegistryEntry) (string, error) {
	if entry.APIKeyEnv != "" {
		if key := os.Getenv(entry.APIKeyEnv); key != "" {
			return key, nil
		}
	}

	defaults := map[types.ProviderType]string{
		types.ProviderOpenAI:          "OPENAI_API_KEY",
		types.ProviderOpenAIResponses: "OPENAI_API_KEY",
		types.ProviderAnthropic:       "ANTHROPIC_API_KEY",
		types.ProviderGemini:          "GEMINI_API_KEY",
	}
	if envName, ok := defaults[entry.ProviderType]; ok {
		if key := os.Getenv(envName); key != "" {
			return key, nil
		}
	}

	if key := os.Getenv("API_KEY"); key != "" {
		return key, nil
	}

	envHint := entry.APIKeyEnv
	if envHint == "" {
		if d, ok := defaults[entry.ProviderType]; ok {
			envHint = d
		}
	}
	return "", fmt.Errorf("no API key found (set %s)", envHint)
}
