# Provider Layer Deep Dive

This document explains how Yantra talks to LLMs — the provider abstraction, how each API differs, and how the reliable wrapper handles failures.

## The problem

There are three major LLM APIs, and they all work differently:

| Aspect | OpenAI | Anthropic | Gemini |
|--------|--------|-----------|--------|
| Endpoint | Chat Completions | Messages | GenerateContent |
| Message format | `{role, content, tool_calls}` | `{role, content: [blocks]}` | `{role, parts: [parts]}` |
| System message | Regular message with role "system" | Separate `system` parameter | `SystemInstruction` config |
| Tool calls | `tool_calls` array on message | `tool_use` content block | `FunctionCall` part |
| Tool results | Message with role "tool" | `tool_result` content block | `FunctionResponse` part |
| Token counting | `usage.prompt_tokens` | `usage.input_tokens` | `UsageMetadata.PromptTokenCount` |

Without an abstraction, the runtime would need `if openai ... else if anthropic ... else if gemini` everywhere. That's unmaintainable.

## The Provider interface

```go
type Provider interface {
    Complete(ctx context.Context, c *Context) (*Response, error)
    Stream(ctx context.Context, c *Context) <-chan StreamItem
    ProviderID() ProviderID
    ModelID() ModelID
    MaxContextTokens() int
}
```

