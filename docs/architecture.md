# Yantra Architecture

This document explains how Yantra works, layer by layer. If you're reading this, you probably want to understand the system deeply — not just use it.

## The big idea

An AI agent is an LLM in a loop with tools. That's it. Here's the loop:

```
User: "Fix the bug in auth.go"
  │
  ▼
┌─────────────────────────────────────────┐
│              AGENT LOOP                  │
│                                          │
│  1. Send messages + tool schemas to LLM  │
│  2. LLM responds with text or tool calls │
│  3. Execute each tool call               │
│  4. Append results to message history    │
│  5. Go to step 1                         │
│                                          │
│  Stop when:                              │
│  - LLM responds with just text (done)    │
│  - Budget exceeded (tokens/cost/turns)   │
│  - User cancels                          │
│  - Timeout                               │
└─────────────────────────────────────────┘
  │
  ▼
Agent: "I've fixed the null pointer check on line 42 of auth.go"
```

Without the loop, it's a chatbot. With the loop, it's an agent. The loop is what lets the LLM plan, try things, read results, and adjust.

Everything in Yantra exists to make this loop work well:
- **Types** define the shared vocabulary
- **Providers** talk to LLMs
- **Tools** give the LLM hands
- **Security** prevents the LLM from doing damage
- **Config** makes it all customizable
- **Runtime** runs the think → act → observe loop
- **Memory** lets the agent remember across sessions
- **Gateway** (planned) lets you control it remotely

## Layer 1: Types (`internal/types/`)

The types package is the foundation. Every other package imports it. Nothing imports anything else.

### Why this matters

In Go, import cycles are compile errors. By putting all interfaces and data types in one package, every layer can talk to every other layer through shared contracts without importing each other. The provider package never imports the tool package. The tool package never imports the provider package. They both speak `types`.

### The key interfaces

**Provider** — anything that can talk to an LLM:
```go
type Provider interface {
    Complete(ctx context.Context, c *Context) (*Response, error)
    Stream(ctx context.Context, c *Context) <-chan StreamItem
    ProviderID() ProviderID
    ModelID() ModelID
    MaxContextTokens() int
}
```

`Complete` sends the full conversation and gets a response back. `Stream` does the same but returns tokens incrementally. The runtime will use `Complete` for tool-heavy work and `Stream` for showing text to the user in real-time.

**Tool** — anything the LLM can call:
```go
type Tool interface {
    Name() string
    Description() string
    Decl() FunctionDecl
    Execute(ctx context.Context, input json.RawMessage, execCtx ToolExecutionContext) (string, error)
    SafetyTier() SafetyTier
    Timeout() time.Duration
}
```

A tool has a name the LLM uses to call it, a JSON Schema declaration telling the LLM what parameters it accepts, and an Execute method that actually does the work. SafetyTier and Timeout are metadata the registry uses for policy enforcement.

**FunctionDecl** — what the LLM sees:
```go
type FunctionDecl struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}
```

This is what gets sent to the LLM in every request. The LLM reads the name, description, and parameter schema, then decides whether and how to call the tool. The Parameters field is raw JSON because every provider (OpenAI, Anthropic, Gemini) expects JSON Schema in slightly different wrappers — keeping it raw lets each provider marshal it however they need.

### Messages and streaming

A `Message` carries conversation content:
```go
type Message struct {
    Role       MessageRole
    Content    string
    ToolCalls  []ToolCall
    ToolCallID string
    ToolName   string
}
```

Roles: `system` (instructions), `user` (human input), `assistant` (LLM output), `tool` (tool results).

When the LLM wants to call a tool, it returns a Message with `ToolCalls` populated instead of `Content`. Each ToolCall has a function name and JSON arguments. The runtime executes them and creates new Messages with role `tool` containing the results.

Streaming works through a channel of `StreamItem`:
```go
type StreamItem struct {
    Type          StreamItemType
    Text          string        // for StreamText
    ToolCallDelta *ToolCallDelta // for StreamToolCallDelta
    Usage         *Usage        // for StreamDone
    Error         error         // for StreamError
}
```

The stream sends text chunks as they arrive, tool call argument fragments as the LLM generates them, and a final done/error event.

### Safety tiers

```go
const (
    ReadOnly      SafetyTier = iota // No side effects (read_file, list_files)
    SideEffecting                    // Modifies state (write_file, web_fetch)
    Privileged                       // Dangerous (shell_exec)
)
```

