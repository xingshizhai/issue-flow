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
	"time"

	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/provider"
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
		CommentIssues:   true,
		AdoptIssues:     true,
		StrongClaimCAS:  true,
		IdempotencyKeys: true,
		AccessTransport: "local",
		CredentialMode:  "none",
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

func (s *Store) CreateIssue(ctx context.Context, input provider.CreateIssueInput) (domain.Issue, error) {
	var created domain.Issue
	err := s.withLock(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := s.readUnlocked()
		if err != nil {
			return err
		}
		maximum := 0
		for _, issue := range data.Issues {
			if number, err := strconv.Atoi(issue.Number); err == nil && number > maximum {
				maximum = number
			}
		}
		now := time.Now().UTC()
		created = domain.Issue{
			ID: strconv.Itoa(maximum + 1), Number: strconv.Itoa(maximum + 1),
			Title: input.Title, Body: input.Body, ProviderState: domain.ProviderStateOpen,
			WorkflowState: domain.StateReady, Priority: input.Priority,
			Labels: []domain.Label{}, Assignees: []domain.Actor{}, Comments: []domain.Comment{},
			Attachments: []domain.Attachment{}, Events: []domain.WorkflowEvent{},
			Version: "1", CreatedAt: now, UpdatedAt: now,
		}
		for _, name := range input.Labels {
			created.Labels = append(created.Labels, domain.Label{Name: name})
		}
		data.Issues = append(data.Issues, created)
		return s.writeUnlocked(data)
	})
	return created, err
}

// AddComment appends a plain-text comment with no lease/state requirement.
func (s *Store) AddComment(ctx context.Context, number, body string) (domain.Comment, error) {
	var created domain.Comment
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
		now := time.Now().UTC()
		created = domain.Comment{
			ID:        strconv.Itoa(len(current.Comments) + 1),
			Body:      body,
			Author:    domain.Actor{ID: "fake", Login: "fake"},
			CreatedAt: now,
		}
		current.Comments = append(current.Comments, created)
		current.UpdatedAt = now
		current.Version = nextVersion(current.Version)
		data.Issues[index] = current
		return s.writeUnlocked(data)
	})
	return created, err
}

// AdoptIssue attaches the ready workflow state to an issue that has no
// workflow state yet, without touching ProviderState.
func (s *Store) AdoptIssue(ctx context.Context, number string) (domain.Issue, error) {
	var adopted domain.Issue
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
		if current.WorkflowState != "" {
			return fmt.Errorf("%w: issue %s already has workflow state %s",
				provider.ErrPreconditionFailed, number, current.WorkflowState)
		}
		current.WorkflowState = domain.StateReady
		current.UpdatedAt = time.Now().UTC()
		current.Version = nextVersion(current.Version)
		data.Issues[index] = current
		adopted = current
		return s.writeUnlocked(data)
	})
	return adopted, err
}

func (s *Store) read() (fileData, error) {
	return s.readUnlocked()
}

func (s *Store) readUnlocked() (fileData, error) {
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return fileData{Version: 1, Issues: []domain.Issue{}}, nil
	}
	if err != nil {
		return fileData{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fileData{}, fmt.Errorf("fake provider data must be a regular file, not a symlink")
	}
	file, err := os.Open(s.path)
	if err != nil {
		return fileData{}, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return fileData{}, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return fileData{}, fmt.Errorf("fake provider data changed while opening")
	}
	var data fileData
	if err := json.NewDecoder(file).Decode(&data); err != nil {
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
	if err := provider.ValidateChange(change); err != nil {
		return domain.Issue{}, err
	}
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
				if !provider.SameOperation(event, change.Event) {
					return fmt.Errorf("%w: operation ID is already used with different semantics",
						provider.ErrPreconditionFailed)
				}
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

func ResolvePath(configPath, dataPath string) (string, error) {
	cleaned := filepath.Clean(dataPath)
	if filepath.IsAbs(dataPath) || cleaned == "." || cleaned == ".." ||
		filepath.Base(cleaned) != cleaned {
		return "", fmt.Errorf("fake provider data path must be a filename inside the configuration directory")
	}
	path := filepath.Join(filepath.Dir(configPath), cleaned)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("fake provider data must be a regular file, not a symlink")
	}
	return path, nil
}
