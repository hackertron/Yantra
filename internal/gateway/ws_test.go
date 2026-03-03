package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hackertron/Yantra/internal/memory"
	"github.com/hackertron/Yantra/internal/tool"
	"github.com/hackertron/Yantra/internal/types"
)

// scriptedProvider returns pre-configured responses for WS tests.
type scriptedProvider struct {
	responses []*types.Response
	callIdx   int
}

func (p *scriptedProvider) ProviderID() types.ProviderID { return "test" }
func (p *scriptedProvider) ModelID() types.ModelID       { return "test-model" }
func (p *scriptedProvider) MaxContextTokens() int        { return 128000 }

func (p *scriptedProvider) Complete(ctx context.Context, c *types.Context) (*types.Response, error) {
	if p.callIdx >= len(p.responses) {
		return nil, errors.New("no more scripted responses")
	}
	resp := p.responses[p.callIdx]
	p.callIdx++
	return resp, nil
}

func (p *scriptedProvider) Stream(ctx context.Context, c *types.Context) <-chan types.StreamItem {
	ch := make(chan types.StreamItem, 128)
	if p.callIdx >= len(p.responses) {
		go func() {
			defer close(ch)
			ch <- types.StreamItem{Type: types.StreamError, Error: errors.New("no more scripted responses")}
		}()
		return ch
	}
	resp := p.responses[p.callIdx]
	p.callIdx++

	go func() {
		defer close(ch)
		for _, r := range resp.Message.Content {
			ch <- types.StreamItem{Type: types.StreamText, Text: string(r)}
		}
		ch <- types.StreamItem{Type: types.StreamDone, Usage: &resp.Usage}
	}()
	return ch
}

func newWSTestServer(t *testing.T, prov types.Provider) (*httptest.Server, *Server) {
	t.Helper()

	db, err := memory.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	sessStore := memory.NewSessionStore(db)
	reg := tool.NewRegistry(nil)
	fullCfg := types.DefaultConfig()

	gw := NewServer(fullCfg.Gateway, &fullCfg, prov, reg, nil, sessStore, ".", slog.Default())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", gw.handleWebSocket)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return ts, gw
}

