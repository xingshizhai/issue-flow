package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"issue-flow/internal/clock"
	"issue-flow/internal/domain"
	"issue-flow/internal/provider"
)

var (
	ErrStateConflict = errors.New("workflow state conflict")
	ErrLeaseConflict = errors.New("lease conflict")
	ErrLeaseExpired  = errors.New("lease expired")
	ErrInvalidInput  = errors.New("invalid input")
)

type Service struct {
	provider      provider.Provider
	clock         clock.Clock
	leaseDuration time.Duration
	newToken      func() (string, error)
}

type Result struct {
	Issue      domain.Issue `json:"issue"`
	LeaseToken string       `json:"leaseToken,omitempty"`
	DryRun     bool         `json:"dryRun"`
}

func New(p provider.Provider, c clock.Clock, leaseDuration time.Duration) *Service {
	return &Service{provider: p, clock: c, leaseDuration: leaseDuration, newToken: randomToken}
}

func (s *Service) Claim(ctx context.Context, number, agentID, operationID string, dryRun bool) (Result, error) {
	if err := validateAgentID(agentID); err != nil {
		return Result{}, err
	}
	current, err := s.provider.GetIssue(ctx, number)
	if err != nil {
		return Result{}, err
	}
	now := s.clock.Now()
	if current.WorkflowState != domain.StateReady {
		return Result{}, fmt.Errorf("%w: issue is %s", ErrStateConflict, current.WorkflowState)
	}
	if current.Lease != nil && current.Lease.ValidAt(now) {
		return Result{}, fmt.Errorf("%w: issue is held by %s", ErrLeaseConflict, current.Lease.AgentID)
	}
	token, err := s.newToken()
	if err != nil {
		return Result{}, fmt.Errorf("generate claim token: %w", err)
	}
	lease := domain.Lease{
		ID: "lease_" + domain.HashLeaseToken(token)[:16], TokenHash: domain.HashLeaseToken(token),
		AgentID: agentID, ClaimedAt: now,
		HeartbeatAt: now, ExpiresAt: now.Add(s.leaseDuration),
	}
	next := domain.StateClaimed
	change := provider.IssueChange{
		WorkflowState: &next,
		Lease:         &lease,
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: operationID, Operation: "claim",
			AgentID: agentID, LeaseID: lease.ID, From: current.WorkflowState,
			To: next, OccurredAt: now, ExpiresAt: lease.ExpiresAt,
		},
	}
	result, err := s.apply(ctx, current, change, provider.Precondition{
		Version: current.Version, WorkflowState: domain.StateReady,
	}, dryRun)
	result.LeaseToken = token
	return result, err
}

func (s *Service) Start(ctx context.Context, number, agentID, token, operationID string, dryRun bool) (Result, error) {
	return s.holderTransition(ctx, number, agentID, token, operationID, "start", domain.StateWorking, "", dryRun)
}

func (s *Service) Release(ctx context.Context, number, agentID, token, operationID, reason string, dryRun bool) (Result, error) {
	if strings.TrimSpace(reason) == "" {
		return Result{}, fmt.Errorf("%w: release reason is required", ErrInvalidInput)
	}
	return s.holderTransition(ctx, number, agentID, token, operationID, "release", domain.StateReady, reason, dryRun)
}

func (s *Service) Heartbeat(ctx context.Context, number, agentID, token, operationID string, dryRun bool) (Result, error) {
	current, now, err := s.authorizedIssue(ctx, number, agentID, token)
	if err != nil {
		return Result{}, err
	}
	lease := *current.Lease
	lease.HeartbeatAt = now
	lease.ExpiresAt = now.Add(s.leaseDuration)
	change := provider.IssueChange{
		Lease: &lease,
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: operationID, Operation: "heartbeat",
			AgentID: agentID, LeaseID: lease.ID, From: current.WorkflowState,
			To: current.WorkflowState, OccurredAt: now, ExpiresAt: lease.ExpiresAt,
		},
	}
	return s.apply(ctx, current, change, provider.Precondition{
		Version: current.Version, WorkflowState: current.WorkflowState, LeaseID: current.Lease.ID,
	}, dryRun)
}

