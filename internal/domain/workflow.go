package domain

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidTransition = errors.New("invalid workflow state transition")

var transitions = map[WorkflowState]map[WorkflowState]bool{
	StateReady:   {StateClaimed: true},
	StateClaimed: {StateWorking: true, StateReady: true},
	StateWorking: {StateReview: true, StateBlocked: true, StateReady: true},
	StateBlocked: {StateReady: true, StateReview: true},
	StateReview:  {StateDone: true, StateWorking: true},
}

func ValidateTransition(from, to WorkflowState) error {
	if transitions[from][to] {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}

func IsWorkflowState(value WorkflowState) bool {
	switch value {
	case StateReady, StateClaimed, StateWorking, StateBlocked, StateReview, StateDone:
		return true
	default:
		return false
	}
}

type Lease struct {
	ClaimToken  string    `json:"claimToken"`
	AgentID     string    `json:"agentId"`
	ClaimedAt   time.Time `json:"claimedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	HeartbeatAt time.Time `json:"heartbeatAt"`
}

func (l Lease) ValidAt(now time.Time) bool {
	return l.ClaimToken != "" && l.AgentID != "" && now.Before(l.ExpiresAt)
}

func (l Lease) Authorizes(agentID, claimToken string, now time.Time) bool {
	return l.ValidAt(now) && l.AgentID == agentID && l.ClaimToken == claimToken
}
