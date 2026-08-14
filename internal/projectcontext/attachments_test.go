package projectcontext

import (
	"testing"

	"github.com/xingshizhai/issue-flow/internal/domain"
)

func TestExternalAttachmentsParsesSafeManifestAndRejectsCredentials(t *testing.T) {
	issue := domain.Issue{Body: `
<!-- issue-flow:attachments
{"version":1,"attachments":[
 {"id":"file-12","name":"screen.png","source":"liteerp","downloadRef":"liteerp-file:12","url":"https://erp.example.com/api/files/12/raw"},
 {"id":"bad","name":"bad","source":"liteerp","downloadRef":"bad","url":"https://user:secret@example.com/file"}
]}
-->
`}
	attachments, warnings := ExternalAttachments(issue)
	if len(attachments) != 1 || attachments[0].DownloadRef != "liteerp-file:12" {
		t.Fatalf("attachments = %+v", attachments)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v", warnings)
	}
}
