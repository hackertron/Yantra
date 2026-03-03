# Tool System Deep Dive

This document covers everything about how Yantra's tool system works — how tools are defined, registered, secured, and executed.

## What is a tool?

A tool is a function the LLM can call. The LLM doesn't execute code directly — it outputs a structured request ("I want to call read_file with path=main.go"), and the runtime executes it on the LLM's behalf.

This is the fundamental mechanism that turns a chatbot into an agent. Without tools, an LLM can only generate text. With tools, it can interact with the world.

## The Tool interface

Every tool in Yantra implements this interface:

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

Let's break down each method:

### Name()

A unique string identifier like `"read_file"` or `"shell_exec"`. This is what the LLM uses in its tool call. Must be unique across the registry.

### Description()

A human-readable sentence explaining what the tool does. This goes to the LLM — it reads this to decide when to use the tool. Good descriptions matter. A vague description means the LLM won't know when to reach for the tool.

### Decl()

Returns the `FunctionDecl` — the complete package sent to the LLM:

```go
type FunctionDecl struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}
```

The Parameters field is a JSON Schema object describing the tool's inputs. For example, read_file's schema:

```json
{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "File path (relative to workspace or absolute)"},
    "offset": {"type": "integer", "description": "Line number to start reading from (1-based, default 1)"},
    "limit": {"type": "integer", "description": "Maximum number of lines to read (default 2000)"}
  },
  "required": ["path"],
  "additionalProperties": false
}
```

The LLM reads this schema and generates valid JSON arguments when calling the tool.

### Execute()

The actual implementation. Receives:
- `ctx` — Go context with cancellation and deadline (the registry sets the timeout here)
- `input` — raw JSON matching the Parameters schema
- `execCtx` — workspace directory, session ID, progress channel

Returns a string result (what the LLM sees) or an error.

### SafetyTier()

One of three values:
- `ReadOnly` — no side effects (reading files, listing directories)
- `SideEffecting` — changes state (writing files, making HTTP requests)
- `Privileged` — potentially dangerous (running shell commands)

The runtime uses these to decide execution strategy. Contiguous ReadOnly tools run in parallel; SideEffecting and Privileged tools run sequentially at their original position in the call list. This preserves model-provided ordering for cross-tool dependencies (e.g., `write_file` then `read_file`) while maximizing parallelism where safe.

### Timeout()

How long the tool gets before the context is cancelled. Each tool sets its own timeout:
- File operations: 10 seconds
- Shell commands: 60 seconds (builds are slow)
- HTTP requests: 30 seconds

## Schema builder

Building JSON Schema by hand is tedious and error-prone. The `Schema()` function does it declaratively:

```go
Schema(
    Prop{Name: "path", Type: TypeString, Description: "File path", Required: true},
    Prop{Name: "limit", Type: TypeInteger, Description: "Max lines"},
    Prop{Name: "tags", Type: TypeArray, Description: "Tags", Items: &TypeString},
    Prop{Name: "mode", Type: TypeString, Description: "Write mode", Enum: []string{"overwrite", "append"}},
)
```

Available types: `TypeString`, `TypeInteger`, `TypeNumber`, `TypeBoolean`, `TypeArray`.

For arrays, set `Items` to the element type. For enums, provide the valid values list.

The output is a `json.RawMessage` ready to drop into `FunctionDecl.Parameters`.

## Registry

The `ToolRegistry` is the central hub:

```go
registry := tool.NewRegistry(policy)
tool.RegisterBuiltins(registry, config.Tools)
```

### Registration

Tools register at startup. Duplicate names are rejected — this catches bugs where two tools accidentally use the same name.

```go
registry.Register(NewReadFile())   // ok
registry.Register(NewReadFile())   // error: "read_file" already registered
```

### Schema export

```go
// All tool schemas (for the main agent)
allSchemas := registry.Schemas(nil)

// Filtered schemas (for a specialist agent that only gets file tools)
fileSchemas := registry.Schemas([]string{"read_file", "write_file", "list_files"})
```

This is how per-agent tool filtering works. In the config, an `AgentDefinition` has a `Tools` field listing which tools it can use. The registry's Schemas method accepts that list directly.

### Execution flow

```
registry.Execute(ctx, "read_file", '{"path":"main.go"}', execCtx)
    │
    ├── 1. Look up tool by name (fail if not found)
    │
    ├── 2. Policy check
    │      policy.CheckExecution(tool, input, execCtx)
    │      - For file tools: validate path is inside workspace
    │      - For shell: validate command against allow/deny lists
    │      - Returns error if blocked
    │
    ├── 3. Emit progress event
    │      execCtx.Progress <- ProgressEvent{Kind: "tool_execution", Tool: "read_file"}
    │
    ├── 4. Apply timeout
    │      ctx, cancel = context.WithTimeout(ctx, tool.Timeout())
    │
    ├── 5. Execute
    │      output, err = tool.Execute(ctx, input, execCtx)
    │
    └── 6. Truncate output
           if len(output) > 128KB: cut at last newline, append "[output truncated]"
```

