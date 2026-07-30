package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"issue-flow/internal/clock"
	"issue-flow/internal/domain"
	"issue-flow/internal/provider"
)

type claimRaceProvider struct {
	ready  domain.Issue
	winner domain.Issue
}

func (p claimRaceProvider) ListIssues(context.Context, provider.ListQuery) (provider.IssuePage, error) {
	return provider.IssuePage{Items: []domain.Issue{p.ready}}, nil
}

func (p claimRaceProvider) GetIssue(context.Context, string) (domain.Issue, error) {
	return p.ready, nil
}

func (p claimRaceProvider) UpdateIssue(context.Context, string, provider.IssueChange, provider.Precondition) (domain.Issue, error) {
	return p.winner, nil
}

func (claimRaceProvider) Capabilities(context.Context) provider.Capabilities {
	return provider.Capabilities{ReadIssues: true, WriteIssues: true}
}

func (claimRaceProvider) Check(context.Context) error {
	return nil
}

func TestClaimRejectsTokenWhenAnotherClaimWinsConvergence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	ready := domain.Issue{
		ID: "1", Number: "1", WorkflowState: domain.StateReady, Version: "1",
	}
	rivalToken := "rival-secret"
	winner := ready
	winner.WorkflowState = domain.StateClaimed
	winner.Lease = &domain.Lease{
		ID: "lease_rival", AgentID: "rival-agent",
		TokenHash: domain.HashLeaseToken(rivalToken),
		ClaimedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}
	service := New(
		claimRaceProvider{ready: ready, winner: winner},
		clock.Fixed{Time: now},
		time.Hour,
	)
	service.newToken = func() (string, error) {
		return "losing-secret", nil
	}

	result, err := service.Claim(context.Background(), "1", "losing-agent", "op_loser", false)
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	if result.LeaseToken != "" {
		t.Fatalf("losing claim returned lease token %q", result.LeaseToken)
	}
}
