package gitee

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/provider"
)

func TestListIssuesMapsStateAndPagination(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/issues" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("labels") != "agent-ready" || r.URL.Query().Get("per_page") != "1" {
			t.Errorf("query = %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<https://example.test/issues?page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[{
			"id":10,"number":"IABC1","title":"bug","body":"details","state":"open",
			"html_url":"https://gitee.test/owner/repo/issues/IABC1",
			"labels":[{"name":"agent-ready"}],
			"created_at":"2026-07-30T01:00:00Z","updated_at":"2026-07-30T02:00:00Z"
		}]`))
	})
	p := testProvider(handler)
	page, err := p.ListIssues(context.Background(), provider.ListQuery{State: domain.StateReady, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Number != "IABC1" ||
		page.Items[0].WorkflowState != domain.StateReady || page.NextCursor != "2" {
		t.Fatalf("page = %+v", page)
	}
}

func TestGetIssueMapsPriority(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/owner/repo/issues/IABC1":
			_, _ = w.Write([]byte(`{
				"id":10,"number":"IABC1","title":"bug","body":"details","state":"open",
				"html_url":"https://gitee.test/owner/repo/issues/IABC1","priority":3,
				"labels":[{"name":"agent-ready"}],
				"created_at":"2026-07-30T01:00:00Z","updated_at":"2026-07-30T02:00:00Z"
			}`))
		case "/repos/owner/repo/issues/IABC1/comments":
			_, _ = w.Write([]byte(`[]`))
		case "/user":
			_, _ = w.Write([]byte(`{"id":7,"login":"automation"}`))
		default:
			http.NotFound(w, r)
		}
	})
	issue, err := testProvider(handler).GetIssue(context.Background(), "IABC1")
	if err != nil {
		t.Fatal(err)
	}
	if issue.Priority != "high" {
		t.Fatalf("priority = %q", issue.Priority)
	}
}

func TestCreateIssueMapsNativeIssueType(t *testing.T) {
	t.Parallel()
	var createBody map[string]string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/issues":
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent-ready")))
		case r.URL.Path == "/repos/owner/repo/issues/IABC1":
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent-ready")))
		case r.URL.Path == "/repos/owner/repo/issues/IABC1/comments":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/user":
			_, _ = w.Write([]byte(`{"id":7,"login":"automation"}`))
		default:
			http.NotFound(w, r)
		}
	})
	_, err := testProvider(handler).CreateIssue(context.Background(), provider.CreateIssueInput{
		Title: "bug", Body: "details", Type: "bug",
		Labels: []string{"type-bug", "agent-ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if createBody["issue_type"] != "缺陷" || createBody["labels"] != "type-bug,agent-ready" {
		t.Fatalf("create body = %#v", createBody)
	}
	_, err = testProvider(handler).CreateIssue(context.Background(), provider.CreateIssueInput{
		Title: "feature", Body: "details", Type: "feature",
		Labels: []string{"type-feature", "agent-ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if createBody["issue_type"] != "需求" {
		t.Fatalf("feature create body = %#v", createBody)
	}
}

func TestCreateIssueWithPriorityRequiresEnterprise(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/issues":
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent-ready")))
		default:
			http.NotFound(w, r)
		}
	})
	_, err := testProvider(handler).CreateIssue(context.Background(), provider.CreateIssueInput{
		Title: "bug", Body: "details", Type: "bug",
		Labels: []string{"type-bug", "agent-ready"}, Priority: "high",
	})
	if !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestCreateIssueSetsEnterprisePriority(t *testing.T) {
	t.Parallel()
	var enterprisePayload map[string]any
	entServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPut || r.URL.Path != "/42/issues/IABC1" {
			t.Fatalf("unexpected enterprise request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&enterprisePayload); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"ident":"IABC1"}`))
	}))
	t.Cleanup(entServer.Close)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/issues":
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent-ready")))
		case r.URL.Path == "/repos/owner/repo/issues/IABC1":
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent-ready")))
		case r.URL.Path == "/repos/owner/repo/issues/IABC1/comments":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/user":
			_, _ = w.Write([]byte(`{"id":7,"login":"automation"}`))
		default:
			http.NotFound(w, r)
		}
	})
	client := NewClientWithBaseURL("https://gitee.test", "test-token", memoryHTTPClient(handler))
	enterprise := NewEnterpriseClient("ent-token", entServer.URL, 42, "", time.Second)
	p := NewWithEnterprise(client, "owner", "repo", config.Default().Workflow, enterprise)

	_, err := p.CreateIssue(context.Background(), provider.CreateIssueInput{
		Title: "bug", Body: "details", Type: "bug",
		Labels: []string{"type-bug", "agent-ready"}, Priority: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if enterprisePayload["qt"] != "ident" {
		t.Fatalf("payload = %#v", enterprisePayload)
	}
	if priority, ok := enterprisePayload["priority"].(float64); !ok || int(priority) != 3 {
		t.Fatalf("priority = %#v", enterprisePayload["priority"])
	}
}

func TestAddCommentPostsPlainBodyWithoutEventEncoding(t *testing.T) {
	t.Parallel()
	var postedBody map[string]string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/repos/owner/repo/issues/IABC1/comments" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&postedBody); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"id":42,"body":"hello there","user":{"id":7,"login":"assistant"},"created_at":"2026-07-30T01:00:00Z"}`))
	})
	comment, err := testProvider(handler).AddComment(context.Background(), "IABC1", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if postedBody["body"] != "hello there" {
		t.Fatalf("posted body = %#v, want plain text (not an issue-flow:event wrapper)", postedBody)
	}
	if comment.ID != "42" || comment.Body != "hello there" || comment.Author.Login != "assistant" {
		t.Fatalf("comment = %+v", comment)
	}
}

