package gateway

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hackertron/Yantra/internal/runtime"
	"github.com/hackertron/Yantra/internal/types"
)

const defaultSystemPrompt = "You are a helpful AI assistant with access to tools."

// executeTurnWS runs a full agent turn for a WebSocket connection.
// It installs a StreamCallback to forward text_delta frames in real time.
func (ws *wsConn) executeTurnWS(ctx context.Context, ms *ManagedSession, userMessage string) {
	// Serialise turns within this session.
	ms.mu.Lock()
	if ms.turnActive {
		ms.mu.Unlock()
		ws.sendFrame(types.ServerFrame{
			Type:  types.FrameError,
			Error: "a turn is already active on this session",
		})
		return
	}
	ms.turnActive = true
	ms.mu.Unlock()

	defer func() {
		ms.mu.Lock()
		ms.turnActive = false
		ms.mu.Unlock()
	}()

	// Global concurrency gate.
	if !ws.server.sessMgr.AcquireTurnSlot(ctx) {
		ws.sendFrame(types.ServerFrame{
			Type:  types.FrameError,
			Error: "server at capacity, try again later",
		})
		return
	}
	defer ws.server.sessMgr.ReleaseTurnSlot()

	// Cancellable context for this turn.
	turnCtx, cancel := context.WithCancel(ctx)
	ms.CancelFunc = cancel
	defer func() {
		cancel()
		ms.CancelFunc = nil
	}()

	// Install stream callback to forward text deltas.
	ms.Runtime.SetStreamCallback(func(item types.StreamItem) {
		if item.Type == types.StreamText && item.Text != "" {
			ws.sendFrame(types.ServerFrame{
				Type:      types.FrameTextDelta,
				SessionID: ms.Record.ID,
				Text:      item.Text,
			})
		}
	})
	defer ms.Runtime.SetStreamCallback(nil)

	// Progress channel for tool events.
	progress := make(chan types.ProgressEvent, 32)
	go func() {
		for ev := range progress {
			if ev.Kind == types.ProgressToolExecution {
				ws.sendFrame(types.ServerFrame{
					Type:      types.FrameToolProgress,
					SessionID: ms.Record.ID,
					Tool:      ev.Tool,
					Status:    ev.Message,
				})
			}
		}
	}()

	result, err := ms.Runtime.Run(turnCtx, defaultSystemPrompt, userMessage, progress)
	close(progress)

	if err != nil {
		ws.sendFrame(types.ServerFrame{
			Type:      types.FrameError,
			SessionID: ms.Record.ID,
			Error:     err.Error(),
		})
		return
	}

	ws.sendFrame(types.ServerFrame{
		Type:      types.FrameTurnComplete,
		SessionID: ms.Record.ID,
		Text:      result.FinalContent,
		Status:    fmt.Sprintf("turns=%d tokens=%d", result.TurnsUsed, result.TotalUsage.TotalTokens),
	})
}

// executeTurnREST runs a full agent turn synchronously and returns the result.
func executeTurnREST(ctx context.Context, sm *SessionManager, ms *ManagedSession, userMessage string) (*runtime.RunResult, error) {
	ms.mu.Lock()
	if ms.turnActive {
		ms.mu.Unlock()
		return nil, fmt.Errorf("a turn is already active on this session")
	}
	ms.turnActive = true
	ms.mu.Unlock()

	defer func() {
		ms.mu.Lock()
		ms.turnActive = false
		ms.mu.Unlock()
	}()

	if !sm.AcquireTurnSlot(ctx) {
		return nil, fmt.Errorf("server at capacity, try again later")
	}
	defer sm.ReleaseTurnSlot()

	turnCtx, cancel := context.WithCancel(ctx)
	ms.CancelFunc = cancel
	defer func() {
		cancel()
		ms.CancelFunc = nil
	}()

	progress := make(chan types.ProgressEvent, 32)
	go func() {
		for range progress {
			// Drain — REST clients don't receive progress.
		}
	}()

	result, err := ms.Runtime.Run(turnCtx, defaultSystemPrompt, userMessage, progress)
	close(progress)
	return result, err
}

// executeTurnSSE runs a turn and streams events as SSE to the writer.
func executeTurnSSE(ctx context.Context, sm *SessionManager, ms *ManagedSession, userMessage string, sse *SSEWriter) error {
	ms.mu.Lock()
	if ms.turnActive {
		ms.mu.Unlock()
		return fmt.Errorf("a turn is already active on this session")
	}
	ms.turnActive = true
	ms.mu.Unlock()

	defer func() {
		ms.mu.Lock()
		ms.turnActive = false
		ms.mu.Unlock()
	}()

	if !sm.AcquireTurnSlot(ctx) {
		return fmt.Errorf("server at capacity, try again later")
	}
	defer sm.ReleaseTurnSlot()

	turnCtx, cancel := context.WithCancel(ctx)
	ms.CancelFunc = cancel
	defer func() {
		cancel()
		ms.CancelFunc = nil
	}()

	// Stream callback writes SSE text_delta events.
	ms.Runtime.SetStreamCallback(func(item types.StreamItem) {
		if item.Type == types.StreamText && item.Text != "" {
			sse.WriteEvent("text_delta", types.ServerFrame{
				Type:      types.FrameTextDelta,
				SessionID: ms.Record.ID,
				Text:      item.Text,
			})
		}
	})
	defer ms.Runtime.SetStreamCallback(nil)

	progress := make(chan types.ProgressEvent, 32)
	go func() {
		for ev := range progress {
			if ev.Kind == types.ProgressToolExecution {
				sse.WriteEvent("tool_progress", types.ServerFrame{
					Type:      types.FrameToolProgress,
					SessionID: ms.Record.ID,
					Tool:      ev.Tool,
					Status:    ev.Message,
				})
			}
		}
	}()

	result, err := ms.Runtime.Run(turnCtx, defaultSystemPrompt, userMessage, progress)
	close(progress)

	if err != nil {
		slog.Warn("SSE turn error", "session", ms.Record.ID, "error", err)
		sse.WriteEvent("error", types.ServerFrame{
			Type:  types.FrameError,
			Error: err.Error(),
		})
		return err
	}

	sse.WriteEvent("turn_complete", types.ServerFrame{
		Type:      types.FrameTurnComplete,
		SessionID: ms.Record.ID,
		Text:      result.FinalContent,
		Status:    fmt.Sprintf("turns=%d tokens=%d", result.TurnsUsed, result.TotalUsage.TotalTokens),
	})
	return nil
}
