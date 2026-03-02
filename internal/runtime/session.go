package runtime

import (
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

// Session is an in-memory conversation buffer. It holds the system prompt
// separately from the message list so that turn counting and future
// summarization only deal with user/assistant/tool messages.
//
// A Session is not safe for concurrent use — a single goroutine owns it.
type Session struct {
	systemPrompt string
	messages     []types.Message
	tools        []types.FunctionDecl
}

// NewSession creates a Session with the given system prompt and tool declarations.
func NewSession(systemPrompt string, tools []types.FunctionDecl) *Session {
	return &Session{
		systemPrompt: systemPrompt,
		tools:        tools,
	}
}

// Append adds a message to the conversation. If CreatedAt is zero it is
// stamped with the current time.
func (s *Session) Append(msg types.Message) {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	s.messages = append(s.messages, msg)
}

// Context builds the types.Context that is sent to the provider. The system
// prompt is injected as the first message; tool declarations are attached.
func (s *Session) Context() *types.Context {
	msgs := make([]types.Message, 0, 1+len(s.messages))
	if s.systemPrompt != "" {
		msgs = append(msgs, types.Message{
			Role:    types.RoleSystem,
			Content: s.systemPrompt,
		})
	}
	msgs = append(msgs, s.messages...)

	return &types.Context{
		Messages: msgs,
		Tools:    s.tools,
	}
}

// Messages returns a copy of the conversation messages (excluding the system prompt).
func (s *Session) Messages() []types.Message {
	out := make([]types.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// Len returns the number of non-system messages.
func (s *Session) Len() int {
	return len(s.messages)
}
