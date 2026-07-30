package gitee

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"issue-flow/internal/provider"
)

const defaultBaseURL = "https://gitee.com/api/v5"

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(token string, timeout time.Duration) *Client {
	return NewClientWithBaseURL(defaultBaseURL, token, &http.Client{Timeout: timeout})
}

func NewClientWithBaseURL(baseURL, token string, httpClient *http.Client) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    httpClient,
	}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, target any) (*http.Response, error) {
	return c.do(ctx, http.MethodGet, path, query, nil, target)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, target any) (*http.Response, error) {
	if query == nil {
		query = make(url.Values)
	}
	if c.token != "" {
		query.Set("access_token", c.token)
	}
	endpoint := c.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", provider.ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, mapHTTPError(response)
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		_, err = io.Copy(io.Discard, response.Body)
		return response, err
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(target); err != nil {
		return response, fmt.Errorf("decode gitee response: %w", err)
	}
	return response, nil
}

func mapHTTPError(response *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var payload struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &payload)
	message := strings.TrimSpace(payload.Message)
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	var category error
	switch response.StatusCode {
	case http.StatusUnauthorized:
		if strings.Contains(message, "权限") || strings.Contains(strings.ToLower(message), "permission") {
			category = provider.ErrPermission
		} else {
			category = provider.ErrAuthentication
		}
	case http.StatusForbidden:
		category = provider.ErrPermission
	case http.StatusNotFound:
		category = provider.ErrNotFound
	case http.StatusTooManyRequests:
		category = provider.ErrRateLimited
	default:
		if response.StatusCode >= 500 {
			category = provider.ErrUnavailable
		} else {
			category = errors.New("gitee request rejected")
		}
	}
	return fmt.Errorf("%w: status %s: %s", category, strconv.Itoa(response.StatusCode), message)
}
