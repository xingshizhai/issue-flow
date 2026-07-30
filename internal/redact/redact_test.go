package redact

import (
	"strings"
	"testing"

	"issue-flow/internal/domain"
)

func TestStringRedactsConfiguredAndDefaultPatterns(t *testing.T) {
	t.Parallel()

	input := `{"token":"json-secret","custom_key":"custom-secret"}
password=plain-secret
Authorization: Bearer bearer-secret
url=https://example.test/?access_token=query-secret&safe=yes
-----BEGIN PRIVATE KEY-----
private-secret
-----END PRIVATE KEY-----`
	output := New([]string{"custom_key"}).String(input)
	for _, secret := range []string{
		"json-secret", "custom-secret", "plain-secret", "bearer-secret",
		"query-secret", "private-secret",
	} {
		if strings.Contains(output, secret) {
			t.Errorf("output leaked %q: %s", secret, output)
		}
	}
	if !strings.Contains(output, `"token":"[REDACTED]"`) ||
		!strings.Contains(output, "safe=yes") {
		t.Fatalf("unexpected redaction output: %s", output)
	}
}

func TestIssueRedactsExternalTextWithoutChangingIdentity(t *testing.T) {
	t.Parallel()

	issue := domain.Issue{
		Number: "123",
		Title:  "token=title-secret",
		Body:   "password: body-secret",
		URL:    "https://example.test/123?access_token=url-secret",
		Comments: []domain.Comment{{
			Body: "api_key=comment-secret",
		}},
		Events: []domain.WorkflowEvent{{
			Message: "secret=event-secret",
		}},
	}
	result := New(nil).Issue(issue)
	if result.Number != issue.Number {
		t.Fatalf("number changed from %q to %q", issue.Number, result.Number)
	}
	for _, secret := range []string{
		"title-secret", "body-secret", "url-secret", "comment-secret", "event-secret",
	} {
		if strings.Contains(result.Title+result.Body+result.URL+
			result.Comments[0].Body+result.Events[0].Message, secret) {
			t.Errorf("redacted issue leaked %q: %+v", secret, result)
		}
	}
}
