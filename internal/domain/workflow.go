package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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
	ID          string    `json:"id"`
	TokenHash   string    `json:"tokenHash,omitempty"`
	AgentID     string    `json:"agentId"`
	ClaimedAt   time.Time `json:"claimedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	HeartbeatAt time.Time `json:"heartbeatAt"`
}

func (l Lease) ValidAt(now time.Time) bool {
	return l.ID != "" && l.TokenHash != "" && l.AgentID != "" && now.Before(l.ExpiresAt)
}

func (l Lease) Authorizes(agentID, leaseToken string, now time.Time) bool {
	return l.Authenticates(agentID, leaseToken) && l.ValidAt(now)
}

func (l Lease) Authenticates(agentID, leaseToken string) bool {
	actual, err := hex.DecodeString(l.TokenHash)
	if err != nil {
		return false
	}
	expected := sha256.Sum256([]byte(leaseToken))
	return l.AgentID == agentID &&
		subtle.ConstantTimeCompare(actual, expected[:]) == 1
}

func HashLeaseToken(leaseToken string) string {
	sum := sha256.Sum256([]byte(leaseToken))
	return hex.EncodeToString(sum[:])
}
