package gitee

import (
	"context"
	"net/http"
	"net/url"

	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
)

// NativeStateSyncer updates provider-side issue state after workflow transitions.
// Open API syncs open|progressing|closed|rejected; enterprise syncs Kanban titles.
type NativeStateSyncer interface {
	Sync(ctx context.Context, number string, state domain.WorkflowState) error
	Check(ctx context.Context) error
}

type openAPINativeState struct {
	client   Transport
	owner    string
	repo     string
	workflow *config.Workflow
}

func (s *openAPINativeState) Sync(ctx context.Context, number string, state domain.WorkflowState) error {
	target := s.workflow.ProviderStateFor(state)
	if target == "" {
		return nil
	}
	var updated issueDTO
	_, err := s.client.Do(ctx, http.MethodPatch,
		"/repos/"+url.PathEscape(s.owner)+"/issues/"+url.PathEscape(number), nil,
		map[string]string{"repo": s.repo, "state": target}, &updated)
	return err
}

func (s *openAPINativeState) Check(context.Context) error { return nil }

type enterpriseNativeState struct {
	client   *EnterpriseClient
	workflow *config.Workflow
}

func (s *enterpriseNativeState) Sync(ctx context.Context, number string, state domain.WorkflowState) error {
	title := s.workflow.EnterpriseStateFor(state)
	if title == "" {
		return nil
	}
	return s.client.SetIssueStateByTitle(ctx, number, title)
}

func (s *enterpriseNativeState) Check(ctx context.Context) error {
	return s.client.Check(ctx)
}

type compositeNativeState struct {
	parts []NativeStateSyncer
}

func (s *compositeNativeState) Sync(ctx context.Context, number string, state domain.WorkflowState) error {
	for _, part := range s.parts {
		if err := part.Sync(ctx, number, state); err != nil {
			return err
		}
	}
	return nil
}

func (s *compositeNativeState) Check(ctx context.Context) error {
	for _, part := range s.parts {
		if err := part.Check(ctx); err != nil {
			return err
		}
	}
	return nil
}

func newNativeStateSyncer(
	client Transport,
	owner, repo string,
	workflow *config.Workflow,
	enterprise *EnterpriseClient,
) NativeStateSyncer {
	parts := []NativeStateSyncer{
		&openAPINativeState{client: client, owner: owner, repo: repo, workflow: workflow},
	}
	if enterprise != nil {
		parts = append(parts, &enterpriseNativeState{client: enterprise, workflow: workflow})
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return &compositeNativeState{parts: parts}
}
