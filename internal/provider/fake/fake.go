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
		WriteIssues:     false,
		StrongClaimCAS:  false,
		IdempotencyKeys: false,
	}
}

func (s *Store) ListIssues(_ context.Context, query provider.ListQuery) (provider.IssuePage, error) {
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

func (s *Store) GetIssue(_ context.Context, number int) (domain.Issue, error) {
	data, err := s.read()
	if err != nil {
		return domain.Issue{}, err
	}
	for _, issue := range data.Issues {
		if issue.Number == number {
			return issue, nil
		}
	}
	return domain.Issue{}, fmt.Errorf("%w: %d", provider.ErrNotFound, number)
}

func (s *Store) read() (fileData, error) {
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

func ResolvePath(configPath, dataPath string) string {
	if filepath.IsAbs(dataPath) {
		return dataPath
	}
	return filepath.Join(filepath.Dir(configPath), dataPath)
}
