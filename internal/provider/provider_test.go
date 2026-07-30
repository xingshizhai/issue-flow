package provider

import (
	"testing"

	"issue-flow/internal/domain"
)

func TestSameOperationIgnoresDeliveryTimingButNotSemantics(t *testing.T) {
	t.Parallel()

	existing := domain.WorkflowEvent{
		OperationID: "op_1", Operation: "progress", AgentID: "agent-a",
		LeaseID: "lease_1", Message: "tests pass",
		From: domain.StateWorking, To: domain.StateWorking,
	}
	retry := existing
	if !SameOperation(existing, retry) {
		t.Fatal("identical operation did not match")
	}
	retry.Message = "different result"
	if SameOperation(existing, retry) {
		t.Fatal("different operation message matched")
	}
}
