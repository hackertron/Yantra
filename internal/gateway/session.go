package gateway

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hackertron/Yantra/internal/runtime"
	"github.com/hackertron/Yantra/internal/tool"
	"github.com/hackertron/Yantra/internal/types"
)

// ManagedSession pairs a session record with its dedicated runtime.
type ManagedSession struct {
	Record     *types.SessionRecord
	Runtime    *runtime.AgentRuntime
	CancelFunc context.CancelFunc // cancels current turn
	LastActive time.Time

	mu         sync.Mutex // serialises turns within this session
	turnActive bool
}

// SessionManager maps session IDs to managed sessions and enforces limits.
type SessionManager struct {
	server   *Server
	sessions map[string]*ManagedSession
	mu       sync.RWMutex
	turnSem  chan struct{} // buffered to MaxConcurrentTurns
}

func newSessionManager(server *Server) *SessionManager {
	cap := server.cfg.MaxConcurrentTurns
	if cap <= 0 {
		cap = 10
	}
	return &SessionManager{
		server:   server,
		sessions: make(map[string]*ManagedSession),
		turnSem:  make(chan struct{}, cap),
	}
}

// GetOrCreate returns a cached ManagedSession or creates a new one.
func (sm *SessionManager) GetOrCreate(ctx context.Context, sessionID string) (*ManagedSession, error) {
	// Fast path: already cached.
	sm.mu.RLock()
	ms, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()
	if ok {
		ms.LastActive = time.Now()
		return ms, nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double-check after acquiring write lock.
	if ms, ok := sm.sessions[sessionID]; ok {
		ms.LastActive = time.Now()
		return ms, nil
	}

	// Enforce max sessions.
	maxSessions := sm.server.cfg.MaxSessions
	if maxSessions <= 0 {
		maxSessions = 50
	}
	if len(sm.sessions) >= maxSessions {
		return nil, fmt.Errorf("max sessions (%d) reached", maxSessions)
	}

	// Look up or create the persistent session record.
	rec, err := sm.server.sessStore.Get(ctx, sessionID)
	if err != nil {
		// Not found — create a new record.
		rec, err = sm.server.sessStore.Create(ctx, "gateway-session")
		if err != nil {
			return nil, fmt.Errorf("creating session: %w", err)
		}
		sessionID = rec.ID
	}

	// Build a fresh runtime for this session. Provider and tool registry are
	// stateless, so they can be shared across sessions.
	rt := runtime.New(
		sm.server.provider,
		sm.server.toolReg,
		sm.server.fullCfg.Runtime,
		sm.server.workspaceDir,
	)
	if sm.server.memStore != nil {
		rt.SetMemory(sm.server.memStore, sessionID)
	}

	ms = &ManagedSession{
		Record:     rec,
		Runtime:    rt,
		LastActive: time.Now(),
	}
	sm.sessions[sessionID] = ms
	return ms, nil
}

// Get returns an existing session or nil.
func (sm *SessionManager) Get(sessionID string) *ManagedSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[sessionID]
}

// AcquireTurnSlot blocks until a global turn slot is available. Returns false
// if the context is cancelled before a slot opens.
func (sm *SessionManager) AcquireTurnSlot(ctx context.Context) bool {
	select {
	case sm.turnSem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// ReleaseTurnSlot frees a global turn slot.
func (sm *SessionManager) ReleaseTurnSlot() {
	<-sm.turnSem
}

// Shutdown cancels all active turns.
func (sm *SessionManager) Shutdown() {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	for _, ms := range sm.sessions {
		if ms.CancelFunc != nil {
			ms.CancelFunc()
		}
	}
}

// buildRuntime creates a fresh AgentRuntime with shared provider/tools.
func (sm *SessionManager) buildRuntime(sessionID string) *runtime.AgentRuntime {
	rt := runtime.New(
		sm.server.provider,
		sm.server.toolReg,
		sm.server.fullCfg.Runtime,
		sm.server.workspaceDir,
	)
	if sm.server.memStore != nil {
		rt.SetMemory(sm.server.memStore, sessionID)
	}
	return rt
}

// newSession creates a brand-new session via the session store and caches it.
func (sm *SessionManager) newSession(ctx context.Context, name string) (*ManagedSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	maxSessions := sm.server.cfg.MaxSessions
	if maxSessions <= 0 {
		maxSessions = 50
	}
	if len(sm.sessions) >= maxSessions {
		return nil, fmt.Errorf("max sessions (%d) reached", maxSessions)
	}

	if name == "" {
		name = "gateway-session"
	}
	rec, err := sm.server.sessStore.Create(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	rt := sm.buildRuntime(rec.ID)

	ms := &ManagedSession{
		Record:     rec,
		Runtime:    rt,
		LastActive: time.Now(),
	}
	sm.sessions[rec.ID] = ms
	return ms, nil
}

// newRegistryFrom creates a fresh ToolRegistry cloning the server's config.
func newRegistryFrom(policy *tool.WorkspacePolicy) *tool.ToolRegistry {
	return tool.NewRegistry(policy)
}