These tiers inform the runtime how to dispatch tools:
- **ReadOnly** tools run in parallel when contiguous in the call list
- **SideEffecting** tools run sequentially (they change state)
- **Privileged** tools run sequentially and may require user confirmation in future

### Configuration

`YantraConfig` is the root config struct. It's hierarchical:

```
YantraConfig
├── Selection        which provider + model to use
├── Providers        registry of available providers
├── Runtime          turn limits, timeouts, cost budgets
├── Memory           embedding backend, retrieval weights
├── Tools            shell allow/deny lists, web search config
├── Gateway          WebSocket server settings
├── MCP              Model Context Protocol servers
└── Agents           specialist subagent definitions
```

Config loading has three layers (highest priority wins):
1. `DefaultConfig()` — hardcoded sensible defaults
2. Config file — `yantra.toml` (auto-discovered or explicit path)
3. Environment variables — `YANTRA__SELECTION__PROVIDER=anthropic`

This means you can run with zero config (defaults), customize with a file, and override per-deployment with env vars.

### Error types

Every subsystem has its own error type:
```go
ToolError{Tool: "shell_exec", Message: "policy violation", Err: ...}
ProviderError{Provider: "openai", StatusCode: 429, Message: "rate limited", Retryable: true}
```

They all implement `error` and `Unwrap()` for use with `errors.Is()` and `errors.As()`. The Retryable flag on ProviderError lets the retry wrapper know which failures to retry.


## Layer 2: Providers (`internal/provider/`)

Providers translate between Yantra's universal message format and each LLM API's specific format.

### The problem they solve

OpenAI, Anthropic, and Gemini all have different APIs:
- OpenAI uses `messages` with `role` and `tool_calls` arrays
- Anthropic uses `messages` with `content` blocks (text blocks, tool_use blocks, tool_result blocks)
- Gemini uses `contents` with `parts` (text parts, function call parts, function response parts)

The Provider interface hides all of this. The runtime just calls `Complete()` with a list of messages and gets back a response. It never thinks about API formats.

### How a provider works (OpenAI example)

```
Yantra Messages    →  convertMessagesOpenAI()  →  OpenAI API format
                                                       │
                                                       ▼
                                                  HTTP request
                                                       │
                                                       ▼
Yantra Response    ←  openaiMessageToYantra()  ←  OpenAI API response
```

Each provider has:
- **Constructor** (`NewOpenAI`, `NewAnthropic`, `NewGemini`) — creates the SDK client, validates API key
- **Message converter** — translates Yantra Messages to provider format
- **Tool converter** — wraps FunctionDecl in provider-specific tool format
- **Response converter** — translates provider response back to Yantra Message
- **Stream handler** — processes incremental chunks into StreamItem channel

### The factory

```go
func Build(name string, entry ProviderRegistryEntry, model string) (Provider, error)
```

The factory routes to the right constructor based on `ProviderType`. It also resolves API keys from environment variables with fallback chains:

```
Explicit env var → Provider-specific default → Generic API_KEY
```

For example, the Anthropic provider checks `ANTHROPIC_API_KEY` if no explicit env var is configured.

`BuildFromConfig(cfg)` is a convenience that pulls the provider name and model from `cfg.Selection`.

### Reliable wrapper

```go
reliable := NewReliableProvider(inner, DefaultReliableConfig())
```

The `ReliableProvider` wraps any provider and adds retry logic:
- Retries on HTTP 429 (rate limit) and 5xx (server errors)
- Retries on connection failures, timeouts, EOF
- Exponential backoff: 250ms → 500ms → 1s → 2s (with jitter)
- Default 3 attempts
- Respects context cancellation (stops retrying if cancelled)

For streaming, it only retries the initial connection. Once the first chunk arrives, failures aren't retried (can't replay a partial stream).


## Layer 3: Tool System (`internal/tool/`)

Tools are what make an agent an agent. Without them, the LLM can only output text.

### How tool execution works end-to-end

```
LLM returns:
  ToolCall{Name: "read_file", Arguments: '{"path": "main.go"}'}
       │
       ▼
  Registry.Execute("read_file", '{"path": "main.go"}', execCtx)
       │
       ├── 1. Lookup tool by name
       ├── 2. SecurityPolicy.CheckExecution()
       │       ├── Is path inside workspace? ✓
       │       └── (for shell: is command allowed? operators ok?)
       ├── 3. Apply tool-specific timeout (10s for read_file)
       ├── 4. tool.Execute(ctx, input, execCtx)
       │       ├── Resolve path relative to workspace
       │       ├── Open file, read lines with numbers
       │       └── Return formatted output
       └── 5. Truncate output if > 128KB (at line boundary)
       │
       ▼
  "     1\tpackage main\n     2\t\n     3\tfunc main() {\n..."
       │
       ▼
  Appended to conversation as Message{Role: "tool", Content: ...}
```

