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
	ProviderStateOpen   ProviderState = "open"
	ProviderStateClosed ProviderState = "closed"
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

type Issue struct {
	ID            string        `json:"id"`
	Number        int           `json:"number"`
	Title         string        `json:"title"`
	Body          string        `json:"body"`
	ProviderState ProviderState `json:"providerState"`
	WorkflowState WorkflowState `json:"workflowState"`
	Labels        []Label       `json:"labels"`
	Assignees     []Actor       `json:"assignees"`
	Comments      []Comment     `json:"comments"`
	Attachments   []Attachment  `json:"attachments"`
	URL           string        `json:"url"`
	Version       string        `json:"version"`
	CreatedAt     time.Time     `json:"createdAt"`
	UpdatedAt     time.Time     `json:"updatedAt"`
}
