# Yantra

An AI agent orchestrator written in Go. Multi-provider, tool-equipped, memory-backed agents that you fully own and understand.

## What is this?

Yantra is a system that lets LLMs (GPT-4, Claude, Gemini) **do things** — not just chat. It gives them tools to read files, write code, run commands, and fetch data. Then it runs them in a loop: the LLM thinks, calls tools, reads results, thinks again, until the task is done.

Think of it as building your own Claude Code / Cursor agent from scratch.

## Architecture at a glance

```
┌─────────────────────────────────────────────┐
│                    CLI                       │
│              cmd/yantra/main.go              │
├─────────────────────────────────────────────┤
│               Runtime (Step 4)               │
│         the agent turn loop (planned)        │
├──────────────┬──────────────┬───────────────┤
│   Provider   │    Tools     │    Memory     │
│   Layer      │   System     │   (Step 5)    │
│              │              │   (planned)   │
│  OpenAI      │  read_file   │              │
│  Anthropic   │  write_file  │              │
│  Gemini      │  list_files  │              │
│              │  shell_exec  │              │
│              │  web_fetch   │              │
├──────────────┴──────────────┴───────────────┤
│                   Types                      │
│          interfaces, contracts, config       │
└─────────────────────────────────────────────┘
```

## Quick start

```bash
# Build
go build ./...

# Generate default config
go run ./cmd/yantra init

# Edit yantra.toml with your API keys
$EDITOR yantra.toml
```

## Project structure

```
cmd/yantra/           CLI entry point (init, version, start, serve, tui)
internal/
  types/              Shared interfaces and data types
    config.go         Configuration structs + defaults
    config_loader.go  TOML + env var config loading
    tool.go           Tool interface, SafetyTier, FunctionDecl
    provider.go       Provider interface, ProviderType
    message.go        Message, ToolCall, streaming types
    errors.go         Typed errors (ToolError, ProviderError, etc.)
    memory.go         Memory interface, embedding, retrieval
    session.go        Session store interface
    channel.go        WebSocket frame protocol
    delegation.go     Multi-agent delegation types
  provider/           LLM provider implementations
    provider.go       Factory (Build, BuildFromConfig)
    openai.go         OpenAI Chat Completions
    anthropic.go      Anthropic Messages API
    gemini.go         Google Gemini GenerateContent
    reliable.go       Retry wrapper with exponential backoff
  tool/               Tool system
    schema.go         JSON Schema builder helpers
    security.go       SecurityPolicy + WorkspacePolicy
    registry.go       ToolRegistry (register, lookup, execute)
    read_file.go      read_file tool
    write_file.go     write_file tool
    list_files.go     list_files tool
    shell_exec.go     shell_exec tool
    web_fetch.go      web_fetch tool
    builtin.go        RegisterBuiltins() convenience
```

## Configuration

Yantra uses layered configuration (highest priority wins):

1. Built-in defaults
2. Config file (`yantra.toml`, auto-discovered)
3. Environment variables (`YANTRA__` prefix)

Run `yantra init` to generate a starter `yantra.toml`.

## Providers

| Provider  | SDK                          | Default context |
|-----------|------------------------------|----------------:|
| OpenAI    | `openai-go/v3`               |         128,000 |
| Anthropic | `anthropic-sdk-go`           |         200,000 |
| Gemini    | `google.golang.org/genai`    |       1,000,000 |

All providers implement the same `Provider` interface. Swap between them with one config change. The `ReliableProvider` wrapper adds automatic retries with exponential backoff for transient failures.

## Tools

| Tool         | Safety tier    | Timeout | What it does                                |
|--------------|---------------|---------|---------------------------------------------|
| `read_file`  | ReadOnly      | 10s     | Read file with line numbers, offset, limit  |
| `write_file` | SideEffecting | 10s     | Write/append to file, auto-creates dirs     |
| `list_files` | ReadOnly      | 10s     | List directory, optional recursive          |
| `shell_exec` | Privileged    | 60s     | Run shell command, capture stdout/stderr    |
| `web_fetch`  | SideEffecting | 30s     | HTTP GET/POST, return status + body         |

### Security

All tool execution goes through a `SecurityPolicy`:

- **Path containment**: file tools can only access files inside the workspace directory
- **Shell allowlist**: ~40 common commands permitted by default (git, go, node, python, curl, etc.)
- **Shell denylist**: dangerous commands blocked (sudo, rm, mkfs, shutdown, etc.)
- **Operator blocking**: `|`, `&&`, `||`, `;`, `>` blocked by default (configurable)
- Deny always overrides allow

## Tests

```bash
go test ./... -race -count=1
```

## Docs

See [`docs/`](docs/) for detailed architecture documentation.

## Inspired by

[Oxydra](https://github.com/shantanugoel/oxydra) — the same architecture in Rust. Yantra is a Go rewrite for simplicity and learning.

## License

MIT
