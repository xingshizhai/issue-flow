package domain

import (
	"errors"
	"testing"
	"time"
)

func TestValidateTransition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		from    WorkflowState
		to      WorkflowState
		wantErr bool
	}{
		{"claim", StateReady, StateClaimed, false},
		{"start", StateClaimed, StateWorking, false},
		{"finish", StateWorking, StateDone, false},
		{"optional review", StateWorking, StateReview, false},
		{"reject review", StateReview, StateWorking, false},
		{"skip claim", StateReady, StateWorking, true},
		{"blocked finish", StateBlocked, StateDone, false},
		{"reopen", StateDone, StateReady, false},
		{"done is otherwise terminal", StateDone, StateWorking, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateTransition(tt.from, tt.to)
			if tt.wantErr != errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("ValidateTransition() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestLeaseAuthorizationRequiresSecretToken(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	lease := Lease{
		ID: "lease_test", AgentID: "codex-a", TokenHash: HashLeaseToken("secret-token"),
		ClaimedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if !lease.Authorizes("codex-a", "secret-token", now) {
		t.Fatal("holder with token should be authorized")
	}
	if lease.Authorizes("codex-a", "wrong-token", now) {
		t.Fatal("agent ID alone must not authorize")
	}
	if lease.Authorizes("codex-a", "secret-token", lease.ExpiresAt) {
		t.Fatal("expired lease must not authorize")
	}
	if !lease.Authenticates("codex-a", "secret-token") {
		t.Fatal("expiration must not erase token identity")
	}
}
