package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/hackertron/Yantra/internal/gateway"
	"github.com/hackertron/Yantra/internal/memory"
	"github.com/hackertron/Yantra/internal/provider"
	"github.com/hackertron/Yantra/internal/runtime"
	"github.com/hackertron/Yantra/internal/tool"
	"github.com/hackertron/Yantra/internal/tui"
	"github.com/hackertron/Yantra/internal/types"
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
		runCmd(),
		startCmd(),
		tuiCmd(),
		serveCmd(),
		versionCmd(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := root.ExecuteContext(ctx); err != nil {
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

func runCmd() *cobra.Command {
	var systemPrompt string
	var workspace string

	cmd := &cobra.Command{
		Use:   "run [prompt]",
		Short: "Run a single agent turn loop with the given prompt",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(cmd.Context(), args[0], systemPrompt, workspace)
		},
	}
	cmd.Flags().StringVar(&systemPrompt, "system", "You are a helpful AI assistant with access to tools.", "system prompt")
	cmd.Flags().StringVar(&workspace, "workspace", ".", "workspace directory for tool execution")
	return cmd
}

func runAgent(ctx context.Context, prompt, systemPrompt, workspace string) error {
	cfg, err := types.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	p, err := provider.BuildFromConfig(cfg)
	if err != nil {
		return fmt.Errorf("building provider: %w", err)
	}
	p = provider.NewReliable(p, provider.DefaultReliableConfig())

	policy := tool.NewWorkspacePolicy(cfg.Tools.Shell)
	reg := tool.NewRegistry(policy)

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolving workspace: %w", err)
	}

	// Set up output scrubbing to hide host paths from the LLM.
	reg.SetScrubber(tool.NewScrubber([]tool.PathMapping{
		{HostPath: absWorkspace, DisplayPath: "."},
	}))

	// Set up memory if enabled.
	var mem types.MemoryRetrieval
	var memDB *memory.DB
	var sessionID string

	if cfg.Memory.Enabled {
		dbPath := cfg.Memory.DBPath
		if dbPath == "" {
			dbPath = ".yantra/memory.db"
		}
		if !filepath.IsAbs(dbPath) {
			dbPath = filepath.Join(absWorkspace, dbPath)
		}

		memDB, err = memory.OpenDB(dbPath)
		if err != nil {
			slog.Warn("failed to open memory DB, continuing without memory", "error", err)
		} else {
			embedder, err := memory.NewEmbeddingBackend(cfg.Memory)
			if err != nil {
				slog.Warn("failed to create embedding backend, continuing without embeddings", "error", err)
			}

			store := memory.NewStore(memDB, embedder, cfg.Memory.Retrieval)
			mem = store

			// Create a session for this run.
			sessionStore := memory.NewSessionStore(memDB)
			sess, err := sessionStore.Create(ctx, "cli-run")
			if err != nil {
				slog.Warn("failed to create session", "error", err)
			} else {
				sessionID = sess.ID
			}
		}
	}

	if memDB != nil {
		defer memDB.Close()
	}

	if err := tool.RegisterBuiltins(reg, cfg.Tools, mem); err != nil {
		return fmt.Errorf("registering tools: %w", err)
	}

	rt := runtime.New(p, reg, cfg.Runtime, absWorkspace)
	if mem != nil {
		rt.SetMemory(mem, sessionID)
	}

	progress := make(chan types.ProgressEvent, 32)
	go func() {
		for ev := range progress {
			if ev.Tool != "" {
				fmt.Fprintf(os.Stderr, "[%s] %s: %s\n", ev.Kind, ev.Tool, ev.Message)
			} else {
				fmt.Fprintf(os.Stderr, "[%s] %s\n", ev.Kind, ev.Message)
			}
		}
	}()

	result, err := rt.Run(ctx, systemPrompt, prompt, progress)
	close(progress)
	if err != nil {
		return fmt.Errorf("agent run failed: %w", err)
	}

	fmt.Println(result.FinalContent)
	fmt.Fprintf(os.Stderr, "\n--- stats ---\n")
	fmt.Fprintf(os.Stderr, "turns: %d\n", result.TurnsUsed)
	fmt.Fprintf(os.Stderr, "tokens: %d prompt, %d completion, %d total\n",
		result.TotalUsage.PromptTokens,
		result.TotalUsage.CompletionTokens,
		result.TotalUsage.TotalTokens,
	)
	return nil
}

