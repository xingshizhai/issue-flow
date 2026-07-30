package gitee

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xingshizhai/issue-flow/internal/provider"
)

func TestClientMapsErrorsWithoutLeakingToken(t *testing.T) {
	t.Parallel()
	const token = "top-secret-token"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("access_token") != token {
			t.Error("access token was not sent")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"bad credentials"}`))
	})
	client := NewClientWithBaseURL("https://gitee.test", token, memoryHTTPClient(handler))
	_, err := client.get(context.Background(), "/user", nil, &struct{}{})
	if !errors.Is(err, provider.ErrAuthentication) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("error leaked token")
	}
}

func TestClientRedactsSecretsFromProviderErrorMessage(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"rejected access_token=response-secret Authorization: Bearer response-bearer"}`))
	})
	client := NewClientWithBaseURL("https://gitee.test", "request-secret", memoryHTTPClient(handler))
	_, err := client.Get(context.Background(), "/test", nil, &struct{}{})
	if !errors.Is(err, provider.ErrPermission) {
		t.Fatalf("error = %v", err)
	}
	for _, secret := range []string{"response-secret", "response-bearer", "request-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("provider error leaked %q: %v", secret, err)
		}
	}
}

func TestClientMapsGiteePermissionMessageOn401(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"需要企业管理员权限"}`))
	})
	client := NewClientWithBaseURL("https://gitee.test", "secret", memoryHTTPClient(handler))
	_, err := client.get(context.Background(), "/test", nil, &struct{}{})
	if !errors.Is(err, provider.ErrPermission) {
		t.Fatalf("error = %v", err)
	}
}

func TestClientLimitsResponseBody(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	client := NewClientWithBaseURL("https://gitee.test", "", memoryHTTPClient(handler))
	var target struct {
		OK bool `json:"ok"`
	}
	if _, err := client.get(context.Background(), "/test", nil, &target); err != nil {
		t.Fatal(err)
	}
	if !target.OK {
		t.Fatal("response was not decoded")
	}
}

func TestClientRetriesTransientGET(t *testing.T) {
	t.Parallel()

	attempts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	client := NewClientWithBaseURL("https://gitee.test", "", memoryHTTPClient(handler))
	var delays []time.Duration
	client.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	var target struct {
		OK bool `json:"ok"`
	}
	if _, err := client.Get(context.Background(), "/test", nil, &target); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || !target.OK {
		t.Fatalf("attempts = %d, target = %+v", attempts, target)
	}
	if len(delays) != 2 || delays[0] != 2*time.Second || delays[1] != 2*time.Second {
		t.Fatalf("retry delays = %v", delays)
	}
}

func TestClientDoesNotRetryWrites(t *testing.T) {
	t.Parallel()

	attempts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	})
	client := NewClientWithBaseURL("https://gitee.test", "", memoryHTTPClient(handler))
	client.sleep = func(context.Context, time.Duration) error {
		t.Fatal("write request attempted to retry")
		return nil
	}
	_, err := client.Do(context.Background(), http.MethodPost, "/test", nil, map[string]string{"body": "event"}, nil)
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("write attempts = %d, want 1", attempts)
	}
}

func TestRetryDelayIsCapped(t *testing.T) {
	t.Parallel()

	response := &http.Response{Header: http.Header{"Retry-After": {"3600"}}}
	if delay := retryDelay(1, response); delay != 30*time.Second {
		t.Fatalf("retry delay = %s", delay)
	}
}

func TestClientRedactsTransportErrorURL(t *testing.T) {
	t.Parallel()

	const token = "transport-secret-token"
	client := NewClientWithBaseURL(
		"https://gitee.test",
		token,
		&http.Client{Transport: failingRoundTripper{}},
	)
	client.maxAttempts = 1
	_, err := client.Get(context.Background(), "/test", nil, &struct{}{})
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "access_token") {
		t.Fatalf("transport error leaked request URL: %v", err)
	}
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, errors.New(request.URL.String())
}
