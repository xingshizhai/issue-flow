package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xingshizhai/issue-flow/internal/clock"
	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/provider"
	"github.com/xingshizhai/issue-flow/internal/redact"
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
	redactor      redact.Redactor
}

type Result struct {
	Issue      domain.Issue `json:"issue"`
	LeaseToken string       `json:"leaseToken,omitempty"`
	DryRun     bool         `json:"dryRun"`
}

func New(p provider.Provider, c clock.Clock, leaseDuration time.Duration) *Service {
	return NewWithRedactKeys(p, c, leaseDuration, nil)
}

func NewWithRedactKeys(p provider.Provider, c clock.Clock, leaseDuration time.Duration, keys []string) *Service {
	return &Service{
		provider: p, clock: c, leaseDuration: leaseDuration,
		newToken: randomToken, redactor: redact.New(keys),
	}
}

func (s *Service) Claim(ctx context.Context, number, agentID, operationID string, dryRun bool) (Result, error) {
	if err := validateAgentID(agentID); err != nil {
		return Result{}, err
	}
	current, err := s.provider.GetIssue(ctx, number)
	if err != nil {
		return Result{}, err
	}
	if event, found := findOperation(current, operationID); found {
		if event.Operation != "claim" || event.AgentID != agentID {
			return Result{}, fmt.Errorf("%w: operation ID is already used", ErrInvalidInput)
		}
		return Result{}, fmt.Errorf("%w: claim already applied; its one-time token cannot be returned again", ErrLeaseConflict)
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
	if err != nil {
		return result, err
	}
	if result.Issue.Lease == nil ||
		result.Issue.Lease.ID != lease.ID ||
		!result.Issue.Lease.Authenticates(agentID, token) {
		return Result{}, fmt.Errorf("%w: another claim won ownership", ErrLeaseConflict)
	}
	result.LeaseToken = token
	return result, nil
}

func (s *Service) Start(ctx context.Context, number, agentID, token, operationID string, dryRun bool) (Result, error) {
	return s.holderTransition(ctx, number, agentID, token, operationID, "start", domain.StateWorking, "", false, dryRun)
}

func (s *Service) Release(ctx context.Context, number, agentID, token, operationID, reason string, dryRun bool) (Result, error) {
	if strings.TrimSpace(reason) == "" {
		return Result{}, fmt.Errorf("%w: release reason is required", ErrInvalidInput)
	}
	return s.holderTransition(ctx, number, agentID, token, operationID, "release", domain.StateReady, reason, true, dryRun)
}

func (s *Service) Progress(ctx context.Context, number, agentID, token, operationID, message string, dryRun bool) (Result, error) {
	if strings.TrimSpace(message) == "" {
		return Result{}, fmt.Errorf("%w: progress message is required", ErrInvalidInput)
	}
	current, err := s.provider.GetIssue(ctx, number)
	if err != nil {
		return Result{}, err
	}
	if result, found, err := replayOperation(current, operationID, "progress", agentID); found || err != nil {
		return result, err
	}
	now, err := s.authorizeLoaded(current, agentID, token)
	if err != nil {
		return Result{}, err
	}
	switch current.WorkflowState {
	case domain.StateClaimed, domain.StateWorking, domain.StateBlocked:
	default:
		return Result{}, fmt.Errorf("%w: progress is not allowed from %s", ErrStateConflict, current.WorkflowState)
	}
	change := provider.IssueChange{
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: operationID, Operation: "progress",
			AgentID: agentID, LeaseID: current.Lease.ID, Message: s.redactor.String(message),
			From: current.WorkflowState, To: current.WorkflowState, OccurredAt: now,
			ExpiresAt: current.Lease.ExpiresAt,
		},
	}
	return s.apply(ctx, current, change, provider.Precondition{
		Version: current.Version, WorkflowState: current.WorkflowState, LeaseID: current.Lease.ID,
	}, dryRun)
}

func (s *Service) Block(ctx context.Context, number, agentID, token, operationID, reason string, dryRun bool) (Result, error) {
	if strings.TrimSpace(reason) == "" {
		return Result{}, fmt.Errorf("%w: block reason is required", ErrInvalidInput)
	}
	return s.holderTransition(ctx, number, agentID, token, operationID, "block", domain.StateBlocked, reason, false, dryRun)
}

func (s *Service) Finish(ctx context.Context, number, agentID, token, operationID, summary string, dryRun bool) (Result, error) {
	if strings.TrimSpace(summary) == "" {
		return Result{}, fmt.Errorf("%w: finish summary is required", ErrInvalidInput)
	}
	return s.holderTransition(ctx, number, agentID, token, operationID, "finish", domain.StateDone, summary, true, dryRun)
}