### The registry

The `ToolRegistry` is a central lookup table:

```go
type ToolRegistry struct {
    tools          map[string]types.Tool
    policy         SecurityPolicy
    maxOutputBytes int  // default 128KB
}
```

It does four things:
1. **Registration** — tools register at startup via `RegisterBuiltins()`. Duplicate names are rejected.
2. **Schema export** — `Schemas(filter)` returns `[]FunctionDecl` for the LLM. The filter lets different agents see different tools.
3. **Policy enforcement** — every Execute call runs through the SecurityPolicy first.
4. **Execution** — timeout wrapping, running the tool, truncating output.

Output truncation is line-boundary-aware. If a tool returns 200KB of text, the registry cuts at the last newline before 128KB and appends `... [output truncated]`. This prevents sending garbage to the LLM (a mid-line cut could confuse it).

### Security policy

The `WorkspacePolicy` is the default security policy. It enforces three rules:

**1. Path containment for file tools**

Any `path` argument must resolve inside the workspace directory:
```
Workspace: /home/user/project

✓ "src/main.go"          → /home/user/project/src/main.go
✓ "/home/user/project/x" → /home/user/project/x
✗ "../../etc/passwd"      → /home/etc/passwd (BLOCKED)
✗ "/etc/passwd"           → /etc/passwd (BLOCKED)
```

The check uses `filepath.Clean` to resolve `.` and `..` before comparing. This prevents path traversal attacks where the LLM might try to read system files.

**2. Shell command allowlist/denylist**

The shell_exec tool is the most dangerous tool — it can run arbitrary commands. The policy restricts it:

```
Default allowlist (~40 commands):
  ls, git, go, node, python, curl, grep, make, docker, ...

Default denylist:
  sudo, rm, mkfs, shutdown, reboot, dd, ...

Rule: deny always overrides allow
```

The base command is extracted from the full command string. `/usr/bin/git status` → checks `git`. `sudo rm -rf /` → checks `sudo` (blocked).

**3. Shell operator blocking**

By default, these operators are blocked: `|`, `&&`, `||`, `;`, `>`, `>>`, `<`, `$(`, `` ` ``

This prevents the LLM from chaining commands in unexpected ways. `ls | rm -rf /` would bypass the command allowlist without this check. You can enable operators with `ShellConfig.AllowOperators: true` if you trust the LLM enough.

### Schema builder

Instead of hand-writing JSON Schema:
```json
{"type":"object","properties":{"path":{"type":"string","description":"File path"},"limit":{"type":"integer","description":"Max lines"}},"required":["path"]}
```

You write:
```go
Schema(
    Prop{Name: "path", Type: TypeString, Description: "File path", Required: true},
    Prop{Name: "limit", Type: TypeInteger, Description: "Max lines"},
)
```

This is a small quality-of-life builder. It outputs valid JSON Schema as `json.RawMessage`, which slots directly into `FunctionDecl.Parameters`.

### The built-in tools

Yantra ships with 7 built-in tools (5 core + 2 memory):

**read_file** (ReadOnly, 10s timeout)
- Reads a file with 6-digit line numbers: `     1\tpackage main`
- Supports `offset` (skip to line N) and `limit` (max N lines, default 2000)
- Line numbers help the LLM reference specific locations

**write_file** (SideEffecting, 10s timeout)
- Writes or appends content to a file
- Auto-creates parent directories (`MkdirAll`)
- Returns byte count confirmation

**list_files** (ReadOnly, 10s timeout)
- Flat listing (like `ls`) or recursive with max depth
- Directories have trailing `/` to distinguish from files
- Recursive mode uses `filepath.WalkDir` with depth cutoff

**shell_exec** (Privileged, 60s timeout)
- Runs `sh -c <command>` in the workspace directory
- Captures stdout and stderr separately
- Reports exit code (non-zero exit isn't an error — the LLM needs to see the output)
- 60s timeout because builds and tests can be slow

**web_fetch** (SideEffecting, 30s timeout)
- HTTP client with GET/POST/PUT/DELETE/PATCH/HEAD
- Custom headers via JSON string
- 1MB body limit (request and response)
- Returns `status: 200\n\n<body>`
- MVP: returns raw body, no HTML-to-markdown conversion yet

### ToolExecutionContext

Every tool execution receives context about where it's running:
```go
type ToolExecutionContext struct {
    SessionID    string
    UserID       string
    WorkspaceDir string
    Progress     chan<- ProgressEvent
}
```

`WorkspaceDir` is the most important — it's the root directory for all file operations. `Progress` is an optional channel for emitting status updates (the gateway can forward these to the UI).


## Layer 4: Runtime (`internal/runtime/`)

The runtime is the brain — it ties providers and tools together in a turn loop.

### Session buffer

`Session` is an in-memory conversation buffer. The system prompt is stored separately and injected by `Context()` when building the payload for the provider. This keeps the message list clean for turn counting and future summarization.

```go
session := NewSession("You are a helpful assistant.", toolSchemas)
session.Append(Message{Role: "user", Content: "fix the bug"})