func (s *Service) Reclaim(ctx context.Context, number, operationID string, dryRun bool) (Result, error) {
	current, err := s.provider.GetIssue(ctx, number)
	if err != nil {
		return Result{}, err
	}
	now := s.clock.Now()
	if current.Lease == nil {
		return Result{}, fmt.Errorf("%w: issue has no lease", ErrLeaseConflict)
	}
	if current.Lease.ValidAt(now) {
		return Result{}, fmt.Errorf("%w: lease held by %s until %s", ErrLeaseConflict, current.Lease.AgentID, current.Lease.ExpiresAt.Format(time.RFC3339))
	}
	switch current.WorkflowState {
	case domain.StateClaimed, domain.StateWorking, domain.StateBlocked:
	default:
		return Result{}, fmt.Errorf("%w: issue is %s", ErrStateConflict, current.WorkflowState)
	}
	next := domain.StateReady
	change := provider.IssueChange{
		WorkflowState: &next, ClearLease: true,
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: operationID, Operation: "reclaim",
			From: current.WorkflowState, To: next, OccurredAt: now,
		},
	}
	return s.apply(ctx, current, change, provider.Precondition{
		Version: current.Version, WorkflowState: current.WorkflowState,
		LeaseID: current.Lease.ID,
	}, dryRun)
}

func (s *Service) holderTransition(
	ctx context.Context, number, agentID, token, operationID, operation string,
	next domain.WorkflowState, message string, dryRun bool,
) (Result, error) {
	current, now, err := s.authorizedIssue(ctx, number, agentID, token)
	if err != nil {
		return Result{}, err
	}
	if err := domain.ValidateTransition(current.WorkflowState, next); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrStateConflict, err)
	}
	change := provider.IssueChange{
		WorkflowState: &next,
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: operationID, Operation: operation,
			AgentID: agentID, LeaseID: current.Lease.ID, Message: message,
			From: current.WorkflowState, To: next, OccurredAt: now,
			ExpiresAt: current.Lease.ExpiresAt,
		},
	}
	if next == domain.StateReady {
		change.ClearLease = true
	}
	return s.apply(ctx, current, change, provider.Precondition{
		Version: current.Version, WorkflowState: current.WorkflowState, LeaseID: current.Lease.ID,
	}, dryRun)
}

func (s *Service) authorizedIssue(ctx context.Context, number, agentID, token string) (domain.Issue, time.Time, error) {
	if err := validateAgentID(agentID); err != nil {
		return domain.Issue{}, time.Time{}, err
	}
	if token == "" {
		return domain.Issue{}, time.Time{}, fmt.Errorf("%w: lease token is required", ErrInvalidInput)
	}
	current, err := s.provider.GetIssue(ctx, number)
	if err != nil {
		return domain.Issue{}, time.Time{}, err
	}
	now := s.clock.Now()
	if current.Lease == nil || !current.Lease.Authorizes(agentID, token, now) {
		return domain.Issue{}, time.Time{}, fmt.Errorf("%w: caller does not hold this lease", ErrLeaseConflict)
	}
	if !current.Lease.ValidAt(now) {
		return domain.Issue{}, time.Time{}, ErrLeaseExpired
	}
	return current, now, nil
}

func (s *Service) apply(ctx context.Context, current domain.Issue, change provider.IssueChange, precondition provider.Precondition, dryRun bool) (Result, error) {
	if dryRun {
		if change.WorkflowState != nil {
			current.WorkflowState = *change.WorkflowState
		}
		if change.ClearLease {
			current.Lease = nil
		} else if change.Lease != nil {
			lease := *change.Lease
			current.Lease = &lease
		}
		current.Events = append(current.Events, change.Event)
		return Result{Issue: current, DryRun: true}, nil
	}
	updated, err := s.provider.UpdateIssue(ctx, current.Number, change, precondition)
	if errors.Is(err, provider.ErrPreconditionFailed) {
		return Result{}, fmt.Errorf("%w: issue changed concurrently", ErrLeaseConflict)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Issue: updated}, nil
}

func validateAgentID(agentID string) error {
	if len(agentID) < 1 || len(agentID) > 64 {
		return fmt.Errorf("%w: agent ID must contain 1 to 64 characters", ErrInvalidInput)
	}
	for _, r := range agentID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("%w: agent ID may only contain letters, digits, dot, dash, and underscore", ErrInvalidInput)
	}
	return nil
}

func randomToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
