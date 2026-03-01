package types

// ClientFrameType identifies a frame sent by the client.
type ClientFrameType string

const (
	FrameHello      ClientFrameType = "hello"
	FrameSend       ClientFrameType = "send"
	FrameSubscribe  ClientFrameType = "subscribe"
	FrameCancel     ClientFrameType = "cancel"
	FrameSessionCmd ClientFrameType = "session_cmd"
)

// ServerFrameType identifies a frame sent by the server.
type ServerFrameType string

const (
	FrameWelcome        ServerFrameType = "welcome"
	FrameTextDelta      ServerFrameType = "text_delta"
	FrameToolProgress   ServerFrameType = "tool_progress"
	FrameTurnComplete   ServerFrameType = "turn_complete"
	FrameError          ServerFrameType = "error"
	FrameSessionList    ServerFrameType = "session_list"
	FrameSessionCreated ServerFrameType = "session_created"
	FrameSessionSwitched ServerFrameType = "session_switched"
)

// ClientFrame is a message from the client to the gateway.
type ClientFrame struct {
	Type      ClientFrameType `json:"type"`
	APIKey    string          `json:"api_key,omitempty"`    // hello
	SessionID string          `json:"session_id,omitempty"` // send, subscribe
	Content   string          `json:"content,omitempty"`    // send
	Command   string          `json:"command,omitempty"`    // session_cmd
	Args      string          `json:"args,omitempty"`       // session_cmd
}

// ServerFrame is a message from the gateway to the client.
type ServerFrame struct {
	Type      ServerFrameType `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Text      string          `json:"text,omitempty"`       // text_delta
	Tool      string          `json:"tool,omitempty"`       // tool_progress
	Status    string          `json:"status,omitempty"`     // tool_progress, turn_complete
	Error     string          `json:"error,omitempty"`      // error
	Sessions  []SessionRecord `json:"sessions,omitempty"`   // session_list
	Message   string          `json:"message,omitempty"`    // welcome, info
}

// Channel is the interface that channel adapters must implement.
type Channel interface {
	// Name returns the channel identifier (e.g., "telegram", "discord").
	Name() string

	// Start begins listening for inbound messages.
	Start() error

	// Stop gracefully shuts down the channel.
	Stop() error

	// Health returns the current health status.
	Health() string
}