### Output truncation

LLMs have context limits. If a tool returns a 5MB log file, it would blow the context budget. The registry truncates output at 128KB by default (configurable via `SetMaxOutputBytes`).

Truncation is line-boundary-aware — it finds the last newline before the limit and cuts there, appending `\n... [output truncated]`. This avoids sending the LLM a partial line that might confuse it.

## Security policy

The `SecurityPolicy` interface has one method:

```go
type SecurityPolicy interface {
    CheckExecution(t types.Tool, input json.RawMessage, execCtx types.ToolExecutionContext) error
}
```

Return `nil` to allow, return an error to block. The registry calls this before every tool execution.

### WorkspacePolicy

The default policy, `WorkspacePolicy`, enforces three categories of rules.

#### Path containment

File tools (`read_file`, `write_file`, `list_files`) must operate within the workspace:

```
Workspace: /home/user/project

"src/main.go"            → /home/user/project/src/main.go     ALLOWED
"./test/../src/main.go"  → /home/user/project/src/main.go     ALLOWED
"../../etc/passwd"        → /home/etc/passwd                   BLOCKED
"/etc/shadow"             → /etc/shadow                        BLOCKED
"/home/user/project/ok"  → /home/user/project/ok              ALLOWED
```

The path is cleaned (`filepath.Clean` resolves `.` and `..`), then checked with `strings.HasPrefix` against the workspace path. An absolute path is allowed only if it's inside the workspace.

#### Shell command filtering

The shell_exec tool extracts the base command name and checks it against two lists:

**Default allowlist** (~40 commands):
```
ls, cat, head, tail, grep, find, git, go, node, python, curl, make, docker,
mkdir, cp, mv, touch, tar, jq, tree, ...
```

**Default denylist**:
```
sudo, su, doas, rm, mkfs, fdisk, dd, shutdown, reboot, halt, poweroff, init
```

**The rule: deny always overrides allow.** If a command is in both lists, it's blocked.

You can customize via config:
```toml
[tools.shell]
allow = ["mycustomtool"]           # add to allowlist
deny = ["docker"]                   # add to denylist
replace_defaults = false            # true = start from scratch instead of extending defaults
allow_operators = false             # true = permit |, &&, ||, etc.
```

#### Operator blocking

By default, these shell metacharacters are blocked:

```
|   &&   ||   ;   >   >>   <   $(   `
```

This prevents command chaining that could bypass the command filter. Without this, an LLM could write `ls | rm -rf /` — `ls` is allowed, but the pipe chains it with a destructive command.

Set `allow_operators = true` in config if your use case needs pipes and redirects.

## Built-in tools reference

### read_file

**Purpose:** Read file contents so the LLM can understand code.

**Parameters:**
| Name   | Type    | Required | Default | Description |
|--------|---------|----------|---------|-------------|
| path   | string  | yes      | —       | File path relative to workspace or absolute |
| offset | integer | no       | 1       | Start reading from this line (1-based) |
| limit  | integer | no       | 2000    | Maximum lines to return |

**Output format:**
```
     1	package main
     2
     3	import "fmt"
     4
     5	func main() {
     6		fmt.Println("hello")
     7	}
