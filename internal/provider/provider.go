package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"

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
	CommentIssues         bool   `json:"commentIssues"`
	AdoptIssues           bool   `json:"adoptIssues"`
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
	Type   string
	Labels []string
	// Priority is provider-specific and optional; empty means leave the
	// provider's default. For Gitee: low, medium, high, or critical, mapped
	// to its native priority field rather than a label.
	Priority string
}

type IssueCreator interface {
	CreateIssue(context.Context, CreateIssueInput) (domain.Issue, error)
}

// IssueCommenter adds a plain-text comment to an issue without requiring a
// held lease. It is a separate capability from IssueWriter's lease-gated
// workflow events: callers that only need to relay a message (not perform a
// state transition) should use this instead.
type IssueCommenter interface {
	AddComment(ctx context.Context, number, body string) (domain.Comment, error)
}

// IssueAdopter brings an issue that was created outside issue-flow (no
// workflow label at all) under management by attaching the ready label —
// the same starting point CreateIssue gives a brand-new issue. It must fail
// with ErrPreconditionFailed if the issue already carries a workflow label,
// so a caller can't silently clobber an issue someone else is already
// working. Unlike CreateIssue, this never touches the issue's native
// provider state.
type IssueAdopter interface {
	AdoptIssue(ctx context.Context, number string) (domain.Issue, error)
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
		existing.To == requested.To &&
		reflect.DeepEqual(existing.Delivery, requested.Delivery)
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
