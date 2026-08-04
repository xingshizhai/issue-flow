package gitee

import (
	"strings"
	"testing"
	"time"

	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/provider"
)

func TestEncodeEventIncludesHumanReadableMessage(t *testing.T) {
	t.Parallel()
	body, err := encodeEvent(provider.IssueChange{
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: "op_1", Operation: "finish",
			AgentID: "agent-a", Message: "Root cause: X\n\nFix: Y",
			From: domain.StateWorking, To: domain.StateReview,
			OccurredAt: time.Date(2026, 8, 4, 2, 0, 0, 0, time.UTC),
		},
		ClearLease: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "### Issue Flow: finish") {
		t.Fatalf("missing visible heading: %s", body)
	}
	if !strings.Contains(body, "Root cause: X") || !strings.Contains(body, "Fix: Y") {
		t.Fatalf("missing summary body: %s", body)
	}
	if _, ok := decodeEvent(body); !ok {
		t.Fatal("encoded event must still decode")
	}
}

func TestEncodeEventWithoutMessageKeepsHeading(t *testing.T) {
	t.Parallel()
	body, err := encodeEvent(provider.IssueChange{
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: "op_2", Operation: "start",
			AgentID: "agent-a", From: domain.StateClaimed, To: domain.StateWorking,
			OccurredAt: time.Date(2026, 8, 4, 2, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "### Issue Flow: start") {
		t.Fatalf("body = %s", body)
	}
	if strings.Count(body, "\n\n") < 1 {
		t.Fatalf("unexpected body: %s", body)
	}
}
