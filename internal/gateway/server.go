package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/hackertron/Yantra/internal/tool"
	"github.com/hackertron/Yantra/internal/types"
)

// Server is the HTTP/WebSocket gateway that exposes the agent runtime.
type Server struct {
	cfg          types.GatewayConfig
	fullCfg      *types.YantraConfig
	provider     types.Provider
	toolReg      *tool.ToolRegistry
	memStore     types.MemoryRetrieval
	sessStore    types.SessionStore
	sessMgr      *SessionManager
	workspaceDir string
	logger       *slog.Logger
}

// NewServer creates a gateway server.
func NewServer(
	cfg types.GatewayConfig,
	fullCfg *types.YantraConfig,
	provider types.Provider,
	toolReg *tool.ToolRegistry,
	memStore types.MemoryRetrieval,
	sessStore types.SessionStore,
	workspaceDir string,
	logger *slog.Logger,
) *Server {
	s := &Server{
		cfg:          cfg,
		fullCfg:      fullCfg,
		provider:     provider,
		toolReg:      toolReg,
		memStore:     memStore,
		sessStore:    sessStore,
		workspaceDir: workspaceDir,
		logger:       logger,
	}
	s.sessMgr = newSessionManager(s)
	return s
}

// ListenAndServe starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()

	// Unauthenticated routes.
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /ready", s.handleReady)
	mux.HandleFunc("GET /ws", s.handleWebSocket)

	// Authenticated API routes.
	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/chat", s.handleChat)
	api.HandleFunc("POST /api/v1/chat/stream", s.handleChatStream)
	api.HandleFunc("GET /api/v1/sessions", s.handleListSessions)
	api.HandleFunc("POST /api/v1/sessions", s.handleCreateSession)
	api.HandleFunc("GET /api/v1/sessions/{id}", s.handleGetSession)
	mux.Handle("/api/", s.authMiddleware(api))

	addr := s.cfg.Listen
	if addr == "" {
		addr = "127.0.0.1:7700"
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Graceful shutdown goroutine.
	go func() {
		<-ctx.Done()
		s.logger.Info("shutting down gateway")
		s.sessMgr.Shutdown()
		srv.Shutdown(context.Background())
	}()

	s.logger.Info("gateway listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("gateway listen: %w", err)
	}
	return nil
}

// --- REST handlers ---

type chatRequest struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id,omitempty"`
}

type chatResponse struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	TurnsUsed int    `json:"turns_used"`
	Tokens    int    `json:"tokens"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}

	var ms *ManagedSession
	var err error
	if req.SessionID != "" {
		ms, err = s.sessMgr.GetOrCreate(r.Context(), req.SessionID)
	} else {
		ms, err = s.sessMgr.newSession(r.Context(), "api-session")
	}
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	result, err := executeTurnREST(r.Context(), s.sessMgr, ms, req.Message)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chatResponse{
		SessionID: ms.Record.ID,
		Content:   result.FinalContent,
		TurnsUsed: result.TurnsUsed,
		Tokens:    result.TotalUsage.TotalTokens,
	})
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}

	var ms *ManagedSession
	var err error
	if req.SessionID != "" {
		ms, err = s.sessMgr.GetOrCreate(r.Context(), req.SessionID)
	} else {
		ms, err = s.sessMgr.newSession(r.Context(), "api-stream-session")
	}
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	sse := NewSSEWriter(w)
	executeTurnSSE(r.Context(), s.sessMgr, ms, req.Message, sse)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.sessStore.List(r.Context(), false)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	ms, err := s.sessMgr.newSession(r.Context(), body.Name)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(ms.Record)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, err := s.sessStore.Get(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rec)
}

// SSEWriter writes Server-Sent Events.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter creates an SSE writer and sets appropriate headers.
func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, _ := w.(http.Flusher)

	return &SSEWriter{w: w, flusher: flusher}
}

// WriteEvent writes a named SSE event with JSON data.
func (s *SSEWriter) WriteEvent(event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}
