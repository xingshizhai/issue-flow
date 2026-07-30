package gitee

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/provider"
)

type Provider struct {
	client   Transport
	owner    string
	repo     string
	workflow config.Workflow

	actorOnce sync.Once
	actor     string
	actorErr  error
}

func New(client Transport, owner, repo string, workflow config.Workflow) *Provider {
	return &Provider{client: client, owner: owner, repo: repo, workflow: workflow}
}

func (p *Provider) Capabilities(context.Context) provider.Capabilities {
	access := p.client.AccessCapabilities()
	return provider.Capabilities{
		ReadIssues: true, WriteIssues: true, StrongClaimCAS: false, IdempotencyKeys: true,
		AccessTransport: access.Transport, CredentialMode: access.CredentialMode,
		RefreshableCredential: access.RefreshableCredential,
	}
}

func (p *Provider) Check(ctx context.Context) error {
	if _, err := p.currentActor(ctx); err != nil {
		return err
	}
	var repository struct {
		ID int64 `json:"id"`
	}
	_, err := p.client.Get(ctx, p.repoPath(""), nil, &repository)
	if err != nil {
		return err
	}
	names, err := p.listLabelNames(ctx)
	if err != nil {
		return err
	}
	var missing []string
	for _, state := range []domain.WorkflowState{
		domain.StateReady, domain.StateClaimed, domain.StateWorking,
		domain.StateBlocked, domain.StateReview, domain.StateDone,
	} {
		name := p.workflow.LabelFor(state)
		if !names[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: missing workflow labels: %s",
			provider.ErrMisconfigured, strings.Join(missing, ", "))
	}
	return nil
}

func (p *Provider) listLabelNames(ctx context.Context) (map[string]bool, error) {
	names := make(map[string]bool)
	for page := 1; ; page++ {
		var labels []labelDTO
		values := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
		response, err := p.client.Get(ctx, p.repoPath("/labels"), values, &labels)
		if err != nil {
			return nil, err
		}
		for _, label := range labels {
			names[label.Name] = true
		}
		if nextPage(response.Header.Get("Link")) == "" && len(labels) < 100 {
			return names, nil
		}
	}
}

func (p *Provider) ListIssues(ctx context.Context, query provider.ListQuery) (provider.IssuePage, error) {
	page := 1
	if query.Cursor != "" {
		var err error
		page, err = strconv.Atoi(query.Cursor)
		if err != nil || page < 1 {
			return provider.IssuePage{}, fmt.Errorf("invalid cursor %q", query.Cursor)
		}
	}
	perPage := query.Limit
	if perPage <= 0 || perPage > 100 {
		perPage = 50
	}
	values := url.Values{
		"state":     {"all"},
		"page":      {strconv.Itoa(page)},
		"per_page":  {strconv.Itoa(perPage)},
		"sort":      {"created"},
		"direction": {"asc"},
	}
	if query.State != "" {
		label := p.workflow.LabelFor(query.State)
		if label == "" {
			return provider.IssuePage{}, fmt.Errorf("workflow state %q has no label mapping", query.State)
		}
		values.Set("labels", label)
	}
	var payload []issueDTO
	response, err := p.client.Get(ctx, p.repoPath("/issues"), values, &payload)
	if err != nil {
		return provider.IssuePage{}, err
	}
	result := provider.IssuePage{Items: make([]domain.Issue, 0, len(payload))}
	for _, item := range payload {
		result.Items = append(result.Items, p.mapIssue(item, nil))
	}
	if next := nextPage(response.Header.Get("Link")); next != "" {
		result.NextCursor = next
	} else if len(payload) == perPage {
		result.NextCursor = strconv.Itoa(page + 1)
	}
	return result, nil
}

func (p *Provider) GetIssue(ctx context.Context, number string) (domain.Issue, error) {
	var issue issueDTO
	if _, err := p.client.Get(ctx, p.repoPath("/issues/"+url.PathEscape(number)), nil, &issue); err != nil {
		return domain.Issue{}, err
	}
	comments, err := p.listComments(ctx, number)
	if err != nil {
		return domain.Issue{}, err
	}
	result := p.mapIssue(issue, comments)
	p.applyEvents(ctx, &result, comments)
	return result, nil
}

func (p *Provider) CreateIssue(ctx context.Context, input provider.CreateIssueInput) (domain.Issue, error) {
	issueType := "需求"
	if input.Type == "bug" {
		issueType = "缺陷"
	}
	var created issueDTO
	if _, err := p.client.Do(ctx, http.MethodPost,
		"/repos/"+url.PathEscape(p.owner)+"/issues", nil, map[string]string{
			"repo": p.repo, "title": input.Title, "body": input.Body,
			"labels": strings.Join(input.Labels, ","), "issue_type": issueType,
		}, &created); err != nil {
		return domain.Issue{}, err
	}
	return p.GetIssue(ctx, created.Number)
}

