package domain

import "time"

type WorkflowState string

const (
	StateReady   WorkflowState = "ready"
	StateClaimed WorkflowState = "claimed"
	StateWorking WorkflowState = "working"
	StateBlocked WorkflowState = "blocked"
	StateReview  WorkflowState = "review"
	StateDone    WorkflowState = "done"
)

type ProviderState string

const (
	ProviderStateOpen        ProviderState = "open"
	ProviderStateProgressing ProviderState = "progressing"
	ProviderStateClosed      ProviderState = "closed"
	ProviderStateRejected    ProviderState = "rejected"
)

type Label struct {
	Name string `json:"name"`
}

type Actor struct {
	ID    string `json:"id"`
	Login string `json:"login"`
}

type Comment struct {
	ID        string    `json:"id"`
	Author    Actor     `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

type Attachment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type WorkflowEvent struct {
	Version     int           `json:"version"`
	OperationID string        `json:"operationId"`
	Operation   string        `json:"operation"`
	AgentID     string        `json:"agentId,omitempty"`
	LeaseID     string        `json:"leaseId,omitempty"`
	Message     string        `json:"message,omitempty"`
	From        WorkflowState `json:"from,omitempty"`
	To          WorkflowState `json:"to,omitempty"`
	OccurredAt  time.Time     `json:"occurredAt"`
	ExpiresAt   time.Time     `json:"expiresAt,omitempty"`
}

type Issue struct {
	ID            string          `json:"id"`
	Number        string          `json:"number"`
	Title         string          `json:"title"`
	Body          string          `json:"body"`
	ProviderState ProviderState   `json:"providerState"`
	WorkflowState WorkflowState   `json:"workflowState"`
	Labels        []Label         `json:"labels"`
	Assignees     []Actor         `json:"assignees"`
	Comments      []Comment       `json:"comments"`
	Attachments   []Attachment    `json:"attachments"`
	Lease         *Lease          `json:"lease,omitempty"`
	Events        []WorkflowEvent `json:"events"`
	URL           string          `json:"url"`
	Version       string          `json:"version"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

func (i Issue) Public() Issue {
	if i.Lease != nil {
		lease := *i.Lease
		lease.TokenHash = ""
		i.Lease = &lease
	}
	return i
}
