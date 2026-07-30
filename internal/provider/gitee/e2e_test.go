package gitee

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xingshizhai/issue-flow/internal/clock"
	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/workflow"
)

func TestGiteeE2E(t *testing.T) {
	if os.Getenv("ISSUE_FLOW_GITEE_E2E") != "1" {
		t.Skip("set ISSUE_FLOW_GITEE_E2E=1 to run against an authorized test repository")
	}
	token := os.Getenv("GITEE_TOKEN")
	owner := os.Getenv("GITEE_OWNER")
	repo := os.Getenv("GITEE_REPO")
	if token == "" || owner == "" || repo == "" {
		t.Fatal("GITEE_TOKEN, GITEE_OWNER, and GITEE_REPO are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := NewClient(token, 30*time.Second)
	cfg := config.Default()
	if os.Getenv("GITEE_E2E_USE_EXISTING_LABELS") == "1" {
		cfg.Workflow.ReadyLabel = "bug"
		cfg.Workflow.ClaimedLabel = "duplicate"
		cfg.Workflow.WorkingLabel = "enhancement"
		cfg.Workflow.BlockedLabel = "feature"
		cfg.Workflow.ReviewLabel = "invalid"
		cfg.Workflow.DoneLabel = "question"
	} else {
		ensureWorkflowLabels(t, ctx, client, owner, repo, cfg.Workflow)
	}
	created := createTestIssue(t, ctx, client, owner, repo, cfg.Workflow.ReadyLabel)
	if created.Number == "" {
		t.Fatal("Gitee returned an empty issue number")
	}
	t.Logf("created Gitee test issue %s", created.Number)

	p := New(client, owner, repo, cfg.Workflow)
	service := workflow.New(p, clock.Real{}, 10*time.Minute)
	claim, err := service.Claim(ctx, created.Number, "issue-flow-e2e", "op_e2e_claim", false)
	if err != nil {
		t.Fatal(err)
	}
	if claim.LeaseToken == "" || claim.Issue.WorkflowState != domain.StateClaimed {
		t.Fatalf("claim result = %+v", claim)
	}
	started, err := service.Start(ctx, created.Number, "issue-flow-e2e", claim.LeaseToken, "op_e2e_start", false)
	if err != nil {
		t.Fatal(err)
	}
	if started.Issue.WorkflowState != domain.StateWorking {
		t.Fatalf("start state = %s", started.Issue.WorkflowState)
	}
	if _, err := service.Heartbeat(ctx, created.Number, "issue-flow-e2e", claim.LeaseToken, "op_e2e_heartbeat", false); err != nil {
		t.Fatal(err)
	}
	released, err := service.Release(ctx, created.Number, "issue-flow-e2e", claim.LeaseToken, "op_e2e_release", "E2E verification complete", false)
	if err != nil {
		t.Fatal(err)
	}
	if released.Issue.WorkflowState != domain.StateReady || released.Issue.Lease != nil {
		t.Fatalf("release result = %+v", released.Issue)
	}
}

func ensureWorkflowLabels(t *testing.T, ctx context.Context, client *Client, owner, repo string, workflowConfig config.Workflow) {
	t.Helper()
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/labels"
	var existing []labelDTO
	if _, err := client.get(ctx, path, url.Values{"per_page": {"100"}}, &existing); err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(existing))
	for _, label := range existing {
		names[label.Name] = true
	}
	states := []domain.WorkflowState{
		domain.StateReady, domain.StateClaimed, domain.StateWorking,
		domain.StateBlocked, domain.StateReview, domain.StateDone,
	}
	for _, state := range states {
		name := workflowConfig.LabelFor(state)
		if names[name] {
			continue
		}
		var created labelDTO
		if _, err := client.do(ctx, http.MethodPost, path, nil, map[string]string{
			"name": name, "color": "428BCA",
		}, &created); err != nil {
			t.Fatalf("create label %s: %v", name, err)
		}
	}
}

func createTestIssue(t *testing.T, ctx context.Context, client *Client, owner, repo, readyLabel string) issueDTO {
	t.Helper()
	path := "/repos/" + url.PathEscape(owner) + "/issues"
	title := fmt.Sprintf("[issue-flow E2E] %s", time.Now().UTC().Format(time.RFC3339))
	var created issueDTO
	if _, err := client.do(ctx, http.MethodPost, path, nil, map[string]string{
		"repo":   repo,
		"title":  title,
		"body":   "Created by the explicitly enabled Issue Flow Gitee E2E test.",
		"labels": strings.TrimSpace(readyLabel),
	}, &created); err != nil {
		t.Fatal(err)
	}
	return created
}