ctx := session.Context()
// → Messages: [system prompt, user message]
// → Tools: [read_file, write_file, ...]
```

### The turn loop

`AgentRuntime.Run()` is the main entry point:

```
1. User runs: yantra run "add error handling to server.go"

2. CLI loads config, builds provider + registry + runtime

3. TURN LOOP (up to MaxTurns):
   a. Per-turn timeout covers streaming + tool dispatch
   b. Stream provider response, accumulate text + tool call deltas
   c. If tool calls present:
      - Dispatch respecting safety tiers and model-provided order
      - Contiguous ReadOnly calls run in parallel
      - SideEffecting/Privileged calls run sequentially at original position
      - Tool results appended to session
   d. If text-only response → return result (done)
   e. Check context budget (log warning if approaching limit)

4. Return: FinalContent, TurnsUsed, TotalUsage
```

### Stream accumulation

The provider returns a channel of `StreamItem`. The runtime's `collectStream()` method:
- Accumulates `StreamText` into the response content
- Reassembles `StreamToolCallDelta` fragments into complete `ToolCall` objects (keyed by index)
- Captures final `Usage` from the `StreamDone` event
- Propagates `StreamError` as a Go error

Tool call deltas arrive in chunks — the first delta for an index carries `ID` + `Name`, subsequent deltas append to `Arguments` via a `strings.Builder`. This handles all three providers (OpenAI, Anthropic, Gemini) uniformly.

### Tool dispatch ordering

Tools are dispatched in model-provided order with parallelism for contiguous ReadOnly blocks:

```
Call order from LLM: [read_file, read_file, write_file, read_file]
                      ├─ parallel ─┤  sequential    sequential

Block 1: read_file + read_file → parallel (both ReadOnly)
Block 2: write_file → sequential (SideEffecting)
Block 3: read_file → sequential (ReadOnly, but after a side effect)
```

This preserves correctness for patterns like `write_file → read_file` (verify what was written) while maximizing parallelism where safe.

### Error handling

The runtime classifies errors:
- Parent context cancelled → `ErrCancelled` (user pressed Ctrl-C)
- Turn context deadline exceeded → `ErrTimeout` (turn budget exhausted)
- Max turns reached → `ErrMaxTurns`
- Tool execution errors → placed in message content (the LLM sees them and can recover)

### Context budget and summarization

After each tool dispatch, the runtime estimates token usage (`totalChars / 4`) and checks if the session is approaching the context limit (`TriggerRatio * MaxContextTokens`). When triggered:

1. A `MinTurns` guard (default 6) prevents summarizing too-short conversations
2. The runtime builds a summarization prompt including the existing summary (if any) and the messages to compact
3. The LLM generates a rolling summary via a dedicated system prompt
4. The summary is stored in the `session_summaries` table with an incrementing epoch
5. `session.CompactWithSummary()` replaces older messages with a `[Conversation Summary]` pseudo-message, keeping the most recent turns

On session startup, if a prior summary exists, it's injected as the first messages so the agent has context from previous runs.

## How the pieces connect

```
yantra run "add error handling to server.go"
  │
  ├── LoadConfig()                → YantraConfig
  ├── BuildFromConfig()           → ReliableProvider(OpenAIProvider)
  ├── NewWorkspacePolicy()        → SecurityPolicy
  ├── NewRegistry() + RegisterBuiltins() → ToolRegistry
  └── runtime.New() + Run()       → AgentRuntime turn loop
       │
       ├── Session.Context()      → system prompt + messages + tool schemas
       ├── provider.Stream()      → channel of StreamItem
       ├── collectStream()        → assembled Response with ToolCalls
       ├── dispatchTools()        → tool results (parallel ReadOnly, sequential others)
       ├── checkContextBudget()   → warning if approaching limit
       └── loop until text-only response or MaxTurns
