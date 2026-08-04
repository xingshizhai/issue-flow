package gitee

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xingshizhai/issue-flow/internal/provider"
)

const defaultEnterpriseAPIBase = "https://api.gitee.com/enterprises"

// EnterpriseClient talks to Gitee Enterprise HTTP API (same surface as mcp-gitee-ent).
type EnterpriseClient struct {
	baseURL    string
	token      string
	httpClient *http.Client

	mu            sync.Mutex
	enterpriseID  int64
	pathHint      string
	stateCache    map[int]map[string]int // issueTypeID -> title -> stateID
}

func NewEnterpriseClient(token, apiBase string, enterpriseID int64, path string, timeout time.Duration) *EnterpriseClient {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		base = defaultEnterpriseAPIBase
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &EnterpriseClient{
		baseURL:      base,
		token:        token,
		httpClient:   &http.Client{Timeout: timeout},
		enterpriseID: enterpriseID,
		pathHint:     strings.TrimSpace(path),
		stateCache:   make(map[int]map[string]int),
	}
}

func (c *EnterpriseClient) ResolveEnterpriseID(ctx context.Context) (int64, error) {
	if c.enterpriseID > 0 {
		return c.enterpriseID, nil
	}
	var page struct {
		Data []struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/list", nil, nil, &page); err != nil {
		return 0, err
	}
	hint := strings.ToLower(c.pathHint)
	for _, item := range page.Data {
		if hint != "" && (strings.EqualFold(item.Path, hint) || strings.EqualFold(item.Name, hint)) {
			c.enterpriseID = item.ID
			return item.ID, nil
		}
	}
	if hint == "" && len(page.Data) == 1 {
		c.enterpriseID = page.Data[0].ID
		return c.enterpriseID, nil
	}
	return 0, fmt.Errorf("%w: could not resolve enterprise id (set provider.enterprise.id)",
		provider.ErrMisconfigured)
}

func (c *EnterpriseClient) Check(ctx context.Context) error {
	_, err := c.ResolveEnterpriseID(ctx)
	return err
}

type enterpriseIssueDetail struct {
	Ident string `json:"ident"`
	IssueType *struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	} `json:"issue_type"`
	IssueState *struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	} `json:"issue_state"`
}

func (c *EnterpriseClient) GetIssue(ctx context.Context, number string) (enterpriseIssueDetail, error) {
	entID, err := c.ResolveEnterpriseID(ctx)
	if err != nil {
		return enterpriseIssueDetail{}, err
	}
	query := url.Values{"qt": {"ident"}}
	var detail enterpriseIssueDetail
	path := fmt.Sprintf("/%d/issues/%s", entID, url.PathEscape(number))
	if err := c.do(ctx, http.MethodGet, path, query, nil, &detail); err != nil {
		return enterpriseIssueDetail{}, err
	}
	return detail, nil
}

func (c *EnterpriseClient) SetIssueStateByTitle(ctx context.Context, number, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	detail, err := c.GetIssue(ctx, number)
	if err != nil {
		return err
	}
	if detail.IssueState != nil && detail.IssueState.Title == title {
		return nil
	}
	if detail.IssueType == nil || detail.IssueType.ID == 0 {
		return fmt.Errorf("%w: issue %s has no issue_type for enterprise state sync",
			provider.ErrMisconfigured, number)
	}
	stateID, err := c.lookupStateID(ctx, detail.IssueType.ID, title)
	if err != nil {
		return err
	}
	if detail.IssueState != nil && detail.IssueState.ID == stateID {
		return nil
	}
	entID, err := c.ResolveEnterpriseID(ctx)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"qt":             "ident",
		"issue_state_id": stateID,
	}
	path := fmt.Sprintf("/%d/issues/%s", entID, url.PathEscape(number))
	return c.do(ctx, http.MethodPut, path, nil, payload, &enterpriseIssueDetail{})
}

func (c *EnterpriseClient) lookupStateID(ctx context.Context, issueTypeID int, title string) (int, error) {
	c.mu.Lock()
	if cached, ok := c.stateCache[issueTypeID]; ok {
		if id, ok := cached[title]; ok {
			c.mu.Unlock()
			return id, nil
		}
	}
	c.mu.Unlock()

	entID, err := c.ResolveEnterpriseID(ctx)
	if err != nil {
		return 0, err
	}
	byTitle := make(map[string]int)
	for page := 1; ; page++ {
		query := url.Values{
			"page":     {strconv.Itoa(page)},
			"per_page": {"50"},
		}
		var resp struct {
			Data []struct {
				ID    int    `json:"id"`
				Title string `json:"title"`
			} `json:"data"`
		}
		path := fmt.Sprintf("/%d/issue_types/%d/issue_states", entID, issueTypeID)
		if err := c.do(ctx, http.MethodGet, path, query, nil, &resp); err != nil {
			return 0, err
		}
		for _, item := range resp.Data {
			byTitle[item.Title] = item.ID
		}
		if len(resp.Data) < 50 {
			break
		}
	}
	c.mu.Lock()
	c.stateCache[issueTypeID] = byTitle
	c.mu.Unlock()
	id, ok := byTitle[title]
	if !ok {
		return 0, fmt.Errorf("%w: enterprise issue state %q not found for issue type %d",
			provider.ErrMisconfigured, title, issueTypeID)
	}
	return id, nil
}

func (c *EnterpriseClient) do(
	ctx context.Context,
	method, path string,
	query url.Values,
	payload any,
	out any,
) error {
	if c.token == "" {
		return fmt.Errorf("%w: empty enterprise token", provider.ErrAuthentication)
	}
	full := c.baseURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Gitee enterprise MCP endpoints reject non-MCP clients with
	// "Only For MCP Gitee Enterprise Application".
	req.Header.Set("User-Agent", "mcp-gitee-ent issue-flow")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: enterprise API %s", provider.ErrAuthentication, resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("enterprise API %s %s: %s", method, path, truncateBody(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode enterprise response: %w (%s)", err, truncateBody(raw))
	}
	return nil
}

func truncateBody(raw []byte) string {
	const max = 240
	s := strings.TrimSpace(string(raw))
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
