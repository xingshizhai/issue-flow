package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"issue-flow/internal/domain"
	"issue-flow/internal/provider"
)

type Store struct {
	path string
}

type fileData struct {
	Version int            `json:"version"`
	Issues  []domain.Issue `json:"issues"`
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Capabilities(context.Context) provider.Capabilities {
	return provider.Capabilities{
		ReadIssues:      true,
		WriteIssues:     true,
		StrongClaimCAS:  true,
		IdempotencyKeys: true,
	}
}

func (s *Store) Check(context.Context) error {
	_, err := s.read()
	return err
}

func (s *Store) ListIssues(ctx context.Context, query provider.ListQuery) (provider.IssuePage, error) {
	if err := ctx.Err(); err != nil {
		return provider.IssuePage{}, err
	}
	data, err := s.read()
	if err != nil {
		return provider.IssuePage{}, err
	}
	sort.Slice(data.Issues, func(i, j int) bool {
		if data.Issues[i].CreatedAt.Equal(data.Issues[j].CreatedAt) {
			return data.Issues[i].Number < data.Issues[j].Number
		}
		return data.Issues[i].CreatedAt.Before(data.Issues[j].CreatedAt)
	})
	offset := 0
	if query.Cursor != "" {
		offset, err = strconv.Atoi(query.Cursor)
		if err != nil || offset < 0 {
			return provider.IssuePage{}, fmt.Errorf("invalid cursor %q", query.Cursor)
		}
	}
	filtered := make([]domain.Issue, 0, len(data.Issues))
	for _, issue := range data.Issues {
		if query.State == "" || issue.WorkflowState == query.State {
			filtered = append(filtered, issue)
		}
	}
	if offset > len(filtered) {
		offset = len(filtered)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	end := min(offset+limit, len(filtered))
	page := provider.IssuePage{Items: filtered[offset:end]}
	if end < len(filtered) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func (s *Store) GetIssue(ctx context.Context, number string) (domain.Issue, error) {
	if err := ctx.Err(); err != nil {
		return domain.Issue{}, err
	}
	data, err := s.read()
	if err != nil {
		return domain.Issue{}, err
	}
	for _, issue := range data.Issues {
		if issue.Number == number {
			return issue, nil
		}
	}
	return domain.Issue{}, fmt.Errorf("%w: %s", provider.ErrNotFound, number)
}

func (s *Store) read() (fileData, error) {
	return s.readUnlocked()
}

func (s *Store) readUnlocked() (fileData, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return fileData{Version: 1, Issues: []domain.Issue{}}, nil
	}
	if err != nil {
		return fileData{}, err
	}
	var data fileData
	if err := json.Unmarshal(raw, &data); err != nil {
		return fileData{}, fmt.Errorf("decode fake provider data %s: %w", s.path, err)
	}
	if data.Version != 1 {
		return fileData{}, fmt.Errorf("unsupported fake provider data version %d", data.Version)
	}
	if data.Issues == nil {
		data.Issues = []domain.Issue{}
	}
	return data, nil
}

func (s *Store) UpdateIssue(ctx context.Context, number string, change provider.IssueChange, precondition provider.Precondition) (domain.Issue, error) {
	var updated domain.Issue
	err := s.withLock(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := s.readUnlocked()
		if err != nil {
			return err
		}
		index := -1
		for i := range data.Issues {
			if data.Issues[i].Number == number {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("%w: %s", provider.ErrNotFound, number)
		}
		current := data.Issues[index]
		for _, event := range current.Events {
			if event.OperationID == change.Event.OperationID {
				updated = current
				return nil
			}
		}
		if precondition.Version != "" && current.Version != precondition.Version {
			return fmt.Errorf("%w: version changed", provider.ErrPreconditionFailed)
		}
		if precondition.WorkflowState != "" && current.WorkflowState != precondition.WorkflowState {
			return fmt.Errorf("%w: state is %s", provider.ErrPreconditionFailed, current.WorkflowState)
		}
		if precondition.LeaseID != "" && (current.Lease == nil || current.Lease.ID != precondition.LeaseID) {
			return fmt.Errorf("%w: lease changed", provider.ErrPreconditionFailed)
		}
		if change.WorkflowState != nil {
			current.WorkflowState = *change.WorkflowState
		}
		if change.ClearLease {
			current.Lease = nil
		} else if change.Lease != nil {
			lease := *change.Lease
			current.Lease = &lease
		}
		current.Events = append(current.Events, change.Event)
		current.UpdatedAt = change.Event.OccurredAt
		current.Version = nextVersion(current.Version)
		data.Issues[index] = current
		if err := s.writeUnlocked(data); err != nil {
			return err
		}
		updated = current
		return nil
	})
	return updated, err
}

func nextVersion(current string) string {
	value, err := strconv.ParseUint(current, 10, 64)
	if err != nil {
		value = 0
	}
	return strconv.FormatUint(value+1, 10)
}

func (s *Store) writeUnlocked(data fileData) error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".issue-flow-fake-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return replaceFile(tempName, s.path)
}

func (s *Store) withLock(operation func() error) error {
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	unlock, err := lockFile(lock)
	if err != nil {
		return err
	}
	defer unlock()
	return operation()
}

func ResolvePath(configPath, dataPath string) string {
	if filepath.IsAbs(dataPath) {
		return dataPath
	}
	return filepath.Join(filepath.Dir(configPath), dataPath)
}
