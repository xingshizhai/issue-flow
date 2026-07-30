package provider

import (
	"context"
	"errors"

	"issue-flow/internal/domain"
)

var ErrNotFound = errors.New("issue not found")

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

type Provider interface {
	IssueReader
	Capabilities(context.Context) Capabilities
}