func runStart(cmd *cobra.Command, args []string) error {
	fmt.Println("Starting Yantra daemon...")
	// TODO: implement daemon startup
	return fmt.Errorf("not yet implemented")
}

func runTUI(cmd *cobra.Command, args []string) error {
	cfg, err := types.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Redirect slog to a temp file so logs don't corrupt the TUI.
	logFile, err := os.CreateTemp("", "yantra-tui-*.log")
	if err != nil {
		return fmt.Errorf("creating log file: %w", err)
	}
	defer logFile.Close()
	logger := slog.New(slog.NewTextHandler(logFile, nil))

	// Build the provider label for the header.
	providerLabel := fmt.Sprintf("%s/%s", cfg.Selection.Provider, cfg.Selection.Model)

	// Start the gateway server in-process.
	gwCtx, gwCancel := context.WithCancel(cmd.Context())
	defer gwCancel()

	addr, errCh, gwCleanup, err := startGatewayInProcess(gwCtx, cfg, logger)
	if err != nil {
		return fmt.Errorf("starting gateway: %w", err)
	}
	defer gwCleanup()

	// Wait for the gateway to be healthy.
	if err := waitForHealth(addr, 10*time.Second); err != nil {
		return fmt.Errorf("gateway not ready: %w (check logs: %s)", err, logFile.Name())
	}

	// Create the TUI client and app.
	client := tui.NewClient(addr, cfg.Gateway.APIKey)
	hasDark := tui.DetectDarkMode()
	app := tui.NewApp(client, providerLabel, version, hasDark)

	p := tea.NewProgram(app)
	client.AttachProgram(p)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	// Clean up.
	client.Close()
	gwCancel()

	// Drain any gateway error.
	select {
	case gwErr := <-errCh:
		if gwErr != nil {
			logger.Warn("gateway exited with error", "error", gwErr)
		}
	default:
	}

	fmt.Fprintf(os.Stderr, "Logs written to: %s\n", logFile.Name())
	return nil
}

// startGatewayInProcess boots the gateway server in a goroutine.
// It mirrors the setup in runServe but returns immediately.
// The returned cleanup function closes any opened databases.
func startGatewayInProcess(ctx context.Context, cfg *types.YantraConfig, logger *slog.Logger) (addr string, errCh <-chan error, cleanup func(), err error) {
	p, err := provider.BuildFromConfig(cfg)
	if err != nil {
		return "", nil, nil, fmt.Errorf("building provider: %w", err)
	}
	p = provider.NewReliable(p, provider.DefaultReliableConfig())

	absWorkspace, err := filepath.Abs(".")
	if err != nil {
		return "", nil, nil, fmt.Errorf("resolving workspace: %w", err)
	}

	var mem types.MemoryRetrieval
	var sessStore types.SessionStore
	var closers []func() // DB close functions

	if cfg.Memory.Enabled {
		dbPath := cfg.Memory.DBPath
		if dbPath == "" {
			dbPath = ".yantra/memory.db"
		}
		if !filepath.IsAbs(dbPath) {
			dbPath = filepath.Join(absWorkspace, dbPath)
		}

		memDB, dbErr := memory.OpenDB(dbPath)
		if dbErr != nil {
			logger.Warn("failed to open memory DB, continuing without memory", "error", dbErr)
		} else {
			closers = append(closers, func() { memDB.Close() })
			embedder, embErr := memory.NewEmbeddingBackend(cfg.Memory)
			if embErr != nil {
				logger.Warn("failed to create embedding backend", "error", embErr)
			}
			mem = memory.NewStore(memDB, embedder, cfg.Memory.Retrieval)
			sessStore = memory.NewSessionStore(memDB)
		}
	}

	if sessStore == nil {
		sessDB, dbErr := memory.OpenDB(":memory:")
		if dbErr != nil {
			return "", nil, nil, fmt.Errorf("opening session DB: %w", dbErr)
		}
		closers = append(closers, func() { sessDB.Close() })
		sessStore = memory.NewSessionStore(sessDB)
	}

	policy := tool.NewWorkspacePolicy(cfg.Tools.Shell)
	reg := tool.NewRegistry(policy)
	reg.SetScrubber(tool.NewScrubber([]tool.PathMapping{
		{HostPath: absWorkspace, DisplayPath: "."},
	}))
	if err := tool.RegisterBuiltins(reg, cfg.Tools, mem); err != nil {
		return "", nil, nil, fmt.Errorf("registering tools: %w", err)
	}

	addr = cfg.Gateway.Listen
	if addr == "" {
		addr = "127.0.0.1:7700"
	}

	srv := gateway.NewServer(cfg.Gateway, cfg, p, reg, mem, sessStore, absWorkspace, logger)

	ch := make(chan error, 1)
	go func() {
		ch <- srv.ListenAndServe(ctx)
	}()

	cleanupFn := func() {
		for _, c := range closers {
			c()
		}
	}

	return addr, ch, cleanupFn, nil
}

