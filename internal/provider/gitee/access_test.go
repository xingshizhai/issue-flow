package gitee

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"issue-flow/internal/provider"
)

type rotatingOAuthSource struct {
	mu     sync.Mutex
	tokens []string
	calls  int
}

type failingOAuthSource struct{}

func (failingOAuthSource) AccessToken(context.Context) (string, error) {
	return "", errors.New("refresh secret must not escape")
}

func (s *rotatingOAuthSource) AccessToken(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := s.tokens[s.calls]
	s.calls++
	return token, nil
}

func TestRESTOAuthCredentialIsResolvedForEveryRequest(t *testing.T) {
	t.Parallel()

	source := &rotatingOAuthSource{tokens: []string{"access-one", "access-two"}}
	var mu sync.Mutex
	var received []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.URL.Query().Get("access_token"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	client := NewClientWithCredential(
		"https://gitee.test",
		NewOAuthCredential(source),
		memoryHTTPClient(handler),
	)

	for range 2 {
		if _, err := client.Get(context.Background(), "/test", nil, &struct {
			OK bool `json:"ok"`
		}{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 2 || received[0] != "access-one" || received[1] != "access-two" {
		t.Fatalf("received access tokens = %v", received)
	}
	capabilities := client.AccessCapabilities()
	if capabilities.Transport != TransportREST ||
		capabilities.CredentialMode != CredentialOAuth ||
		!capabilities.RefreshableCredential {
		t.Fatalf("capabilities = %+v", capabilities)
	}
}

func TestMCPTransportFailsWithStableUnsupportedCapability(t *testing.T) {
	t.Parallel()

	transport, err := NewMCPTransport()
	if transport != nil || !errors.Is(err, provider.ErrUnsupported) {
		t.Fatalf("transport = %v, error = %v", transport, err)
	}
}

func TestOAuthCredentialRedactsSourceErrors(t *testing.T) {
	t.Parallel()

	_, err := NewOAuthCredential(failingOAuthSource{}).AccessToken(context.Background())
	if !errors.Is(err, provider.ErrAuthentication) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "refresh secret") {
		t.Fatalf("credential error leaked source detail: %v", err)
	}
}