```

Line numbers are right-aligned in a 6-character field, separated by a tab. This format lets the LLM reference specific lines ("the bug is on line 42").

**Edge cases:**
- Empty file → `"(empty file or offset beyond end of file)"`
- Offset past end → same empty message
- Binary files → will output garbage (not handled yet)

### write_file

**Purpose:** Create or modify files.

**Parameters:**
| Name    | Type    | Required | Default | Description |
|---------|---------|----------|---------|-------------|
| path    | string  | yes      | —       | File path |
| content | string  | yes      | —       | Content to write |
| append  | boolean | no       | false   | Append instead of overwrite |

**Behavior:**
- Creates parent directories automatically (like `mkdir -p`)
- New files get `0644` permissions, directories get `0755`
- Overwrite mode truncates the file first
- Returns: `"wrote 1234 bytes to src/main.go"` or `"appended 56 bytes to log.txt"`

### list_files

**Purpose:** Explore directory structure.

**Parameters:**
| Name      | Type    | Required | Default | Description |
|-----------|---------|----------|---------|-------------|
| path      | string  | yes      | —       | Directory path |
| recursive | boolean | no       | false   | Recurse into subdirectories |
| max_depth | integer | no       | 3       | Max recursion depth |

**Output format (flat):**
```
main.go
go.mod
go.sum
internal/
cmd/
```

Directories have a trailing `/`.

**Output format (recursive):**
```
main.go
go.mod
internal/
internal/types/
internal/types/config.go
internal/types/tool.go
cmd/
cmd/yantra/
cmd/yantra/main.go
```

**Depth limiting:** `max_depth=1` shows only immediate children. `max_depth=2` goes one level deeper. Default is 3 to prevent huge outputs on deep trees.

### shell_exec

**Purpose:** Run arbitrary commands for builds, tests, git operations, etc.

**Parameters:**
| Name    | Type   | Required | Description |
|---------|--------|----------|-------------|
| command | string | yes      | Shell command to execute |

**Execution:** Runs via `sh -c "<command>"` in the workspace directory.

**Output format:**
```
exit_code: 0
stdout:
hello world
stderr:
```

Non-zero exit codes are **not** treated as errors — the result is still returned to the LLM so it can read error messages and fix the problem. Only actual execution failures (binary not found, context cancelled) return errors.

**Security:** This is the only Privileged-tier tool. It goes through command allowlist/denylist and operator checks before execution.

### web_fetch

**Purpose:** Fetch data from URLs.

**Parameters:**
| Name    | Type   | Required | Default | Description |
|---------|--------|----------|---------|-------------|
| url     | string | yes      | —       | URL to fetch |
| method  | string | no       | GET     | HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD) |
| body    | string | no       | —       | Request body |
| headers | string | no       | —       | Custom headers as JSON object string |

**Output format:**
```
status: 200

{"data": [1, 2, 3]}
```

**Limits:** Both request and response bodies are capped at 1MB.

**Headers example:** `{"Authorization": "Bearer xxx", "Content-Type": "application/json"}`

**Current limitations:**
- Returns raw response body (HTML pages come back as raw HTML)
- No HTML-to-markdown conversion (planned for later)
- No cookie handling
- No redirect following configuration (uses Go's default: follows up to 10 redirects)

### memory_save

**Purpose:** Persist knowledge for future recall across sessions.

**Parameters:**
| Name    | Type           | Required | Description |
|---------|----------------|----------|-------------|
| content | string         | yes      | The knowledge to store |
| tags    | array (string) | no       | Optional tags for categorization |

**Behavior:**
- Calls `mem.Store()` with source `"user_saved"`
- If an embedding backend is configured, computes and stores the embedding alongside the content
- Returns: `"Saved to memory (id: <hex>)"`

**Safety tier:** SideEffecting (15s timeout)

### memory_search

**Purpose:** Search persistent memory using hybrid retrieval (vector + full-text).

**Parameters:**
| Name  | Type    | Required | Default | Description |
|-------|---------|----------|---------|-------------|
| query | string  | yes      | —       | Search query (used for both semantic and keyword matching) |
| top_k | integer | no       | 5       | Maximum number of results to return |

**Output format:**
```
1. [score: 0.85] Content of the memory chunk
   Tags: tag1, tag2

2. [score: 0.72] Another memory chunk
   Tags: general
```

Returns `"No matching memories found."` when no results match.

**Safety tier:** ReadOnly (15s timeout)

**Note:** Both memory tools are conditionally registered — they only appear in the tool list when a `MemoryRetrieval` instance is provided to `RegisterBuiltins`. If memory is disabled or the database fails to open, the agent simply doesn't have these tools.

## Writing a custom tool

To add a new tool, implement the `Tool` interface:

```go
package tool

import (
    "context"
    "encoding/json"
    "time"
    "github.com/hackertron/Yantra/internal/types"
)

type myTool struct{}

func NewMyTool() types.Tool { return &myTool{} }

func (t *myTool) Name() string        { return "my_tool" }
func (t *myTool) Description() string  { return "Does something useful" }
func (t *myTool) SafetyTier() types.SafetyTier { return types.ReadOnly }
func (t *myTool) Timeout() time.Duration       { return 10 * time.Second }

func (t *myTool) Decl() types.FunctionDecl {
    return types.FunctionDecl{
        Name:        t.Name(),
        Description: t.Description(),
        Parameters: Schema(
            Prop{Name: "query", Type: TypeString, Description: "Search query", Required: true},
        ),
    }
}

func (t *myTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
    var args struct {
        Query string `json:"query"`
    }
    if err := json.Unmarshal(input, &args); err != nil {
        return "", fmt.Errorf("invalid input: %w", err)
    }

    // Do work here...

    return "result", nil
}
```

Then register it:
```go
registry.Register(NewMyTool())
```

The LLM will automatically see it in its tool list and can call it by name.
