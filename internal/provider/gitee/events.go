package gitee

import (
	"encoding/json"
	"strings"

	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/provider"
)

const (
	eventPrefix = "<!-- issue-flow:event\n"
	eventSuffix = "\n-->"
)

type eventRecord struct {
	Version    int                  `json:"version"`
	Event      domain.WorkflowEvent `json:"event"`
	Lease      *domain.Lease        `json:"lease,omitempty"`
	ClearLease bool                 `json:"clearLease,omitempty"`
}

func encodeEvent(change provider.IssueChange) (string, error) {
	record := eventRecord{
		Version: 1, Event: change.Event, Lease: change.Lease, ClearLease: change.ClearLease,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return eventPrefix + string(raw) + eventSuffix + "\n\nIssue Flow: " + change.Event.Operation, nil
}

func decodeEvent(body string) (eventRecord, bool) {
	start := strings.Index(body, eventPrefix)
	if start < 0 {
		return eventRecord{}, false
	}
	start += len(eventPrefix)
	end := strings.Index(body[start:], eventSuffix)
	if end < 0 {
		return eventRecord{}, false
	}
	var record eventRecord
	if err := json.Unmarshal([]byte(body[start:start+end]), &record); err != nil || record.Version != 1 ||
		record.Event.Version != 1 || record.Event.OperationID == "" {
		return eventRecord{}, false
	}
	return record, true
}