func (p *Provider) UpdateIssue(ctx context.Context, number string, change provider.IssueChange, precondition provider.Precondition) (domain.Issue, error) {
	if err := provider.ValidateChange(change); err != nil {
		return domain.Issue{}, err
	}
	current, err := p.GetIssue(ctx, number)
	if err != nil {
		return domain.Issue{}, err
	}
	for _, event := range current.Events {
		if event.OperationID == change.Event.OperationID {
			if !provider.SameOperation(event, change.Event) {
				return domain.Issue{}, fmt.Errorf(
					"%w: operation ID is already used with different semantics",
					provider.ErrPreconditionFailed,
				)
			}
			changed := false
			if change.WorkflowState != nil && !p.hasWorkflowLabel(current.Labels, *change.WorkflowState) {
				labels := p.replaceWorkflowLabel(current.Labels, *change.WorkflowState)
				if _, err := p.client.Do(ctx, http.MethodPut, p.repoPath("/issues/"+url.PathEscape(number)+"/labels"), nil, labels, &[]labelDTO{}); err != nil {
					return domain.Issue{}, fmt.Errorf("resume label update: %w", err)
				}
				changed = true
			}
			if change.WorkflowState != nil && p.nativeStateNeedsSync(current, *change.WorkflowState) {
				if err := p.syncNativeState(ctx, number, *change.WorkflowState); err != nil {
					return domain.Issue{}, fmt.Errorf("resume native state update: %w", err)
				}
				changed = true
			}
			if changed {
				return p.GetIssue(ctx, number)
			}
			return current, nil
		}
	}
	if precondition.Version != "" && current.Version != precondition.Version {
		return domain.Issue{}, fmt.Errorf("%w: version changed", provider.ErrPreconditionFailed)
	}
	if precondition.WorkflowState != "" && current.WorkflowState != precondition.WorkflowState {
		return domain.Issue{}, fmt.Errorf("%w: state is %s", provider.ErrPreconditionFailed, current.WorkflowState)
	}
	if precondition.LeaseID != "" && (current.Lease == nil || current.Lease.ID != precondition.LeaseID) {
		return domain.Issue{}, fmt.Errorf("%w: lease changed", provider.ErrPreconditionFailed)
	}
	eventBody, err := encodeEvent(change)
	if err != nil {
		return domain.Issue{}, err
	}
	var comment noteDTO
	if _, err := p.client.Do(ctx, http.MethodPost, p.repoPath("/issues/"+url.PathEscape(number)+"/comments"), nil,
		map[string]string{"body": eventBody}, &comment); err != nil {
		return domain.Issue{}, err
	}
	if change.WorkflowState != nil {
		labels := p.replaceWorkflowLabel(current.Labels, *change.WorkflowState)
		if _, err := p.client.Do(ctx, http.MethodPut, p.repoPath("/issues/"+url.PathEscape(number)+"/labels"), nil, labels, &[]labelDTO{}); err != nil {
			return domain.Issue{}, fmt.Errorf("event written but label update failed: %w", err)
		}
		if err := p.syncNativeState(ctx, number, *change.WorkflowState); err != nil {
			return domain.Issue{}, fmt.Errorf("event and label written but native state update failed: %w", err)
		}
	}
	return p.GetIssue(ctx, number)
}

func (p *Provider) nativeStateNeedsSync(issue domain.Issue, state domain.WorkflowState) bool {
	return p.workflow.AutoClose && state == domain.StateDone &&
		issue.ProviderState != domain.ProviderStateClosed
}

func (p *Provider) syncNativeState(ctx context.Context, number string, state domain.WorkflowState) error {
	if !p.workflow.AutoClose || state != domain.StateDone {
		return nil
	}
	var updated issueDTO
	_, err := p.client.Do(ctx, http.MethodPatch,
		"/repos/"+url.PathEscape(p.owner)+"/issues/"+url.PathEscape(number), nil,
		map[string]string{"repo": p.repo, "state": "closed"}, &updated)
	return err
}

func (p *Provider) hasWorkflowLabel(labels []domain.Label, state domain.WorkflowState) bool {
	expected := p.workflow.LabelFor(state)
	for _, label := range labels {
		if label.Name == expected {
			return true
		}
	}
	return false
}

func (p *Provider) repoPath(suffix string) string {
	return "/repos/" + url.PathEscape(p.owner) + "/" + url.PathEscape(p.repo) + suffix
}

