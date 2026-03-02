# Configuration Guide

This document explains every configuration option in Yantra, how config loading works, and how to customize behavior.

## Config loading

Yantra loads config in three layers (highest priority wins):

```
┌──────────────────────────────┐
│  3. Environment variables    │  ← overrides everything
│     YANTRA__SELECTION__...   │
├──────────────────────────────┤
│  2. Config file              │  ← overrides defaults
│     yantra.toml              │
├──────────────────────────────┤
│  1. Built-in defaults        │  ← always present
│     DefaultConfig()          │
└──────────────────────────────┘
```

### Config file discovery

If you don't pass `--config`, Yantra looks for a config file in this order:
1. `./yantra.toml` (current directory)
2. `./.yantra/config.toml` (hidden config directory)
3. `~/.config/yantra/config.toml` (user config)

First match wins. If none are found, defaults are used.

### Environment variables

Every config field can be set via environment variable with the `YANTRA__` prefix. Nesting uses double underscore:

```bash
# Set provider to anthropic
export YANTRA__SELECTION__PROVIDER=anthropic
export YANTRA__SELECTION__MODEL=claude-sonnet-4-20250514

# Set runtime limits
export YANTRA__RUNTIME__MAX_TURNS=50
export YANTRA__RUNTIME__TURN_TIMEOUT_SECS=300

# Set gateway listen address
export YANTRA__GATEWAY__LISTEN=0.0.0.0:7700
```

This is useful for deployments where you want config in the environment rather than files.

## Generating a config file

```bash
yantra init
```

This creates a `yantra.toml` in the current directory with all options documented.

## Full config reference

### selection

Which provider and model to use by default.

```toml
[selection]
provider = "openai"      # Must match a key in providers.registry
model = "gpt-4o-mini"    # Model name passed to the provider
```

### providers.registry

Available LLM providers. You can define multiple and switch between them.

```toml
[providers.registry.openai]
provider_type = "openai"
api_key_env = "OPENAI_API_KEY"        # Environment variable holding the API key
max_context_tokens = 128000           # Context window size
max_output_tokens = 4096              # Max tokens per response
# base_url = "https://custom-endpoint.example.com/v1"  # Optional custom endpoint

[providers.registry.anthropic]
provider_type = "anthropic"
api_key_env = "ANTHROPIC_API_KEY"
max_context_tokens = 200000

[providers.registry.gemini]
provider_type = "gemini"
api_key_env = "GEMINI_API_KEY"
max_context_tokens = 1000000

# Custom/self-hosted example:
[providers.registry.local-llama]
provider_type = "openai"                           # Use OpenAI-compatible API
base_url = "http://localhost:8080/v1"
api_key_env = "LOCAL_API_KEY"
max_context_tokens = 32000
```

`provider_type` values: `"openai"`, `"anthropic"`, `"gemini"`

### runtime

Controls the agent turn loop.

```toml
[runtime]
max_turns = 25             # Max LLM round-trips before stopping
turn_timeout_secs = 120    # Timeout per turn in seconds
max_cost = 0.0             # Max dollar cost (0 = unlimited)
```

**max_turns** prevents infinite loops. If the LLM keeps calling tools without converging on an answer, this stops it.

**turn_timeout_secs** is the timeout for a single turn. It covers both the provider streaming phase and tool execution as one budget. Individual tools also have their own Timeout() applied by the registry.

**max_cost** tracks token usage cost and stops if exceeded. Useful for preventing runaway spend.

### runtime.context_budget

Controls when context compaction (summarization) triggers.

```toml
[runtime.context_budget]
trigger_ratio = 0.85                   # Compact when context is 85% full
safety_buffer_tokens = 1024            # Reserve this many tokens for the response
fallback_max_context_tokens = 128000   # Used when provider doesn't report context size
```

When the conversation history approaches the context limit (trigger_ratio × max_context_tokens), older messages get summarized to free space. The safety buffer ensures there's always room for the next response.

### runtime.summarization

Controls rolling summarization behavior.

```toml
[runtime.summarization]
target_ratio = 0.5   # Aim to reduce context to 50% after summarization
min_turns = 6        # Don't summarize conversations shorter than 6 turns
```

