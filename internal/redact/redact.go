package redact

import (
	"regexp"
	"strings"

	"github.com/xingshizhai/issue-flow/internal/domain"
)

const replacement = "[REDACTED]"

var defaultKeys = []string{
	"password", "passwd", "token", "access_token", "refresh_token",
	"cookie", "set-cookie", "authorization", "api_key", "secret", "private_key",
}

type Redactor struct {
	patterns []*regexp.Regexp
}

func New(configured []string) Redactor {
	seen := make(map[string]bool)
	keys := append(append([]string(nil), defaultKeys...), configured...)
	var patterns []*regexp.Regexp
	for _, key := range keys {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		quoted := regexp.QuoteMeta(key)
		patterns = append(patterns,
			regexp.MustCompile(`(?i)("(?:`+quoted+`)"\s*:\s*")[^"]*(")`),
			regexp.MustCompile(`(?i)(\b(?:`+quoted+`)\b\s*[=:]\s*)[^\s,;&]+`),
		)
	}
	patterns = append(patterns,
		regexp.MustCompile(`(?i)(\bauthorization\b\s*[=:]\s*)[^\r\n]+`),
		regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
	)
	return Redactor{patterns: patterns}
}

func (r Redactor) String(value string) string {
	for index, pattern := range r.patterns {
		if index%2 == 0 && index < len(r.patterns)-2 {
			value = pattern.ReplaceAllString(value, `${1}`+replacement+`${2}`)
			continue
		}
		value = pattern.ReplaceAllString(value, `${1}`+replacement)
	}
	return value
}

func (r Redactor) Issue(issue domain.Issue) domain.Issue {
	issue.Title = r.String(issue.Title)
	issue.Body = r.String(issue.Body)
	issue.URL = r.String(issue.URL)
	for index := range issue.Comments {
		issue.Comments[index].Body = r.String(issue.Comments[index].Body)
	}
	for index := range issue.Attachments {
		issue.Attachments[index].Name = r.String(issue.Attachments[index].Name)
		issue.Attachments[index].URL = r.String(issue.Attachments[index].URL)
	}
	for index := range issue.Events {
		issue.Events[index].Message = r.String(issue.Events[index].Message)
	}
	return issue
}
