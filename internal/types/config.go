package types

import "time"

// YantraConfig is the root configuration for Yantra.
type YantraConfig struct {
	Selection ProviderSelection            `json:"selection" koanf:"selection"`
	Providers ProvidersConfig              `json:"providers" koanf:"providers"`
	Runtime   RuntimeConfig                `json:"runtime" koanf:"runtime"`
	Memory    MemoryConfig                 `json:"memory" koanf:"memory"`
	Tools     ToolsConfig                  `json:"tools" koanf:"tools"`
	Gateway   GatewayConfig                `json:"gateway" koanf:"gateway"`
	MCP       MCPConfig                    `json:"mcp" koanf:"mcp"`
	Agents    map[string]AgentDefinition   `json:"agents" koanf:"agents"`
}

// ProviderSelection identifies which provider and model to use.
type ProviderSelection struct {
	Provider string `json:"provider" koanf:"provider"`
	Model    string `json:"model" koanf:"model"`
}

// ProvidersConfig holds the provider registry.
type ProvidersConfig struct {
	Registry map[string]ProviderRegistryEntry `json:"registry" koanf:"registry"`
}

// ProviderRegistryEntry defines a single provider endpoint.
type ProviderRegistryEntry struct {
	ProviderType     ProviderType `json:"provider_type" koanf:"provider_type"`
	BaseURL          string       `json:"base_url,omitempty" koanf:"base_url"`
	APIKeyEnv        string       `json:"api_key_env,omitempty" koanf:"api_key_env"`
	MaxContextTokens int          `json:"max_context_tokens,omitempty" koanf:"max_context_tokens"`
	MaxOutputTokens  int          `json:"max_output_tokens,omitempty" koanf:"max_output_tokens"`
}

// RuntimeConfig controls the agent turn loop.
type RuntimeConfig struct {
	MaxTurns        int                 `json:"max_turns" koanf:"max_turns"`
	TurnTimeoutSecs int                 `json:"turn_timeout_secs" koanf:"turn_timeout_secs"`
	MaxCost         float64             `json:"max_cost,omitempty" koanf:"max_cost"`
	ContextBudget   ContextBudgetConfig `json:"context_budget" koanf:"context_budget"`
	Summarization   SummarizationConfig `json:"summarization" koanf:"summarization"`
}

// TurnTimeout returns the turn timeout as a duration.
func (r RuntimeConfig) TurnTimeout() time.Duration {
	if r.TurnTimeoutSecs <= 0 {
		return 120 * time.Second
	}
	return time.Duration(r.TurnTimeoutSecs) * time.Second
}

// ContextBudgetConfig controls when context compaction triggers.
type ContextBudgetConfig struct {
	TriggerRatio             float64 `json:"trigger_ratio" koanf:"trigger_ratio"`
	SafetyBufferTokens       int     `json:"safety_buffer_tokens" koanf:"safety_buffer_tokens"`
	FallbackMaxContextTokens int     `json:"fallback_max_context_tokens" koanf:"fallback_max_context_tokens"`
}

// SummarizationConfig controls rolling summarization.
type SummarizationConfig struct {
	TargetRatio float64 `json:"target_ratio" koanf:"target_ratio"`
	MinTurns    int     `json:"min_turns" koanf:"min_turns"`
}

// MemoryConfig controls persistent memory.
type MemoryConfig struct {
	Enabled          bool            `json:"enabled" koanf:"enabled"`
	DBPath           string          `json:"db_path,omitempty" koanf:"db_path"`
	EmbeddingBackend string          `json:"embedding_backend" koanf:"embedding_backend"` // "openai", "ollama"
	Embedding        EmbeddingConfig `json:"embedding" koanf:"embedding"`
	Retrieval        RetrievalConfig `json:"retrieval" koanf:"retrieval"`
}

// EmbeddingConfig holds embedding-specific settings.
type EmbeddingConfig struct {
	Model      string `json:"model,omitempty" koanf:"model"`             // OpenAI model name
	OllamaURL  string `json:"ollama_url,omitempty" koanf:"ollama_url"`   // Ollama base URL
	OllamaModel string `json:"ollama_model,omitempty" koanf:"ollama_model"` // Ollama model name
}