### memory

Persistent memory backed by a vector database.

```toml
[memory]
enabled = true
db_path = ".yantra/memory.db"          # SQLite/libSQL database path
embedding_backend = "openai"           # "openai" or "ollama"

[memory.embedding]
model = "text-embedding-3-small"       # OpenAI embedding model
# ollama_url = "http://localhost:11434" # For ollama backend
# ollama_model = "nomic-embed-text"    # For ollama backend

[memory.retrieval]
top_k = 8              # Number of results to retrieve
vector_weight = 0.7    # Weight for vector similarity (0-1)
fts_weight = 0.3       # Weight for full-text search (0-1)
```

Memory uses hybrid retrieval — combining vector similarity search (semantic meaning) with full-text search (exact keyword matching). The weights control the balance. Higher vector_weight favors semantic matches; higher fts_weight favors exact matches.

### tools

Tool-specific configuration.

```toml
[tools.web_search]
provider = "duckduckgo"     # "duckduckgo", "google", "searxng"
# base_url = ""             # For self-hosted search (searxng)
# api_key_env = ""          # For Google search API

[tools.shell]
# allow = ["mycustomtool"]  # Add commands to allowlist
# deny = ["docker"]         # Add commands to denylist
replace_defaults = false     # true = discard default allow/deny lists
allow_operators = false      # true = permit |, &&, ||, ;, >, etc.
```

#### Shell allowlist defaults

These commands are allowed by default:
```
ls, cat, head, tail, wc, find, grep, rg, sed, awk, sort, uniq, cut, tr, tee,
diff, patch, git, gh, go, gofmt, goimports, node, npm, npx, yarn, pnpm, bun,
deno, python, python3, pip, pip3, uv, ruby, gem, bundle, rustc, cargo, java,
javac, mvn, gradle, make, cmake, docker, docker-compose, curl, wget, echo,
printf, date, env, which, whoami, pwd, mkdir, cp, mv, touch, chmod, ln, tar,
zip, unzip, gzip, gunzip, jq, yq, tree, file, stat, du, df
```

#### Shell denylist defaults

These commands are always blocked:
```
sudo, su, doas, mkfs, fdisk, dd, shutdown, reboot, halt, poweroff, init, rm
```

**replace_defaults = true** clears both lists and uses only your custom allow/deny entries. Use this for locked-down environments where you want explicit control.

**allow_operators = true** permits shell operators (`|`, `&&`, etc.). Only enable this if you trust the LLM not to chain dangerous commands.

### gateway

WebSocket gateway for remote access.

```toml
[gateway]
listen = "127.0.0.1:7700"     # Listen address
api_key = ""                    # Optional auth key
max_sessions = 50               # Max concurrent sessions
max_concurrent_turns = 10       # Max turns running at once across all sessions
session_idle_ttl_hours = 48     # Cleanup idle sessions after this long
```

### mcp

Model Context Protocol server definitions. MCP lets you extend Yantra with external tool servers.

```toml
[mcp.servers.filesystem]
transport = "stdio"                          # "stdio" or "sse"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"]

[mcp.servers.remote-tools]
transport = "sse"
url = "http://localhost:3000/sse"
```

`stdio` transport launches a subprocess and communicates over stdin/stdout.
`sse` transport connects to an HTTP server using Server-Sent Events.

### agents

Specialist subagent definitions.

```toml
[agents.coder]
system_prompt = "You are a senior software engineer..."
tools = ["read_file", "write_file", "shell_exec"]
max_turns = 15
max_cost = 0.50

[agents.coder.selection]
provider = "anthropic"
model = "claude-sonnet-4-20250514"

[agents.researcher]
system_prompt = "You are a research assistant..."
tools = ["web_fetch", "read_file"]
max_turns = 10
```

Each agent can have its own:
- **system_prompt** — personality and instructions
- **tools** — subset of registered tools (filters the tool schemas sent to the LLM)
- **max_turns / max_cost** — independent budgets
- **selection** — different provider/model (optional, defaults to the global selection)
