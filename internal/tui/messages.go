package tui

import "github.com/hackertron/Yantra/internal/types"

// Connection lifecycle messages.

type ConnectedMsg struct{ SessionID string }
type DisconnectedMsg struct{ Err error }
type ReconnectingMsg struct{ Attempt int }

// Server frame messages (1:1 with ServerFrameType).

type WelcomeMsg struct{ SessionID, Message string }
type TextDeltaMsg struct{ Text string }
type ToolProgressMsg struct{ Tool, Status string }
type TurnCompleteMsg struct{ Text, Status string }
type ErrorMsg struct{ Error string }
type SessionListMsg struct{ Sessions []types.SessionRecord }
type SessionCreatedMsg struct{ SessionID string }
type SessionSwitchedMsg struct{ SessionID string }

// Internal messages.

type GatewayReadyMsg struct{ Addr string }
type GatewayFailedMsg struct{ Err error }