// RetrievalConfig controls hybrid retrieval weights.
type RetrievalConfig struct {
	TopK         int     `json:"top_k" koanf:"top_k"`
	VectorWeight float64 `json:"vector_weight" koanf:"vector_weight"`
	FTSWeight    float64 `json:"fts_weight" koanf:"fts_weight"`
}

// ToolsConfig holds tool-specific settings.
type ToolsConfig struct {
	WebSearch WebSearchConfig `json:"web_search" koanf:"web_search"`
	Shell     ShellConfig     `json:"shell" koanf:"shell"`
}

// WebSearchConfig controls the web_search tool.
type WebSearchConfig struct {
	Provider   string `json:"provider" koanf:"provider"`        // "duckduckgo", "google", "searxng"
	BaseURL    string `json:"base_url,omitempty" koanf:"base_url"`
	APIKeyEnv  string `json:"api_key_env,omitempty" koanf:"api_key_env"`
	GoogleCXEnv string `json:"google_cx_env,omitempty" koanf:"google_cx_env"` // Google Custom Search engine ID env var
}

// ShellConfig controls the shell_exec tool.
type ShellConfig struct {
	Allow           []string `json:"allow,omitempty" koanf:"allow"`
	Deny            []string `json:"deny,omitempty" koanf:"deny"`
	ReplaceDefaults bool     `json:"replace_defaults" koanf:"replace_defaults"`
	AllowOperators  bool     `json:"allow_operators" koanf:"allow_operators"`
}

// GatewayConfig controls the WebSocket gateway server.
type GatewayConfig struct {
	Listen              string `json:"listen" koanf:"listen"`
	APIKey              string `json:"api_key,omitempty" koanf:"api_key"`
	MaxSessions         int    `json:"max_sessions" koanf:"max_sessions"`
	MaxConcurrentTurns  int    `json:"max_concurrent_turns" koanf:"max_concurrent_turns"`
	SessionIdleTTLHours int    `json:"session_idle_ttl_hours" koanf:"session_idle_ttl_hours"`
}

// MCPConfig holds MCP server definitions.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers,omitempty" koanf:"servers"`
}

// MCPServerConfig defines a single MCP server connection.
type MCPServerConfig struct {
	Transport string            `json:"transport" koanf:"transport"` // "stdio", "sse"
	Command   string            `json:"command,omitempty" koanf:"command"`
	Args      []string          `json:"args,omitempty" koanf:"args"`
	URL       string            `json:"url,omitempty" koanf:"url"`
	Env       map[string]string `json:"env,omitempty" koanf:"env"`
}

// AgentDefinition describes a specialist subagent.
type AgentDefinition struct {
	SystemPrompt string            `json:"system_prompt,omitempty" koanf:"system_prompt"`
	Tools        []string          `json:"tools,omitempty" koanf:"tools"`
	MaxTurns     int               `json:"max_turns,omitempty" koanf:"max_turns"`
	MaxCost      float64           `json:"max_cost,omitempty" koanf:"max_cost"`
	Selection    *ProviderSelection `json:"selection,omitempty" koanf:"selection"`
}

// DefaultConfig returns a YantraConfig populated with sensible defaults.
func DefaultConfig() YantraConfig {
	return YantraConfig{
		Selection: ProviderSelection{
			Provider: "openai",
			Model:    "gpt-4o-mini",
		},
		Runtime: RuntimeConfig{
			MaxTurns:        25,
			TurnTimeoutSecs: 120,
			ContextBudget: ContextBudgetConfig{
				TriggerRatio:             0.85,
				SafetyBufferTokens:       1024,
				FallbackMaxContextTokens: 128000,
			},
			Summarization: SummarizationConfig{
				TargetRatio: 0.5,
				MinTurns:    6,
			},
		},
		Memory: MemoryConfig{
			Enabled:          true,
			DBPath:           ".yantra/memory.db",
			EmbeddingBackend: "openai",
			Embedding: EmbeddingConfig{
				Model: "text-embedding-3-small",
			},
			Retrieval: RetrievalConfig{
				TopK:         8,
				VectorWeight: 0.7,
				FTSWeight:    0.3,
			},
		},
		Gateway: GatewayConfig{
			Listen:              "127.0.0.1:7700",
			MaxSessions:         50,
			MaxConcurrentTurns:  10,
			SessionIdleTTLHours: 48,
		},
		Tools: ToolsConfig{
			WebSearch: WebSearchConfig{
				Provider: "duckduckgo",
			},
		},
	}
}
