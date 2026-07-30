package gitee

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"issue-flow/internal/config"
	"issue-flow/internal/domain"
	"issue-flow/internal/provider"
)

func TestListIssuesMapsStateAndPagination(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/issues" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("labels") != "agent:ready" || r.URL.Query().Get("per_page") != "1" {
			t.Errorf("query = %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<https://example.test/issues?page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[{
			"id":10,"number":"IABC1","title":"bug","body":"details","state":"open",
			"html_url":"https://gitee.test/owner/repo/issues/IABC1",
			"labels":[{"name":"agent:ready"}],
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
				{"name":"agent:ready"},{"name":"agent:claimed"},
				{"name":"agent:working"},{"name":"agent:blocked"},
				{"name":"agent:review"},{"name":"agent:done"}
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
			_, _ = w.Write([]byte(`[{"name":"agent:ready"}]`))
		default:
			http.NotFound(w, r)
		}
	})
	err := testProvider(handler).Check(context.Background())
	if !errors.Is(err, provider.ErrMisconfigured) {
		t.Fatalf("error = %v", err)
	}
	for _, missing := range []string{
		"agent:blocked", "agent:claimed", "agent:done", "agent:review", "agent:working",
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
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent:claimed")))
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
			_, _ = w.Write([]byte(issueJSON("IABC1", "agent:claimed")))
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
	stateLabel := "agent:claimed"
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
			if len(labels) != 2 || labels[0] != "type:bug" || labels[1] != "agent:working" {
				t.Errorf("labels = %v", labels)
			}
			stateLabel = "agent:working"
			_, _ = w.Write([]byte(`[{"name":"type:bug"},{"name":"agent:working"}]`))
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
		"labels":[{"name":"type:bug"},{"name":%q}],
		"created_at":"2026-07-30T01:00:00Z","updated_at":"2026-07-30T02:00:00Z"
	}`, number, number, workflowLabel)
}
