package gitee

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"issue-flow/internal/provider"
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
