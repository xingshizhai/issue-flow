package provider

import (
	"context"
	"errors"

	"issue-flow/internal/domain"
)

var (
	ErrNotFound           = errors.New("issue not found")
	ErrPreconditionFailed = errors.New("provider precondition failed")
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
	ReadIssues      bool `json:"readIssues"`
	WriteIssues     bool `json:"writeIssues"`
	StrongClaimCAS  bool `json:"strongClaimCas"`
	IdempotencyKeys bool `json:"idempotencyKeys"`
}

type IssueReader interface {
	ListIssues(context.Context, ListQuery) (IssuePage, error)
	GetIssue(context.Context, int) (domain.Issue, error)
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

type IssueWriter interface {
	UpdateIssue(context.Context, int, IssueChange, Precondition) (domain.Issue, error)
}

type Provider interface {
	IssueReader
	IssueWriter
	Capabilities(context.Context) Capabilities
}