func TestAdoptIssueAttachesReadyLabelToUnmanagedIssue(t *testing.T) {
	t.Parallel()
	var putBody []string
	adopted := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/issues/IABC1":
			labels := `[{"name":"bug"}]`
			if adopted {
				labels = `[{"name":"bug"},{"name":"agent-ready"}]`
			}
			fmt.Fprintf(w, `{
				"id":10,"number":"IABC1","title":"unmanaged","body":"details","state":"open",
				"html_url":"https://gitee.test/owner/repo/issues/IABC1",
				"labels":%s,
				"created_at":"2026-07-30T01:00:00Z","updated_at":"2026-07-30T02:00:00Z"
			}`, labels)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/issues/IABC1/comments":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPut && r.URL.Path == "/repos/owner/repo/issues/IABC1/labels":
			if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
				t.Fatal(err)
			}
			adopted = true
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	})
	issue, err := testProvider(handler).AdoptIssue(context.Background(), "IABC1")
	if err != nil {
		t.Fatal(err)
	}
	if issue.WorkflowState != domain.StateReady {
		t.Fatalf("workflow state = %s, want ready", issue.WorkflowState)
	}
	sort.Strings(putBody)
	if len(putBody) != 2 || putBody[0] != "agent-ready" || putBody[1] != "bug" {
		t.Fatalf("PUT labels = %v, want [agent-ready bug]", putBody)
	}
}

func TestAdoptIssueRejectsAlreadyManagedIssue(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repos/owner/repo/issues/IABC1":
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent-ready")))
		case r.URL.Path == "/repos/owner/repo/issues/IABC1/comments":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	})
	_, err := testProvider(handler).AdoptIssue(context.Background(), "IABC1")
	if !errors.Is(err, provider.ErrPreconditionFailed) {
		t.Fatalf("error = %v, want ErrPreconditionFailed", err)
	}
}

func TestCheckValidatesRepositoryAndWorkflowLabels(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"id":7,"login":"automation"}`))
		case "/repos/owner/repo":
			_, _ = w.Write([]byte(`{"id":10}`))
		case "/repos/owner/repo/labels":
			_, _ = w.Write([]byte(`[
				{"name":"agent-ready"},{"name":"agent-claimed"},
				{"name":"agent-working"},{"name":"agent-blocked"},
				{"name":"agent-review"},{"name":"agent-done"}
			]`))
		default:
			http.NotFound(w, r)
		}
	})
	if err := testProvider(handler).Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCheckReportsMissingWorkflowLabelsWithoutWriting(t *testing.T) {
	t.Parallel()

	writes := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"id":7,"login":"automation"}`))
		case "/repos/owner/repo":
			_, _ = w.Write([]byte(`{"id":10}`))
		case "/repos/owner/repo/labels":
			_, _ = w.Write([]byte(`[{"name":"agent-ready"}]`))
		default:
			http.NotFound(w, r)
		}
	})
	err := testProvider(handler).Check(context.Background())
	if !errors.Is(err, provider.ErrMisconfigured) {
		t.Fatalf("error = %v", err)
	}
	for _, missing := range []string{
		"agent-blocked", "agent-claimed", "agent-done", "agent-review", "agent-working",
	} {
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("error %q does not name missing label %q", err, missing)
		}
	}
	if writes != 0 {
		t.Fatalf("doctor check performed %d writes", writes)
	}
}

