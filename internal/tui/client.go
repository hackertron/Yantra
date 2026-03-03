package tui

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gorilla/websocket"
	"github.com/hackertron/Yantra/internal/types"
)

// Client is a WebSocket client that bridges server frames into tea.Msg.
type Client struct {
	addr      string
	apiKey    string
	conn      *websocket.Conn
	program   *tea.Program
	mu        sync.Mutex // gorilla writes are not concurrent-safe
	sessionID string
	done      chan struct{}
}

// NewClient creates a new WebSocket client for the given gateway address.
func NewClient(addr, apiKey string) *Client {
	return &Client{
		addr:   addr,
		apiKey: apiKey,
		done:   make(chan struct{}),
	}
}

// Connect dials the WebSocket, sends the hello frame, and starts readLoop.
func (c *Client) Connect(p *tea.Program) tea.Cmd {
	c.program = p
	return func() tea.Msg {
		url := fmt.Sprintf("ws://%s/ws", c.addr)
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			return DisconnectedMsg{Err: fmt.Errorf("connect: %w", err)}
		}
		c.mu.Lock()
		c.conn = conn
		c.mu.Unlock()

		// Send hello frame.
		hello := types.ClientFrame{
			Type:   types.FrameHello,
			APIKey: c.apiKey,
		}
		if err := c.writeFrame(hello); err != nil {
			conn.Close()
			return DisconnectedMsg{Err: fmt.Errorf("hello: %w", err)}
		}

		go c.readLoop()
		return ConnectedMsg{}
	}
}

// readLoop reads frames from the WebSocket and sends them as tea.Msg.
func (c *Client) readLoop() {
	defer func() {
		close(c.done)
	}()

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			if c.program != nil {
				c.program.Send(DisconnectedMsg{Err: err})
			}
			return
		}

		var frame types.ServerFrame
		if err := json.Unmarshal(msg, &frame); err != nil {
			continue
		}

		teaMsg := c.frameToMsg(frame)
		if teaMsg != nil && c.program != nil {
			c.program.Send(teaMsg)
		}
	}
}

// frameToMsg converts a ServerFrame to the corresponding tea.Msg.
func (c *Client) frameToMsg(f types.ServerFrame) tea.Msg {
	switch f.Type {
	case types.FrameWelcome:
		c.sessionID = f.SessionID
		return WelcomeMsg{SessionID: f.SessionID, Message: f.Message}
	case types.FrameTextDelta:
		return TextDeltaMsg{Text: f.Text}
	case types.FrameToolProgress:
		return ToolProgressMsg{Tool: f.Tool, Status: f.Status}
	case types.FrameTurnComplete:
		return TurnCompleteMsg{Text: f.Text, Status: f.Status}
	case types.FrameError:
		return ErrorMsg{Error: f.Error}
	case types.FrameSessionList:
		return SessionListMsg{Sessions: f.Sessions}
	case types.FrameSessionCreated:
		c.sessionID = f.SessionID
		return SessionCreatedMsg{SessionID: f.SessionID}
	case types.FrameSessionSwitched:
		c.sessionID = f.SessionID
		return SessionSwitchedMsg{SessionID: f.SessionID}
	default:
		return nil
	}
}

// SendMessage sends a user message to the current session.
func (c *Client) SendMessage(content string) error {
	return c.writeFrame(types.ClientFrame{
		Type:      types.FrameSend,
		SessionID: c.sessionID,
		Content:   content,
	})
}

// SendSessionCmd sends a session management command.
func (c *Client) SendSessionCmd(command, args string) error {
	return c.writeFrame(types.ClientFrame{
		Type:    types.FrameSessionCmd,
		Command: command,
		Args:    args,
	})
}

// SendCancel sends a cancel frame to abort the current turn.
func (c *Client) SendCancel() error {
	return c.writeFrame(types.ClientFrame{
		Type: types.FrameCancel,
	})
}

// Close cleanly shuts down the WebSocket connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		c.conn.Close()
		c.conn = nil
	}
}

// Reconnect attempts to reconnect with exponential backoff.
func (c *Client) Reconnect() tea.Cmd {
	return func() tea.Msg {
		maxAttempts := 10
		delay := time.Second

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if c.program != nil {
				c.program.Send(ReconnectingMsg{Attempt: attempt})
			}
			time.Sleep(delay)

			url := fmt.Sprintf("ws://%s/ws", c.addr)
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				delay = min(delay*2, 10*time.Second)
				continue
			}

			c.mu.Lock()
			c.conn = conn
			c.done = make(chan struct{})
			c.mu.Unlock()

			// Re-send hello with existing session ID to resume.
			hello := types.ClientFrame{
				Type:      types.FrameHello,
				APIKey:    c.apiKey,
				SessionID: c.sessionID,
			}
			if err := c.writeFrame(hello); err != nil {
				conn.Close()
				delay = min(delay*2, 10*time.Second)
				continue
			}

			go c.readLoop()
			return ConnectedMsg{SessionID: c.sessionID}
		}
		return DisconnectedMsg{Err: fmt.Errorf("reconnect failed after %d attempts", maxAttempts)}
	}
}

// writeFrame marshals and writes a client frame. Thread-safe.
func (c *Client) writeFrame(frame types.ClientFrame) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}
