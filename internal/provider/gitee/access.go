package gitee

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"issue-flow/internal/provider"
)

const (
	TransportREST = "rest"
	TransportMCP  = "mcp"

	CredentialToken = "token"
	CredentialOAuth = "oauth"
	CredentialNone  = "none"
)

type AccessCapabilities struct {
	Transport             string
	CredentialMode        string
	RefreshableCredential bool
}

// Transport is the Gitee access boundary used by the Provider. REST, OAuth,
// and future MCP adapters must preserve the same DTO and workflow semantics.
type Transport interface {
	Get(context.Context, string, url.Values, any) (*http.Response, error)
	Do(context.Context, string, string, url.Values, any, any) (*http.Response, error)
	AccessCapabilities() AccessCapabilities
}

// Credential supplies an access token without exposing refresh credentials to
// the Provider or project configuration.
type Credential interface {
	AccessToken(context.Context) (string, error)
	Mode() string
	Refreshable() bool
}

type StaticTokenCredential struct {
	token string
}

func NewStaticTokenCredential(token string) StaticTokenCredential {
	return StaticTokenCredential{token: token}
}

func (c StaticTokenCredential) AccessToken(context.Context) (string, error) {
	if c.token == "" {
		return "", fmt.Errorf("%w: empty Gitee token", provider.ErrAuthentication)
	}
	return c.token, nil
}

func (StaticTokenCredential) Mode() string {
	return CredentialToken
}

func (StaticTokenCredential) Refreshable() bool {
	return false
}

// OAuthCredentialSource is implemented by an approved external OAuth token
// manager. Refresh tokens remain behind this boundary.
type OAuthCredentialSource interface {
	AccessToken(context.Context) (string, error)
}

type OAuthCredential struct {
	source OAuthCredentialSource
}

func NewOAuthCredential(source OAuthCredentialSource) OAuthCredential {
	return OAuthCredential{source: source}
}

func (c OAuthCredential) AccessToken(ctx context.Context) (string, error) {
	if c.source == nil {
		return "", fmt.Errorf("%w: OAuth credential source is not configured", provider.ErrAuthentication)
	}
	token, err := c.source.AccessToken(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: OAuth access token unavailable", provider.ErrAuthentication)
	}
	if token == "" {
		return "", fmt.Errorf("%w: OAuth credential source returned an empty token", provider.ErrAuthentication)
	}
	return token, nil
}

func (OAuthCredential) Mode() string {
	return CredentialOAuth
}

func (OAuthCredential) Refreshable() bool {
	return true
}

// NewMCPTransport documents the reserved MCP boundary until an MCP adapter is
// implemented. Callers receive a stable capability error instead of fallback.
func NewMCPTransport() (Transport, error) {
	return nil, fmt.Errorf("%w: Gitee MCP transport is not implemented", provider.ErrUnsupported)
}
