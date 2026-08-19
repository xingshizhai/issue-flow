package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xingshizhai/issue-flow/internal/clock"
	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/provider"
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

func TestCompletedOperationCanBeReplayedWithoutAnotherWrite(t *testing.T) {
	t.Parallel()

	completed := domain.Issue{
		ID: "1", Number: "1", WorkflowState: domain.StateWorking,
		Events: []domain.WorkflowEvent{{
			Version: 1, OperationID: "op_start_retry", Operation: "start",
			AgentID: "agent-a", From: domain.StateClaimed, To: domain.StateWorking,
		}},
	}
	service := New(
		claimRaceProvider{ready: completed, winner: domain.Issue{}},
		clock.Fixed{Time: time.Now().UTC()},
		time.Hour,
	)
	result, err := service.Start(
		context.Background(), "1", "agent-a", "no-longer-needed",
		"op_start_retry", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Issue.WorkflowState != domain.StateWorking {
		t.Fatalf("result = %+v", result)
	}
}

func TestOperationIDCollisionIsRejected(t *testing.T) {
	t.Parallel()

	current := domain.Issue{
		ID: "1", Number: "1", WorkflowState: domain.StateWorking,
		Events: []domain.WorkflowEvent{{
			Version: 1, OperationID: "op_collision", Operation: "start",
			AgentID: "agent-a",
		}},
	}
	service := New(
		claimRaceProvider{ready: current},
		clock.Fixed{Time: time.Now().UTC()},
		time.Hour,
	)
	_, err := service.Heartbeat(
		context.Background(), "1", "agent-a", "unused",
		"op_collision", false,
	)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v", err)
	}
}

func TestAppliedClaimCannotReturnOneTimeTokenAgain(t *testing.T) {
	t.Parallel()

	current := domain.Issue{
		ID: "1", Number: "1", WorkflowState: domain.StateClaimed,
		Events: []domain.WorkflowEvent{{
			Version: 1, OperationID: "op_claim_retry", Operation: "claim",
			AgentID: "agent-a",
		}},
	}
	service := New(
		claimRaceProvider{ready: current},
		clock.Fixed{Time: time.Now().UTC()},
		time.Hour,
	)
	result, err := service.Claim(
		context.Background(), "1", "agent-a", "op_claim_retry", false,
	)
	if !errors.Is(err, ErrLeaseConflict) || result.LeaseToken != "" {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func TestReopenMovesDoneIssueBackToReady(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	current := domain.Issue{
		ID: "1", Number: "1", WorkflowState: domain.StateDone, Version: "1",
	}
	winner := current
	winner.WorkflowState = domain.StateReady
	service := New(claimRaceProvider{ready: current, winner: winner}, clock.Fixed{Time: now}, time.Hour)
	result, err := service.Reopen(context.Background(), "1", "maintainer-a", "op_reopen_1", "same report resurfaced", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Issue.WorkflowState != domain.StateReady {
		t.Fatalf("result = %+v", result)
	}
}

func TestReopenRejectsIssueNotDone(t *testing.T) {
	t.Parallel()
	current := domain.Issue{
		ID: "1", Number: "1", WorkflowState: domain.StateWorking, Version: "1",
	}
	service := New(claimRaceProvider{ready: current}, clock.Fixed{Time: time.Now().UTC()}, time.Hour)
	_, err := service.Reopen(context.Background(), "1", "maintainer-a", "op_reopen_working", "should not apply", false)
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("error = %v", err)
	}
}

func TestReopenRequiresReason(t *testing.T) {
	t.Parallel()
	current := domain.Issue{ID: "1", Number: "1", WorkflowState: domain.StateDone, Version: "1"}
	service := New(claimRaceProvider{ready: current}, clock.Fixed{Time: time.Now().UTC()}, time.Hour)
	_, err := service.Reopen(context.Background(), "1", "maintainer-a", "op_reopen_no_reason", "", false)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v", err)
	}
}

func TestReopenedOperationCanBeReplayedWithoutAnotherWrite(t *testing.T) {
	t.Parallel()
	current := domain.Issue{
		ID: "1", Number: "1", WorkflowState: domain.StateReady,
		Events: []domain.WorkflowEvent{{
			Version: 1, OperationID: "op_reopen_retry", Operation: "reopen",
			AgentID: "maintainer-a", From: domain.StateDone, To: domain.StateReady,
		}},
	}
	service := New(claimRaceProvider{ready: current, winner: current}, clock.Fixed{Time: time.Now().UTC()}, time.Hour)
	result, err := service.Reopen(context.Background(), "1", "maintainer-a", "op_reopen_retry", "same report resurfaced", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Issue.WorkflowState != domain.StateReady {
		t.Fatalf("result = %+v", result)
	}
}

func TestFinishStateCanBeConfiguredToDone(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	token := "holder-token"
	current := domain.Issue{
		ID: "1", Number: "1", WorkflowState: domain.StateWorking, Version: "1",
		Lease: &domain.Lease{ID: "lease_1", AgentID: "agent-a", TokenHash: domain.HashLeaseToken(token), ClaimedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	winner := current
	winner.WorkflowState = domain.StateDone
	winner.Lease = nil
	service := New(claimRaceProvider{ready: current, winner: winner}, clock.Fixed{Time: now}, time.Hour).WithFinishState(domain.StateDone)
	result, err := service.Finish(context.Background(), "1", "agent-a", token, "op_finish_done", "delivered", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Issue.WorkflowState != domain.StateDone {
		t.Fatalf("result = %+v", result)
	}
}