func (p *Provider) listComments(ctx context.Context, number string) ([]noteDTO, error) {
	var all []noteDTO
	for page := 1; ; page++ {
		var batch []noteDTO
		values := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
		if _, err := p.client.Get(ctx, p.repoPath("/issues/"+url.PathEscape(number)+"/comments"), values, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
}

func (p *Provider) mapIssue(item issueDTO, notes []noteDTO) domain.Issue {
	result := domain.Issue{
		ID: strconv.FormatInt(item.ID, 10), Number: item.Number, Title: item.Title, Body: item.Body,
		ProviderState: domain.ProviderState(item.State), URL: item.HTMLURL,
		Version:   item.UpdatedAt.UTC().Format(time.RFC3339Nano),
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		Labels:   make([]domain.Label, 0, len(item.Labels)),
		Comments: make([]domain.Comment, 0, len(notes)),
		Events:   []domain.WorkflowEvent{}, Attachments: []domain.Attachment{},
	}
	for _, label := range item.Labels {
		result.Labels = append(result.Labels, domain.Label{Name: label.Name})
		if state := p.workflow.StateForLabel(label.Name); state != "" {
			result.WorkflowState = state
		}
	}
	for _, note := range notes {
		result.Comments = append(result.Comments, domain.Comment{
			ID: strconv.FormatInt(note.ID, 10), Body: note.Body,
			Author:    domain.Actor{ID: strconv.FormatInt(note.User.ID, 10), Login: note.User.Login},
			CreatedAt: note.CreatedAt,
		})
	}
	return result
}

func (p *Provider) applyEvents(ctx context.Context, issue *domain.Issue, notes []noteDTO) {
	actor, err := p.currentActor(ctx)
	if err != nil {
		return
	}
	type trustedRecord struct {
		record    eventRecord
		createdAt time.Time
		id        int64
	}
	var records []trustedRecord
	for _, note := range notes {
		if note.User.Login != actor {
			continue
		}
		record, ok := decodeEvent(note.Body)
		if !ok {
			continue
		}
		records = append(records, trustedRecord{record: record, createdAt: note.CreatedAt, id: note.ID})
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].createdAt.Equal(records[j].createdAt) {
			return records[i].id < records[j].id
		}
		return records[i].createdAt.Before(records[j].createdAt)
	})
	seen := make(map[string]bool)
	var derivedState domain.WorkflowState
	var lease *domain.Lease
	for _, trusted := range records {
		record := trusted.record
		if seen[record.Event.OperationID] {
			continue
		}
		seen[record.Event.OperationID] = true
		if derivedState == "" {
			derivedState = record.Event.From
		}
		if record.Event.From != derivedState {
			continue
		}
		switch record.Event.Operation {
		case "claim":
			if derivedState != domain.StateReady || record.Lease == nil ||
				record.Lease.ID == "" || record.Event.LeaseID != record.Lease.ID {
				continue
			}
			copied := *record.Lease
			lease = &copied
		case "start", "heartbeat", "progress", "block", "release", "finish":
			if lease == nil || record.Event.LeaseID != lease.ID {
				continue
			}
		case "complete":
			if derivedState != domain.StateReview || lease != nil ||
				record.Event.LeaseID != "" || record.Event.To != domain.StateDone {
				continue
			}
		case "reclaim":
			if lease == nil || !record.ClearLease {
				continue
			}
		default:
			continue
		}
		issue.Events = append(issue.Events, record.Event)
		if record.ClearLease {
			lease = nil
		} else if record.Lease != nil {
			copied := *record.Lease
			lease = &copied
		}
		derivedState = record.Event.To
	}
	if derivedState != "" {
		issue.WorkflowState = derivedState
	}
	issue.Lease = lease
}

func (p *Provider) currentActor(ctx context.Context) (string, error) {
	p.actorOnce.Do(func() {
		var user userDTO
		_, p.actorErr = p.client.Get(ctx, "/user", nil, &user)
		p.actor = user.Login
	})
	return p.actor, p.actorErr
}

func (p *Provider) replaceWorkflowLabel(labels []domain.Label, next domain.WorkflowState) []string {
	result := make([]string, 0, len(labels)+1)
	for _, label := range labels {
		if p.workflow.StateForLabel(label.Name) == "" {
			result = append(result, label.Name)
		}
	}
	result = append(result, p.workflow.LabelFor(next))
	return result
}

func nextPage(link string) string {
	for _, item := range strings.Split(link, ",") {
		if !strings.Contains(item, `rel="next"`) {
			continue
		}
		start := strings.Index(item, "<")
		end := strings.Index(item, ">")
		if start < 0 || end <= start {
			continue
		}
		parsed, err := url.Parse(item[start+1 : end])
		if err == nil {
			return parsed.Query().Get("page")
		}
	}
	return ""
}

type issueDTO struct {
	ID        int64      `json:"id"`
	Number    string     `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	State     string     `json:"state"`
	HTMLURL   string     `json:"html_url"`
	Labels    []labelDTO `json:"labels"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type labelDTO struct {
	Name string `json:"name"`
}

type userDTO struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type noteDTO struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	User      userDTO   `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}
