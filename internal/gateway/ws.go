package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/hackertron/Yantra/internal/types"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true }, // permissive for dev
}

// wsConn wraps a single WebSocket connection.
type wsConn struct {
	conn      *websocket.Conn
	server    *Server
	sessionID string
	authed    bool
	writeMu   sync.Mutex // gorilla writes are not concurrent-safe
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}

	ws := &wsConn{
		conn:   conn,
		server: s,
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer conn.Close()

	ws.readLoop(ctx)
}

func (ws *wsConn) readLoop(ctx context.Context) {
	for {
		_, msg, err := ws.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Warn("websocket read error", "error", err)
			}
			return
		}

		var frame types.ClientFrame
		if err := json.Unmarshal(msg, &frame); err != nil {
			ws.sendFrame(types.ServerFrame{
				Type:  types.FrameError,
				Error: "invalid frame: " + err.Error(),
			})
			continue
		}

		ws.dispatch(ctx, frame)
	}
}

func (ws *wsConn) dispatch(ctx context.Context, frame types.ClientFrame) {
	// First frame must be hello.
	if !ws.authed {
		if frame.Type != types.FrameHello {
			ws.sendFrame(types.ServerFrame{
				Type:  types.FrameError,
				Error: "first frame must be hello",
			})
			return
		}
		ws.handleHello(ctx, frame)
		return
	}

	switch frame.Type {
	case types.FrameSend:
		ws.handleSend(ctx, frame)
	case types.FrameCancel:
		ws.handleCancel()
	case types.FrameSessionCmd:
		ws.handleSessionCmd(ctx, frame)
	case types.FrameSubscribe:
		ws.handleSubscribe(ctx, frame)
	default:
		ws.sendFrame(types.ServerFrame{
			Type:  types.FrameError,
			Error: "unknown frame type: " + string(frame.Type),
		})
	}
}

func (ws *wsConn) handleHello(ctx context.Context, frame types.ClientFrame) {
	// Authenticate if API key is configured.
	if ws.server.cfg.APIKey != "" {
		if frame.APIKey == "" || !ws.server.validateAPIKey(frame.APIKey) {
			ws.sendFrame(types.ServerFrame{
				Type:  types.FrameError,
				Error: "authentication failed",
			})
			return
		}
	}
	ws.authed = true

	// Create or resume a session.
	var ms *ManagedSession
	var err error
	if frame.SessionID != "" {
		ms, err = ws.server.sessMgr.GetOrCreate(ctx, frame.SessionID)
	} else {
		ms, err = ws.server.sessMgr.newSession(ctx, "ws-session")
	}
	if err != nil {
		ws.sendFrame(types.ServerFrame{
			Type:  types.FrameError,
			Error: "session error: " + err.Error(),
		})
		return
	}

	ws.sessionID = ms.Record.ID

	ws.sendFrame(types.ServerFrame{
		Type:      types.FrameWelcome,
		SessionID: ms.Record.ID,
		Message:   "connected",
	})
}

func (ws *wsConn) handleSend(ctx context.Context, frame types.ClientFrame) {
	sid := frame.SessionID
	if sid == "" {
		sid = ws.sessionID
	}
	if sid == "" {
		ws.sendFrame(types.ServerFrame{
			Type:  types.FrameError,
			Error: "no active session",
		})
		return
	}

	ms, err := ws.server.sessMgr.GetOrCreate(ctx, sid)
	if err != nil {
		ws.sendFrame(types.ServerFrame{
			Type:  types.FrameError,
			Error: err.Error(),
		})
		return
	}

	// Run the turn in a goroutine so the read loop can still process cancel.
	go ws.executeTurnWS(ctx, ms, frame.Content)
}

func (ws *wsConn) handleCancel() {
	ms := ws.server.sessMgr.Get(ws.sessionID)
	if ms != nil && ms.CancelFunc != nil {
		ms.CancelFunc()
	}
}

func (ws *wsConn) handleSessionCmd(ctx context.Context, frame types.ClientFrame) {
	switch frame.Command {
	case "list":
		sessions, err := ws.server.sessStore.List(ctx, false)
		if err != nil {
			ws.sendFrame(types.ServerFrame{
				Type:  types.FrameError,
				Error: err.Error(),
			})
			return
		}
		ws.sendFrame(types.ServerFrame{
			Type:     types.FrameSessionList,
			Sessions: sessions,
		})

	case "new":
		name := frame.Args
		if name == "" {
			name = "ws-session"
		}
		ms, err := ws.server.sessMgr.newSession(ctx, name)
		if err != nil {
			ws.sendFrame(types.ServerFrame{
				Type:  types.FrameError,
				Error: err.Error(),
			})
			return
		}
		ws.sessionID = ms.Record.ID
		ws.sendFrame(types.ServerFrame{
			Type:      types.FrameSessionCreated,
			SessionID: ms.Record.ID,
		})

	case "switch":
		sid := frame.Args
		if sid == "" {
			ws.sendFrame(types.ServerFrame{
				Type:  types.FrameError,
				Error: "session_id required for switch",
			})
			return
		}
		ms, err := ws.server.sessMgr.GetOrCreate(ctx, sid)
		if err != nil {
			ws.sendFrame(types.ServerFrame{
				Type:  types.FrameError,
				Error: err.Error(),
			})
			return
		}
		ws.sessionID = ms.Record.ID
		ws.sendFrame(types.ServerFrame{
			Type:      types.FrameSessionSwitched,
			SessionID: ms.Record.ID,
		})

	default:
		ws.sendFrame(types.ServerFrame{
			Type:  types.FrameError,
			Error: "unknown session command: " + frame.Command,
		})
	}
}

func (ws *wsConn) handleSubscribe(ctx context.Context, frame types.ClientFrame) {
	if frame.SessionID == "" {
		ws.sendFrame(types.ServerFrame{
			Type:  types.FrameError,
			Error: "session_id required for subscribe",
		})
		return
	}

	ms, err := ws.server.sessMgr.GetOrCreate(ctx, frame.SessionID)
	if err != nil {
		ws.sendFrame(types.ServerFrame{
			Type:  types.FrameError,
			Error: err.Error(),
		})
		return
	}

	ws.sessionID = ms.Record.ID
	ws.sendFrame(types.ServerFrame{
		Type:      types.FrameSessionSwitched,
		SessionID: ms.Record.ID,
	})
}

// sendFrame serialises and writes a server frame. Thread-safe.
func (ws *wsConn) sendFrame(frame types.ServerFrame) {
	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()

	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("failed to marshal frame", "error", err)
		return
	}
	if err := ws.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		slog.Warn("failed to write frame", "error", err)
	}
}