`Context` (not to be confused with Go's `context.Context`) is the conversation:
```go
type Context struct {
    Messages []Message
    Tools    []FunctionDecl
    Metadata map[string]string
}
```

`Response` is what comes back:
```go
type Response struct {
    Message      Message  // The LLM's reply
    FinishReason string   // "stop", "tool_calls", etc.
    Usage        Usage    // Token counts
}
```

Every provider converts `Context` into its API format, makes the HTTP call, and converts the response back. The runtime never sees API-specific types.

## How each provider works

### OpenAI

**SDK:** `github.com/openai/openai-go/v3`

**Message conversion (`convertMessagesOpenAI`):**
- `system` → OpenAI system message
- `user` → OpenAI user message
- `assistant` → OpenAI assistant message (if it has ToolCalls, they're converted to OpenAI's `tool_calls` format)
- `tool` → OpenAI tool message with `tool_call_id`

**Tool conversion (`convertToolsOpenAI`):**
```
FunctionDecl → OpenAI ChatCompletionTool{
    Type: "function",
    Function: {Name, Description, Parameters}
}
```

OpenAI's format is the simplest — `Parameters` goes straight through as JSON Schema.

**Streaming:**
OpenAI streams `ChatCompletionChunkChoice` objects. Each chunk can contain:
- `Delta.Content` — text fragment → `StreamText`
- `Delta.ToolCalls` — incremental tool call data → `StreamToolCallDelta`
- Final chunk with `Usage` → `StreamDone`

**Default context window:** 128,000 tokens

### Anthropic

**SDK:** `github.com/anthropics/anthropic-sdk-go`

**Key difference:** Anthropic uses content blocks, not flat fields.

An Anthropic message's content is an array of typed blocks:
```json
[
  {"type": "text", "text": "Let me read that file."},
  {"type": "tool_use", "id": "call_123", "name": "read_file", "input": {"path": "main.go"}}
]
```

**Message conversion (`convertMessagesAnthropic`):**
- System messages are extracted and merged into Anthropic's separate `system` parameter
- `user` → Anthropic user message with text content block
- `assistant` → Text block + tool_use blocks (if tool calls present)
- `tool` → `tool_result` content block inside a user message, referencing the tool_use ID

**Tool conversion (`convertToolsAnthropic`):**
```
FunctionDecl → Anthropic Tool{
    Name, Description,
    InputSchema: Parameters (as JSON Schema)
}
```

**Streaming:**
Anthropic streams events:
- `ContentBlockDelta` with `TextDelta` → `StreamText`
- `ContentBlockDelta` with `InputJSONDelta` → `StreamToolCallDelta` (tool arguments arrive as JSON fragments)
- `MessageDelta` with `Usage` → `StreamDone`

**Default context window:** 200,000 tokens

### Gemini

**SDK:** `google.golang.org/genai`

**Key difference:** Gemini uses `Content` with `Part` arrays and has its own schema format.

**Message conversion (`convertMessagesGemini`):**
- System messages go into `SystemInstruction` (a config field, not a message)
- `user` → Content with role "user" and Text part
- `assistant` → Content with role "model" (Gemini calls it "model", not "assistant")
- `tool` → Content with role "tool" and FunctionResponse part

**Tool conversion — the tricky part:**

Gemini doesn't accept raw JSON Schema. It has its own `genai.Schema` struct:
```go
type Schema struct {
    Type        Type
    Properties  map[string]*Schema
    Required    []string
    Description string
    Enum        []string
    Items       *Schema
}
```

So Yantra has `jsonSchemaToGeminiSchema()` — a recursive converter that walks the JSON Schema and builds Gemini's native schema objects. It handles nested objects, arrays, enums, and required fields.

**Streaming:**
Gemini streams `GenerateContentResponse` objects. Each response's `Candidates[0].Content.Parts` can contain:
- `Text` parts → `StreamText`
- `FunctionCall` parts → `StreamToolCallDelta`
- Final response with `UsageMetadata` → `StreamDone`

**Default context window:** 1,000,000 tokens (Gemini has the largest context)

## The factory

```go
func Build(name string, entry ProviderRegistryEntry, model string) (Provider, error)
```

The factory is the only place that knows about concrete provider types. It:
1. Resolves the API key from environment variables
2. Routes to the right constructor based on `ProviderType`
3. Returns the provider behind the `Provider` interface

**API key resolution chain:**
```
1. Check entry.APIKeyEnv (explicit override in config)
2. Check provider-specific default:
   - OpenAI    → OPENAI_API_KEY
   - Anthropic → ANTHROPIC_API_KEY
   - Gemini    → GEMINI_API_KEY
3. Check generic API_KEY fallback
4. Error if nothing found
```

**Convenience wrapper:**
```go
func BuildFromConfig(cfg *YantraConfig) (Provider, error)
```
Pulls `cfg.Selection.Provider` and `cfg.Selection.Model`, looks up the provider registry entry, and calls `Build`.

## Reliable wrapper

```go
reliable := NewReliableProvider(inner, DefaultReliableConfig())
```

The `ReliableProvider` decorates any `Provider` with automatic retries.

### What gets retried

The `isRetryable` function checks several conditions:

```go
// Retryable conditions:
- ProviderError with Retryable: true
- HTTP 429 (rate limited)
- HTTP 5xx (server error)
- Connection refused/reset/timeout
- Unexpected EOF
```

Non-retryable:
- HTTP 400 (bad request — your fault, retrying won't help)
- HTTP 401/403 (auth error)
- Context cancelled (user cancelled)

### Backoff strategy

```
Attempt 1: immediate
Attempt 2: 250ms (± jitter)
Attempt 3: 500ms (± jitter)
Attempt 4: 1000ms (± jitter)  [if max_attempts > 3]
...
Cap: 2000ms
```

**Exponential backoff:** Each wait doubles from the base (250ms).

**Jitter:** A random component (±50%) prevents thundering herd. If 100 requests all hit a rate limit at the same time, you don't want them all retrying at exactly 250ms — they'd all hit the limit again. Jitter spreads them out.

**Cap:** Backoff never exceeds 2 seconds. Waiting longer than that usually means the problem isn't transient.

### Streaming behavior

For `Stream()`, retries only happen during connection setup — before the first `StreamItem` arrives. Once streaming starts, a failure mid-stream is not retried because:
1. The LLM has already started generating (retrying would restart generation)
2. Partial results may have already been shown to the user
3. The context may have changed (tool results appended)

## Configuration

Provider configuration lives in `yantra.toml`:

```toml
[selection]
provider = "openai"
model = "gpt-4o"

[providers.registry.openai]
provider_type = "openai"
api_key_env = "OPENAI_API_KEY"
max_context_tokens = 128000

[providers.registry.anthropic]
provider_type = "anthropic"
api_key_env = "ANTHROPIC_API_KEY"
max_context_tokens = 200000

[providers.registry.gemini]
provider_type = "gemini"
api_key_env = "GEMINI_API_KEY"
max_context_tokens = 1000000
```

Switching providers is a one-line change:
```toml
provider = "anthropic"
model = "claude-sonnet-4-20250514"
```

Custom endpoints (for proxies, Azure OpenAI, or self-hosted models):
```toml
[providers.registry.local]
provider_type = "openai"
base_url = "http://localhost:8080/v1"
api_key_env = "LOCAL_API_KEY"
```

Any OpenAI-compatible API works with the OpenAI provider type — just set the `base_url`.