```

## Layer 5: Memory (`internal/memory/`)

Memory gives the agent persistence — it can store knowledge, recall it later, and maintain context across sessions.

### Storage: SQLite (no CGO)

The memory system uses `modernc.org/sqlite`, a pure-Go SQLite implementation. No CGO means the binary cross-compiles trivially. The database opens with **WAL mode** and a 5-second busy timeout for concurrent access safety.

Schema (6 tables):

```
chunks              — memory fragments with optional embedding BLOBs
chunks_fts          — FTS5 virtual table (porter stemmer + unicode61)
sessions            — session lifecycle tracking
conversation_events — per-session conversation log
session_summaries   — rolling summary per session (with epoch counter)
scratchpads         — key-value state per session
```

### Hybrid retrieval

Memory search combines two strategies and merges them with Reciprocal Rank Fusion (RRF):

```
Query: "how does authentication work?"
     │
     ├── Vector Search (weight: 0.7)
     │   Compute embedding → cosine similarity against all chunks
     │   Returns: semantically similar results
     │
     ├── FTS Search (weight: 0.3)
     │   SQLite FTS5 with BM25 ranking
     │   Returns: keyword-matching results
     │
     └── Reciprocal Rank Fusion (k=60)
         Merge + deduplicate by chunk ID
         Score: weight / (60 + rank) per source
         Return top K results
```

The system fetches `topK * 3` candidates from each source before fusion, ensuring good coverage. Weights are configurable — higher `VectorWeight` favors semantic matches, higher `FTSWeight` favors exact keyword matches.

**Graceful degradation:**
- No `OPENAI_API_KEY` → FTS-only search (no embeddings)
- FTS query fails (malformed syntax) → vector-only search
- No memory DB → agent runs without memory, logs a warning

### Embeddings

Embeddings are computed via the OpenAI API and stored as compact little-endian binary BLOBs (4 bytes per float32 dimension), saving ~75% compared to JSON-encoded arrays.

Supported models:

| Model | Dimensions |
|-------|-----------|
| `text-embedding-3-small` (default) | 1536 |
| `text-embedding-3-large` | 3072 |
| `text-embedding-ada-002` | 1536 |

The factory returns `nil` (not an error) when the API key is missing, so the system can always boot.

### Conversation persistence

Every message in the turn loop (user, assistant, tool results) is persisted via `StoreConversationEvent()`. Persistence uses the turn context with deadline, ensuring it respects timeouts. Failed persistence is logged as a warning but does not halt execution (fire-and-forget).

### Memory tools

Two tools expose memory to the agent:

- **`memory_save`** (SideEffecting, 15s) — stores knowledge with optional tags
- **`memory_search`** (ReadOnly, 15s) — hybrid search with ranked results

These are conditionally registered only when a `MemoryRetrieval` instance is available.

### Session store

`SQLiteSessionStore` manages session lifecycle:

| Operation | Details |
|-----------|---------|
| Create | Generates `ses_<32 hex chars>` ID |
| Get | Single session by ID |
| List | All sessions, ordered by `updated_at DESC`, optionally including archived |
| Update | Name, message count, timestamps |
| Archive | Soft-delete (sets `archived = 1`) |

### Key patterns

**Interface-driven design** — the runtime and tools depend on `types.MemoryRetrieval` and `types.SessionStore` interfaces, not concrete types. Compile-time checks enforce this:
```go
var _ types.MemoryRetrieval = (*Store)(nil)
var _ types.SessionStore = (*SQLiteSessionStore)(nil)
```

**Transaction safety** — multi-table operations (Store, Forget, StoreConversationEvent) use explicit transactions with `defer tx.Rollback()`.

**Binary embedding storage** — float32 slices are serialized as little-endian BLOBs via custom `encodeFloat32s`/`decodeFloat32s` helpers.

## What's next

| Step | What | Purpose |
|------|------|---------|
| 6 | Gateway | WebSocket server for remote control |
| 7 | MCP | Model Context Protocol client for external tools |
| 8 | TUI | Terminal UI with Bubble Tea |
| 9 | Polish | Config scaffolding, cross-platform build, docs |
