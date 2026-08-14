package projectcontext

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/xingshizhai/issue-flow/internal/domain"
)

const (
	attachmentBlockStart   = "<!-- issue-flow:attachments"
	attachmentBlockEnd     = "-->"
	maxExternalAttachments = 20
)

type attachmentManifest struct {
	Version     int                 `json:"version"`
	Attachments []domain.Attachment `json:"attachments"`
}

func ExternalAttachments(issue domain.Issue) ([]domain.Attachment, []string) {
	texts := []string{issue.Body}
	for _, comment := range issue.Comments {
		texts = append(texts, comment.Body)
	}
	var result []domain.Attachment
	var warnings []string
	seen := make(map[string]bool)
	for _, body := range texts {
		for {
			start := strings.Index(body, attachmentBlockStart)
			if start < 0 {
				break
			}
			body = body[start+len(attachmentBlockStart):]
			end := strings.Index(body, attachmentBlockEnd)
			if end < 0 {
				warnings = append(warnings, "ignored unterminated external attachment manifest")
				break
			}
			raw := strings.TrimSpace(body[:end])
			body = body[end+len(attachmentBlockEnd):]
			var manifest attachmentManifest
			if err := json.Unmarshal([]byte(raw), &manifest); err != nil || manifest.Version != 1 {
				warnings = append(warnings, "ignored invalid external attachment manifest")
				continue
			}
			for _, attachment := range manifest.Attachments {
				if len(result) >= maxExternalAttachments {
					warnings = append(warnings, fmt.Sprintf("external attachments truncated at %d entries", maxExternalAttachments))
					return result, warnings
				}
				if err := validateExternalAttachment(attachment); err != nil {
					warnings = append(warnings, "ignored unsafe external attachment: "+err.Error())
					continue
				}
				key := attachment.Source + "\x00" + attachment.DownloadRef
				if !seen[key] {
					seen[key] = true
					result = append(result, attachment)
				}
			}
		}
	}
	return result, warnings
}

func validateExternalAttachment(value domain.Attachment) error {
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.Source) == "" || strings.TrimSpace(value.DownloadRef) == "" {
		return fmt.Errorf("id, name, source, and downloadRef are required")
	}
	if len(value.ID) > 128 || len(value.Name) > 255 || len(value.Source) > 64 || len(value.DownloadRef) > 512 || value.Size < 0 {
		return fmt.Errorf("field length or size is out of bounds")
	}
	if strings.ContainsAny(value.ID+value.Name+value.Source+value.DownloadRef+value.ContentType+value.Checksum, "\r\n\x00") {
		return fmt.Errorf("control characters are not allowed")
	}
	separator := strings.IndexByte(value.DownloadRef, ':')
	if separator < 1 || !safeReferenceScheme(value.DownloadRef[:separator]) {
		return fmt.Errorf("downloadRef must use a safe resolver scheme")
	}
	if value.Checksum != "" && !validSHA256(value.Checksum) {
		return fmt.Errorf("checksum must be sha256 followed by 64 hexadecimal characters")
	}
	if value.URL != "" {
		parsed, err := url.Parse(value.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("url must be absolute HTTPS without embedded credentials")
		}
	}
	return nil
}

func safeReferenceScheme(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}
