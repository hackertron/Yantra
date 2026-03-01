package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

var configPath string

func main() {
	root := &cobra.Command{
		Use:   "yantra",
		Short: "Yantra — AI agent orchestrator",
		Long: `Yantra (यन्त्र) is a self-hosted AI agent orchestrator.
Multi-provider, WASM-sandboxed tools, persistent memory, MCP support.
Single binary. Zero config to get started.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&configPath, "config", "", "config file path (default: auto-discover)")

	root.AddCommand(
		initCmd(),
		startCmd(),
		tuiCmd(),
		serveCmd(),
		versionCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scaffold a starter yantra.toml config file",
		RunE:  runInit,
	}
}

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the Yantra daemon",
		RunE:  runStart,
	}
}

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch the terminal UI (starts daemon if needed)",
		RunE:  runTUI,
	}
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the API server (headless, no TUI)",
		RunE:  runServe,
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("yantra %s (commit: %s, built: %s)\n", version, commit, buildDate)
		},
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	const configTemplate = `# Yantra configuration
# Full reference: https://github.com/hackertron/Yantra

[selection]
provider = "openai"
model = "gpt-4o-mini"

[providers.registry.openai]
provider_type = "openai"
api_key_env = "OPENAI_API_KEY"

[providers.registry.anthropic]
provider_type = "anthropic"
api_key_env = "ANTHROPIC_API_KEY"

[providers.registry.gemini]
provider_type = "gemini"
api_key_env = "GEMINI_API_KEY"

[runtime]
max_turns = 25
turn_timeout_secs = 120

[runtime.context_budget]
trigger_ratio = 0.85
safety_buffer_tokens = 1024
fallback_max_context_tokens = 128000

[runtime.summarization]
target_ratio = 0.5
min_turns = 6

[memory]
enabled = true
embedding_backend = "openai"

[memory.embedding]
model = "text-embedding-3-small"

[memory.retrieval]
top_k = 8
vector_weight = 0.7
fts_weight = 0.3

[gateway]
listen = "127.0.0.1:7700"
max_sessions = 50
max_concurrent_turns = 10

[tools.web_search]
provider = "duckduckgo"

# [mcp.servers.filesystem]
# transport = "stdio"
# command = "npx"
# args = ["-y", "@modelcontextprotocol/server-filesystem", "."]
`

	target := configPath
	if target == "" {
		target = "yantra.toml"
	}
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("%s already exists (use --config to specify a different path)", target)
	}

	if err := os.WriteFile(target, []byte(configTemplate), 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Printf("Created %s\n", target)
	fmt.Println("")
	fmt.Println("Next steps:")
	fmt.Println("  1. Set your provider API key:")
	fmt.Println("     export OPENAI_API_KEY=sk-...")
	fmt.Println("  2. Start chatting:")
	fmt.Println("     yantra tui")
	return nil
}

func runStart(cmd *cobra.Command, args []string) error {
	fmt.Println("Starting Yantra daemon...")
	// TODO: implement daemon startup
	return fmt.Errorf("not yet implemented")
}

func runTUI(cmd *cobra.Command, args []string) error {
	fmt.Println("Launching Yantra TUI...")
	// TODO: implement TUI launch
	return fmt.Errorf("not yet implemented")
}

func runServe(cmd *cobra.Command, args []string) error {
	fmt.Println("Starting Yantra API server...")
	// TODO: implement API server
	return fmt.Errorf("not yet implemented")
}