// waitForHealth polls the gateway /health endpoint until it responds 200.
func waitForHealth(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://%s/health", addr)
	client := &http.Client{Timeout: time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for gateway at %s", addr)
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	logger := slog.Default()

	cfg, err := types.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	p, err := provider.BuildFromConfig(cfg)
	if err != nil {
		return fmt.Errorf("building provider: %w", err)
	}
	p = provider.NewReliable(p, provider.DefaultReliableConfig())

	absWorkspace, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolving workspace: %w", err)
	}

	// Set up memory if enabled.
	var mem types.MemoryRetrieval
	var memDB *memory.DB
	var sessStore types.SessionStore

	if cfg.Memory.Enabled {
		dbPath := cfg.Memory.DBPath
		if dbPath == "" {
			dbPath = ".yantra/memory.db"
		}
		if !filepath.IsAbs(dbPath) {
			dbPath = filepath.Join(absWorkspace, dbPath)
		}

		memDB, err = memory.OpenDB(dbPath)
		if err != nil {
			slog.Warn("failed to open memory DB, continuing without memory", "error", err)
		} else {
			embedder, err := memory.NewEmbeddingBackend(cfg.Memory)
			if err != nil {
				slog.Warn("failed to create embedding backend, continuing without embeddings", "error", err)
			}
			mem = memory.NewStore(memDB, embedder, cfg.Memory.Retrieval)
			sessStore = memory.NewSessionStore(memDB)
		}
	}
	if memDB != nil {
		defer memDB.Close()
	}

	// Sessions are required for the gateway even when memory is disabled.
	// Fall back to an in-memory SQLite DB for session tracking only.
	if sessStore == nil {
		sessDB, err := memory.OpenDB(":memory:")
		if err != nil {
			return fmt.Errorf("opening session DB: %w", err)
		}
		defer sessDB.Close()
		sessStore = memory.NewSessionStore(sessDB)
	}

	policy := tool.NewWorkspacePolicy(cfg.Tools.Shell)
	reg := tool.NewRegistry(policy)
	reg.SetScrubber(tool.NewScrubber([]tool.PathMapping{
		{HostPath: absWorkspace, DisplayPath: "."},
	}))
	if err := tool.RegisterBuiltins(reg, cfg.Tools, mem); err != nil {
		return fmt.Errorf("registering tools: %w", err)
	}

	srv := gateway.NewServer(cfg.Gateway, cfg, p, reg, mem, sessStore, absWorkspace, logger)

	logger.Info("starting Yantra API server", "listen", cfg.Gateway.Listen)
	return srv.ListenAndServe(ctx)
}
