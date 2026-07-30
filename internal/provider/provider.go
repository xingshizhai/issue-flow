package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/xingshizhai/issue-flow/internal/domain"
)

var (
	ErrNotFound           = errors.New("issue not found")
	ErrPreconditionFailed = errors.New("provider precondition failed")
	ErrAuthentication     = errors.New("provider authentication failed")
	ErrPermission         = errors.New("provider permission denied")
	ErrRateLimited        = errors.New("provider rate limited")
	ErrUnavailable        = errors.New("provider unavailable")
	ErrUnsupported        = errors.New("provider capability unsupported")
	ErrMisconfigured      = errors.New("provider configuration invalid")
)

type ListQuery struct {
	State  domain.WorkflowState
	Limit  int
	Cursor string
}

type IssuePage struct {
	Items      []domain.Issue `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type Capabilities struct {
	ReadIssues            bool   `json:"readIssues"`
	WriteIssues           bool   `json:"writeIssues"`
	StrongClaimCAS        bool   `json:"strongClaimCas"`
	IdempotencyKeys       bool   `json:"idempotencyKeys"`
	AccessTransport       string `json:"accessTransport"`
	CredentialMode        string `json:"credentialMode"`
	RefreshableCredential bool   `json:"refreshableCredential"`
}

type IssueReader interface {
	ListIssues(context.Context, ListQuery) (IssuePage, error)
	GetIssue(context.Context, string) (domain.Issue, error)
}

type CreateIssueInput struct {
	Title  string
	Body   string
	Labels []string
}

type IssueCreator interface {
	CreateIssue(context.Context, CreateIssueInput) (domain.Issue, error)
}

type Precondition struct {
	Version       string
	WorkflowState domain.WorkflowState
	LeaseID       string
}

type IssueChange struct {
	WorkflowState *domain.WorkflowState
	Lease         *domain.Lease
	ClearLease    bool
	Event         domain.WorkflowEvent
}

func SameOperation(existing, requested domain.WorkflowEvent) bool {
	return existing.OperationID == requested.OperationID &&
		existing.Operation == requested.Operation &&
		existing.AgentID == requested.AgentID &&
		existing.LeaseID == requested.LeaseID &&
		existing.Message == requested.Message &&
		existing.From == requested.From &&
		existing.To == requested.To
}

func ValidateChange(change IssueChange) error {
	if change.Event.Version != 1 ||
		change.Event.OperationID == "" ||
		change.Event.Operation == "" {
		return fmt.Errorf("%w: event version, operation ID, and operation are required",
			ErrPreconditionFailed)
	}
	if change.WorkflowState != nil && change.Event.To != *change.WorkflowState {
		return fmt.Errorf("%w: event target %s does not match workflow target %s",
			ErrPreconditionFailed, change.Event.To, *change.WorkflowState)
	}
	if change.Lease != nil {
		if change.ClearLease {
			return fmt.Errorf("%w: change cannot set and clear a lease",
				ErrPreconditionFailed)
		}
		if change.Lease.ID == "" || change.Event.LeaseID != change.Lease.ID {
			return fmt.Errorf("%w: event lease does not match change lease",
				ErrPreconditionFailed)
		}
	}
	return nil
}

type IssueWriter interface {
	UpdateIssue(context.Context, string, IssueChange, Precondition) (domain.Issue, error)
}

type Provider interface {
	IssueReader
	IssueWriter
	Capabilities(context.Context) Capabilities
	Check(context.Context) error
}
