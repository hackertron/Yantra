package types

import "time"

// MessageRole identifies the sender of a message.
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// Message represents a single message in a conversation.
type Message struct {
	Role       MessageRole `json:"role"`
	Content    string      `json:"content,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	CreatedAt  time.Time   `json:"created_at,omitempty"`
}

// ToolCall represents a function call requested by the LLM.
type ToolCall struct {
	ID       string       `json:"id"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the name and arguments of a tool invocation.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string
}

// Context is the ordered conversation history sent to the provider.
type Context struct {
	Messages []Message         `json:"messages"`
	Tools    []FunctionDecl    `json:"tools,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Usage holds token counts reported by the provider.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Response is the complete result from a provider call.
type Response struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason,omitempty"`
	Usage        Usage   `json:"usage"`
}

// StreamItemType distinguishes stream events.
type StreamItemType int

const (
	StreamText StreamItemType = iota
	StreamToolCallDelta
	StreamDone
	StreamError
	StreamProgress
)

// ToolCallDelta is an incremental chunk of a tool call during streaming.
type ToolCallDelta struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // partial JSON
}

// ProgressKind identifies the phase of a runtime progress event.
type ProgressKind string

const (
	ProgressProviderCall   ProgressKind = "provider_call"
	ProgressToolExecution  ProgressKind = "tool_execution"
	ProgressSummarization  ProgressKind = "summarization"
	ProgressMemoryRetrieval ProgressKind = "memory_retrieval"
)

// ProgressEvent is emitted by the runtime to signal phase transitions.
type ProgressEvent struct {
	Kind    ProgressKind `json:"kind"`
	Tool    string       `json:"tool,omitempty"`
	Message string       `json:"message,omitempty"`
}

// StreamItem is a single event in a provider response stream.
type StreamItem struct {
	Type          StreamItemType
	Text          string
	ToolCallDelta *ToolCallDelta
	Usage         *Usage
	Progress      *ProgressEvent
	Error         error
}