func wsConnect(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func sendClientFrame(t *testing.T, conn *websocket.Conn, frame types.ClientFrame) {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readServerFrame(t *testing.T, conn *websocket.Conn) types.ServerFrame {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var frame types.ServerFrame
	if err := json.Unmarshal(msg, &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return frame
}

func TestWS_HelloWelcomeFlow(t *testing.T) {
	prov := &scriptedProvider{
		responses: []*types.Response{
			{Message: types.Message{Role: types.RoleAssistant, Content: "hi"}, Usage: types.Usage{TotalTokens: 1}},
		},
	}
	ts, _ := newWSTestServer(t, prov)
	conn := wsConnect(t, ts)

	sendClientFrame(t, conn, types.ClientFrame{Type: types.FrameHello})
	frame := readServerFrame(t, conn)

	if frame.Type != types.FrameWelcome {
		t.Errorf("expected welcome, got %s", frame.Type)
	}
	if frame.SessionID == "" {
		t.Error("expected session_id in welcome frame")
	}
}

func TestWS_AuthRejection(t *testing.T) {
	prov := &stubProvider{}
	db, err := memory.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	sessStore := memory.NewSessionStore(db)
	reg := tool.NewRegistry(nil)
	fullCfg := types.DefaultConfig()
	fullCfg.Gateway.APIKey = "secret"

	gw := NewServer(fullCfg.Gateway, &fullCfg, prov, reg, nil, sessStore, ".", slog.Default())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", gw.handleWebSocket)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	conn := wsConnect(t, ts)

	// Send hello with wrong key.
	sendClientFrame(t, conn, types.ClientFrame{Type: types.FrameHello, APIKey: "wrong"})
	frame := readServerFrame(t, conn)

	if frame.Type != types.FrameError {
		t.Errorf("expected error frame, got %s", frame.Type)
	}
	if !strings.Contains(frame.Error, "authentication failed") {
		t.Errorf("unexpected error: %s", frame.Error)
	}
}

func TestWS_SendTurnCompleteFlow(t *testing.T) {
	prov := &scriptedProvider{
		responses: []*types.Response{
			{Message: types.Message{Role: types.RoleAssistant, Content: "The answer is 4."}, Usage: types.Usage{TotalTokens: 10}},
		},
	}
	ts, _ := newWSTestServer(t, prov)
	conn := wsConnect(t, ts)

	// Hello
	sendClientFrame(t, conn, types.ClientFrame{Type: types.FrameHello})
	welcome := readServerFrame(t, conn)
	if welcome.Type != types.FrameWelcome {
		t.Fatalf("expected welcome, got %s", welcome.Type)
	}

	// Send message
	sendClientFrame(t, conn, types.ClientFrame{
		Type:    types.FrameSend,
		Content: "What is 2+2?",
	})

	// Read frames until turn_complete.
	var gotDeltas bool
	var turnComplete types.ServerFrame
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for turn_complete")
		default:
		}

		frame := readServerFrame(t, conn)
		switch frame.Type {
		case types.FrameTextDelta:
			gotDeltas = true
		case types.FrameTurnComplete:
			turnComplete = frame
			goto done
		case types.FrameError:
			t.Fatalf("unexpected error: %s", frame.Error)
		}
	}
done:
	if !gotDeltas {
		t.Error("expected at least one text_delta frame")
	}
	if turnComplete.Text != "The answer is 4." {
		t.Errorf("unexpected turn_complete text: %q", turnComplete.Text)
	}
}

func TestWS_SessionCommands(t *testing.T) {
	prov := &stubProvider{}
	ts, _ := newWSTestServer(t, prov)
	conn := wsConnect(t, ts)

	// Hello
	sendClientFrame(t, conn, types.ClientFrame{Type: types.FrameHello})
	welcome := readServerFrame(t, conn)
	if welcome.Type != types.FrameWelcome {
		t.Fatalf("expected welcome, got %s", welcome.Type)
	}
	firstSessionID := welcome.SessionID

	// Create new session
	sendClientFrame(t, conn, types.ClientFrame{
		Type:    types.FrameSessionCmd,
		Command: "new",
		Args:    "my-session",
	})
	created := readServerFrame(t, conn)
	if created.Type != types.FrameSessionCreated {
		t.Fatalf("expected session_created, got %s", created.Type)
	}
	if created.SessionID == "" || created.SessionID == firstSessionID {
		t.Error("expected new session with different ID")
	}

	// List sessions
	sendClientFrame(t, conn, types.ClientFrame{
		Type:    types.FrameSessionCmd,
		Command: "list",
	})
	list := readServerFrame(t, conn)
	if list.Type != types.FrameSessionList {
		t.Fatalf("expected session_list, got %s", list.Type)
	}
	if len(list.Sessions) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(list.Sessions))
	}

	// Switch back to first session
	sendClientFrame(t, conn, types.ClientFrame{
		Type:    types.FrameSessionCmd,
		Command: "switch",
		Args:    firstSessionID,
	})
	switched := readServerFrame(t, conn)
	if switched.Type != types.FrameSessionSwitched {
		t.Fatalf("expected session_switched, got %s", switched.Type)
	}
	if switched.SessionID != firstSessionID {
		t.Errorf("expected session_id=%s, got %s", firstSessionID, switched.SessionID)
	}
}

func TestWS_FirstFrameMustBeHello(t *testing.T) {
	prov := &stubProvider{}
	ts, _ := newWSTestServer(t, prov)
	conn := wsConnect(t, ts)

	// Send a non-hello frame first.
	sendClientFrame(t, conn, types.ClientFrame{Type: types.FrameSend, Content: "hi"})
	frame := readServerFrame(t, conn)

	if frame.Type != types.FrameError {
		t.Errorf("expected error, got %s", frame.Type)
	}
	if !strings.Contains(frame.Error, "first frame must be hello") {
		t.Errorf("unexpected error: %s", frame.Error)
	}
}