func (s *Service) Complete(ctx context.Context, number, reviewerID, operationID, conclusion string, dryRun bool) (Result, error) {
	if err := validateAgentID(reviewerID); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(conclusion) == "" {
		return Result{}, fmt.Errorf("%w: review conclusion is required", ErrInvalidInput)
	}
	current, err := s.provider.GetIssue(ctx, number)
	if err != nil {
		return Result{}, err
	}
	if event, found := findOperation(current, operationID); found {
		if event.Operation != "complete" || event.AgentID != reviewerID {
			return Result{}, fmt.Errorf("%w: operation ID is already used", ErrInvalidInput)
		}
		if dryRun {
			return Result{Issue: current, DryRun: true}, nil
		}
		next := domain.StateDone
		return s.apply(ctx, current, provider.IssueChange{
			WorkflowState: &next, Event: event,
		}, provider.Precondition{
			Version: current.Version, WorkflowState: current.WorkflowState,
		}, false)
	}
	if err := domain.ValidateTransition(current.WorkflowState, domain.StateDone); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrStateConflict, err)
	}
	next := domain.StateDone
	change := provider.IssueChange{
		WorkflowState: &next,
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: operationID, Operation: "complete",
			AgentID: reviewerID, Message: s.redactor.String(conclusion),
			From: current.WorkflowState, To: next, OccurredAt: s.clock.Now(),
		},
	}
	return s.apply(ctx, current, change, provider.Precondition{
		Version: current.Version, WorkflowState: domain.StateReview,
	}, dryRun)
}

func (s *Service) Heartbeat(ctx context.Context, number, agentID, token, operationID string, dryRun bool) (Result, error) {
	current, err := s.provider.GetIssue(ctx, number)
	if err != nil {
		return Result{}, err
	}
	if result, found, err := replayOperation(current, operationID, "heartbeat", agentID); found || err != nil {
		return result, err
	}
	now, err := s.authorizeLoaded(current, agentID, token)
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
	if result, found, err := replayOperation(current, operationID, "reclaim", ""); found || err != nil {
		return result, err
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
	next domain.WorkflowState, message string, clearLease, dryRun bool,
) (Result, error) {
	current, err := s.provider.GetIssue(ctx, number)
	if err != nil {
		return Result{}, err
	}
	if result, found, err := replayOperation(current, operationID, operation, agentID); found || err != nil {
		return result, err
	}
	now, err := s.authorizeLoaded(current, agentID, token)
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
			AgentID: agentID, LeaseID: current.Lease.ID, Message: s.redactor.String(message),
			From: current.WorkflowState, To: next, OccurredAt: now,
			ExpiresAt: current.Lease.ExpiresAt,
		},
	}
	if clearLease {
		change.ClearLease = true
	}
	return s.apply(ctx, current, change, provider.Precondition{
		Version: current.Version, WorkflowState: current.WorkflowState, LeaseID: current.Lease.ID,
	}, dryRun)
}

func (s *Service) authorizeLoaded(current domain.Issue, agentID, token string) (time.Time, error) {
	if err := validateAgentID(agentID); err != nil {
		return time.Time{}, err
	}
	if token == "" {
		return time.Time{}, fmt.Errorf("%w: lease token is required", ErrInvalidInput)
	}
	now := s.clock.Now()
	if current.Lease == nil || !current.Lease.Authenticates(agentID, token) {
		return time.Time{}, fmt.Errorf("%w: caller does not hold this lease", ErrLeaseConflict)
	}
	if !current.Lease.ValidAt(now) {
		return time.Time{}, ErrLeaseExpired
	}
	return now, nil
}

func replayOperation(issue domain.Issue, operationID, operation, agentID string) (Result, bool, error) {
	event, found := findOperation(issue, operationID)
	if !found {
		return Result{}, false, nil
	}
	if event.Operation != operation || event.AgentID != agentID {
		return Result{}, true, fmt.Errorf("%w: operation ID is already used", ErrInvalidInput)
	}
	return Result{Issue: issue}, true, nil
}

func findOperation(issue domain.Issue, operationID string) (domain.WorkflowEvent, bool) {
	for _, event := range issue.Events {
		if event.OperationID == operationID {
			return event, true
		}
	}
	return domain.WorkflowEvent{}, false
}

func (s *Service) apply(ctx context.Context, current domain.Issue, change provider.IssueChange, precondition provider.Precondition, dryRun bool) (Result, error) {
	if !validOperationID(change.Event.OperationID) {
		return Result{}, fmt.Errorf("%w: operation ID must start with op_ and contain 1 to 64 safe characters", ErrInvalidInput)
	}
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

func validOperationID(value string) bool {
	if !strings.HasPrefix(value, "op_") || len(value) < 4 || len(value) > 67 {
		return false
	}
	for _, r := range value[3:] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
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
