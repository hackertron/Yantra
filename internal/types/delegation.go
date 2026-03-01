package types

import "context"

// DelegationRequest describes a task to hand off to a specialist agent.
type DelegationRequest struct {
	AgentName string `json:"agent_name"`
	Goal      string `json:"goal"`
	SessionID string `json:"session_id"`
}

// DelegationResult is returned by a completed delegation.
type DelegationResult struct {
	AgentName string  `json:"agent_name"`
	Output    string  `json:"output"`
	TurnsUsed int     `json:"turns_used"`
	CostUsed  float64 `json:"cost_used"`
	Status    string  `json:"status"` // "completed", "cancelled", "failed"
}

// DelegationExecutor handles multi-agent delegation.
type DelegationExecutor interface {
	Delegate(ctx context.Context, req DelegationRequest) (*DelegationResult, error)
}
