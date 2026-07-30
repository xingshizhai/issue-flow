package provider

import (
	"errors"
	"testing"

	"github.com/xingshizhai/issue-flow/internal/domain"
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

func TestValidateChangeRejectsContradictoryMutations(t *testing.T) {
	t.Parallel()

	working := domain.StateWorking
	lease := &domain.Lease{ID: "lease_1"}
	tests := []IssueChange{
		{
			Event: domain.WorkflowEvent{OperationID: "op_missing_version", Operation: "start"},
		},
		{
			WorkflowState: &working,
			Event: domain.WorkflowEvent{
				Version: 1, OperationID: "op_wrong_target", Operation: "start",
				To: domain.StateReview,
			},
		},
		{
			Lease: lease, ClearLease: true,
			Event: domain.WorkflowEvent{
				Version: 1, OperationID: "op_set_clear", Operation: "heartbeat",
				LeaseID: "lease_1",
			},
		},
		{
			Lease: lease,
			Event: domain.WorkflowEvent{
				Version: 1, OperationID: "op_wrong_lease", Operation: "heartbeat",
				LeaseID: "lease_2",
			},
		},
	}
	for index, change := range tests {
		if err := ValidateChange(change); !errors.Is(err, ErrPreconditionFailed) {
			t.Errorf("case %d error = %v", index, err)
		}
	}
}

func TestValidateChangeAcceptsConsistentMutation(t *testing.T) {
	t.Parallel()

	working := domain.StateWorking
	lease := &domain.Lease{ID: "lease_1"}
	change := IssueChange{
		WorkflowState: &working,
		Lease:         lease,
		Event: domain.WorkflowEvent{
			Version: 1, OperationID: "op_start", Operation: "start",
			LeaseID: "lease_1", To: domain.StateWorking,
		},
	}
	if err := ValidateChange(change); err != nil {
		t.Fatal(err)
	}
}