func TestUpdateRejectsOperationIDSemanticCollisionWithoutWriting(t *testing.T) {
	t.Parallel()

	lease := domain.Lease{
		ID: "lease_1", AgentID: "agent-a", TokenHash: domain.HashLeaseToken("secret"),
		ClaimedAt: time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	}
	existing := domain.WorkflowEvent{
		Version: 1, OperationID: "op_same", Operation: "claim",
		AgentID: "agent-a", LeaseID: "lease_1", Message: "first",
		From: domain.StateReady, To: domain.StateClaimed,
		OccurredAt: time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC),
	}
	body, err := encodeEvent(provider.IssueChange{Event: existing, Lease: &lease})
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/owner/repo/issues/IABC1":
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent-claimed")))
		case "/repos/owner/repo/issues/IABC1/comments":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": 1, "body": body,
				"user":       map[string]any{"id": 7, "login": "automation"},
				"created_at": "2026-07-30T02:00:00Z",
			}})
		case "/user":
			_, _ = w.Write([]byte(`{"id":7,"login":"automation"}`))
		default:
			http.NotFound(w, r)
		}
	})
	changed := existing
	changed.Message = "different"
	_, err = testProvider(handler).UpdateIssue(
		context.Background(),
		"IABC1",
		provider.IssueChange{Event: changed},
		provider.Precondition{},
	)
	if !errors.Is(err, provider.ErrPreconditionFailed) {
		t.Fatalf("error = %v", err)
	}
	if writes != 0 {
		t.Fatalf("semantic collision performed %d writes", writes)
	}
}

func TestUpdateRejectsInvalidChangeBeforeNetworkAccess(t *testing.T) {
	t.Parallel()

	requests := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	})
	working := domain.StateWorking
	_, err := testProvider(handler).UpdateIssue(
		context.Background(),
		"IABC1",
		provider.IssueChange{
			WorkflowState: &working,
			Event: domain.WorkflowEvent{
				Version: 1, OperationID: "op_invalid", Operation: "start",
				To: domain.StateReview,
			},
		},
		provider.Precondition{},
	)
	if !errors.Is(err, provider.ErrPreconditionFailed) {
		t.Fatalf("error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("invalid change performed %d network requests", requests)
	}
}

func TestSyncNativeStateClosesOnlyOptedInDoneIssues(t *testing.T) {
	t.Parallel()
	var requests int
	var lastState string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/owner/issues/IABC1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["repo"] != "repo" {
			t.Fatalf("body = %#v", body)
		}
		lastState = body["state"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(issueJSON("IABC1", "agent-done")))
	})
	p := testProvider(handler)
	if err := p.syncNativeState(context.Background(), "IABC1", domain.StateDone); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("auto_close=false performed %d requests", requests)
	}
	p.workflow.AutoClose = true
	if err := p.syncNativeState(context.Background(), "IABC1", domain.StateReview); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("review performed %d requests", requests)
	}
	if err := p.syncNativeState(context.Background(), "IABC1", domain.StateDone); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || lastState != "closed" {
		t.Fatalf("done performed %d requests state=%q", requests, lastState)
	}
}

func TestSyncNativeStateUsesConfiguredProviderStates(t *testing.T) {
	t.Parallel()
	var requests int
	var lastState string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		lastState = body["state"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(issueJSON("IABC1", "agent-working")))
	})
	p := testProvider(handler)
	p.workflow.SyncProviderState = true
	if err := p.syncNativeState(context.Background(), "IABC1", domain.StateWorking); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || lastState != "progressing" {
		t.Fatalf("requests=%d state=%q", requests, lastState)
	}
	p.workflow.ProviderStates = map[string]string{"review": "open"}
	if err := p.syncNativeState(context.Background(), "IABC1", domain.StateReview); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || lastState != "open" {
		t.Fatalf("override requests=%d state=%q", requests, lastState)
	}
}

func TestGetIssueAcceptsEventsOnlyFromTokenOwner(t *testing.T) {
	t.Parallel()
	lease := domain.Lease{
		ID: "lease_1", TokenHash: domain.HashLeaseToken("secret"), AgentID: "agent-a",
		ExpiresAt: time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC),
	}
	body, err := encodeEvent(provider.IssueChange{
		Lease: &lease,
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: "op_1", Operation: "claim",
			AgentID: "agent-a", LeaseID: lease.ID, From: domain.StateReady,
			To: domain.StateClaimed, OccurredAt: time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/owner/repo/issues/IABC1":
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent-claimed")))
		case "/repos/owner/repo/issues/IABC1/comments":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "body": body, "user": map[string]any{"id": 7, "login": "automation"}, "created_at": "2026-07-30T02:00:00Z"},
				{"id": 2, "body": body, "user": map[string]any{"id": 8, "login": "attacker"}, "created_at": "2026-07-30T02:01:00Z"},
			})
		case "/user":
			_, _ = w.Write([]byte(`{"id":7,"login":"automation"}`))
		default:
			http.NotFound(w, r)
		}
	})
	issue, err := testProvider(handler).GetIssue(context.Background(), "IABC1")
	if err != nil {
		t.Fatal(err)
	}
	if len(issue.Events) != 1 || issue.Lease == nil || issue.Lease.ID != lease.ID {
		t.Fatalf("issue events/lease = %+v / %+v", issue.Events, issue.Lease)
	}
}

