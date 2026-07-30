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
	baseURL     string
	credential  Credential
	http        *http.Client
	maxAttempts int
	sleep       func(context.Context, time.Duration) error
}

func NewClient(token string, timeout time.Duration) *Client {
	return NewClientWithCredential(defaultBaseURL, NewStaticTokenCredential(token), &http.Client{Timeout: timeout})
}

func NewClientWithBaseURL(baseURL, token string, httpClient *http.Client) *Client {
	if token == "" {
		return NewClientWithCredential(baseURL, nil, httpClient)
	}
	return NewClientWithCredential(baseURL, NewStaticTokenCredential(token), httpClient)
}

func NewClientWithCredential(baseURL string, credential Credential, httpClient *http.Client) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		credential:  credential,
		http:        httpClient,
		maxAttempts: 3,
		sleep:       sleepContext,
	}
}

func (c *Client) AccessCapabilities() AccessCapabilities {
	result := AccessCapabilities{Transport: TransportREST, CredentialMode: CredentialNone}
	if c.credential != nil {
		result.CredentialMode = c.credential.Mode()
		result.RefreshableCredential = c.credential.Refreshable()
	}
	return result
}

func (c *Client) Get(ctx context.Context, path string, query url.Values, target any) (*http.Response, error) {
	return c.Do(ctx, http.MethodGet, path, query, nil, target)
}

func (c *Client) get(ctx context.Context, path string, query url.Values, target any) (*http.Response, error) {
	return c.Get(ctx, path, query, target)
}

func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, target any) (*http.Response, error) {
	if query == nil {
		query = make(url.Values)
	} else {
		query = cloneValues(query)
	}
	if c.credential != nil {
		token, err := c.credential.AccessToken(ctx)
		if err != nil {
			return nil, err
		}
		query.Set("access_token", token)
	}
	endpoint := c.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var rawBody []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rawBody = raw
	}
	attempts := c.maxAttempts
	if attempts < 1 || method != http.MethodGet {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		response, err := c.doOnce(ctx, method, endpoint, rawBody, body != nil, target)
		if err == nil || !retryableRead(method, response, err) || attempt == attempts {
			return response, err
		}
		if err := c.sleep(ctx, retryDelay(attempt, response)); err != nil {
			return response, err
		}
	}
	panic("unreachable")
}

func (c *Client) doOnce(ctx context.Context, method, endpoint string, rawBody []byte, hasBody bool, target any) (*http.Response, error) {
	var reader io.Reader
	if hasBody {
		reader = bytes.NewReader(rawBody)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if hasBody {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("%w: request failed", provider.ErrUnavailable)
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

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, target any) (*http.Response, error) {
	return c.Do(ctx, method, path, query, body, target)
}

func cloneValues(values url.Values) url.Values {
	result := make(url.Values, len(values))
	for key, entries := range values {
		result[key] = append([]string(nil), entries...)
	}
	return result
}

func retryableRead(method string, response *http.Response, err error) bool {
	if method != http.MethodGet ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, provider.ErrAuthentication) ||
		errors.Is(err, provider.ErrPermission) ||
		errors.Is(err, provider.ErrNotFound) {
		return false
	}
	if response == nil {
		return errors.Is(err, provider.ErrUnavailable)
	}
	return response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
}

func retryDelay(attempt int, response *http.Response) time.Duration {
	if response != nil {
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds >= 0 {
			const maximumRetryAfter = 30
			if seconds > maximumRetryAfter {
				seconds = maximumRetryAfter
			}
			return time.Duration(seconds) * time.Second
		}
	}
	return time.Duration(100*(1<<(attempt-1))) * time.Millisecond
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
