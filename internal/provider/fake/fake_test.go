package fake

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"issue-flow/internal/domain"
	"issue-flow/internal/provider"
)

func TestListFiltersAndPaginates(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "issues.json")
	data := fileData{Version: 1, Issues: []domain.Issue{
		{Number: 3, Title: "third", WorkflowState: domain.StateReady, CreatedAt: time.Unix(3, 0)},
		{Number: 1, Title: "first", WorkflowState: domain.StateReady, CreatedAt: time.Unix(1, 0)},
		{Number: 2, Title: "working", WorkflowState: domain.StateWorking, CreatedAt: time.Unix(2, 0)},
	}}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store := New(path)
	first, err := store.ListIssues(context.Background(), provider.ListQuery{
		State: domain.StateReady, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].Number != 1 || first.NextCursor != "1" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second, err := store.ListIssues(context.Background(), provider.ListQuery{
		State: domain.StateReady, Limit: 1, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Number != 3 || second.NextCursor != "" {
		t.Fatalf("unexpected second page: %+v", second)
	}
}

func TestConcurrentUpdateHasOneWinner(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "issues.json")
	data := fileData{Version: 1, Issues: []domain.Issue{{
		Number: 1, Title: "race", WorkflowState: domain.StateReady, Version: "1",
	}}}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			<-start
			state := domain.StateClaimed
			_, err := New(path).UpdateIssue(context.Background(), 1, provider.IssueChange{
				WorkflowState: &state,
				Lease: &domain.Lease{
					ID: "lease_" + string(rune('a'+i)), AgentID: "agent",
					TokenHash: domain.HashLeaseToken(string(rune('a' + i))),
					ExpiresAt: time.Now().Add(time.Hour),
				},
				Event: domain.WorkflowEvent{
					Version: 1, OperationID: string(rune('a' + i)), Operation: "claim",
				},
			}, provider.Precondition{Version: "1", WorkflowState: domain.StateReady})
			results <- err
		}()
	}
	close(start)
	successes := 0
	conflicts := 0
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, provider.ErrPreconditionFailed):
			conflicts++
		default:
			t.Fatalf("unexpected update error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestGetIssueNotFound(t *testing.T) {
	t.Parallel()
	_, err := New(filepath.Join(t.TempDir(), "missing.json")).GetIssue(context.Background(), 42)
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("GetIssue() error = %v", err)
	}
}