func TestUpdateIssueWritesEventThenLabelsAndRereads(t *testing.T) {
	t.Parallel()
	lease := domain.Lease{
		ID: "lease_1", TokenHash: domain.HashLeaseToken("secret"), AgentID: "agent",
		ExpiresAt: time.Date(2026, 7, 30, 5, 0, 0, 0, time.UTC),
	}
	claimBody, err := encodeEvent(provider.IssueChange{
		Lease: &lease,
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: "op_claim", Operation: "claim",
			AgentID: "agent", LeaseID: lease.ID, From: domain.StateReady,
			To: domain.StateClaimed, OccurredAt: time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	commentWritten := false
	stateLabel := "agent-claimed"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/issues/IABC1":
			_, _ = w.Write([]byte(issueJSON("IABC1", stateLabel)))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/issues/IABC1/comments":
			comments := []map[string]any{{
				"id": 3, "body": claimBody, "user": map[string]any{"id": 7, "login": "automation"},
				"created_at": "2026-07-30T02:00:00Z",
			}}
			if commentWritten {
				body, _ := encodeEvent(provider.IssueChange{
					Event: domain.WorkflowEvent{
						Version: 1, OperationID: "op_start", Operation: "start",
						AgentID: "agent", LeaseID: "lease_1", From: domain.StateClaimed,
						To: domain.StateWorking, OccurredAt: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC),
					},
				})
				comments = append(comments, map[string]any{
					"id": 4, "body": body, "user": map[string]any{"id": 7, "login": "automation"},
					"created_at": "2026-07-30T03:00:00Z",
				})
			}
			_ = json.NewEncoder(w).Encode(comments)
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			_, _ = w.Write([]byte(`{"id":7,"login":"automation"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			commentWritten = true
			_, _ = w.Write([]byte(`{"id":4,"body":"ok","user":{"id":7,"login":"automation"},"created_at":"2026-07-30T03:00:00Z"}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/labels"):
			var labels []string
			if err := json.NewDecoder(r.Body).Decode(&labels); err != nil {
				t.Error(err)
			}
			if len(labels) != 2 || labels[0] != "type-bug" || labels[1] != "agent-working" {
				t.Errorf("labels = %v", labels)
			}
			stateLabel = "agent-working"
			_, _ = w.Write([]byte(`[{"name":"type-bug"},{"name":"agent-working"}]`))
		default:
			http.NotFound(w, r)
		}
	})
	p := testProvider(handler)
	next := domain.StateWorking
	updated, err := p.UpdateIssue(context.Background(), "IABC1", provider.IssueChange{
		WorkflowState: &next,
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: "op_start", Operation: "start",
			AgentID: "agent", LeaseID: "lease_1", From: domain.StateClaimed,
			To: next, OccurredAt: time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC),
		},
	}, provider.Precondition{
		Version: "2026-07-30T02:00:00Z", WorkflowState: domain.StateClaimed, LeaseID: lease.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.WorkflowState != domain.StateWorking {
		t.Fatalf("updated state = %s", updated.WorkflowState)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(calls, "\n")
	if strings.Index(joined, "POST /repos/owner/repo/issues/IABC1/comments") >
		strings.Index(joined, "PUT /repos/owner/repo/issues/IABC1/labels") {
		t.Fatalf("event was not written before labels:\n%s", joined)
	}
}

func testProvider(handler http.Handler) *Provider {
	client := NewClientWithBaseURL("https://gitee.test", "test-token", memoryHTTPClient(handler))
	return New(client, "owner", "repo", config.Default().Workflow)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func memoryHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			return recorder.Result(), nil
		}),
	}
}

func issueJSON(number, workflowLabel string) string {
	return fmt.Sprintf(`{
		"id":10,"number":%q,"title":"bug","body":"details","state":"open",
		"html_url":"https://gitee.test/owner/repo/issues/%s",
		"labels":[{"name":"type-bug"},{"name":%q}],
		"created_at":"2026-07-30T01:00:00Z","updated_at":"2026-07-30T02:00:00Z"
	}`, number, number, workflowLabel)
}
