package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitDoctorAndListJSON(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	code, stdout, stderr := invoke("init", "--project", project, "--format", "json")
	if code != 0 || stderr != "" {
		t.Fatalf("init code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, true, "")

	code, stdout, stderr = invoke("doctor", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, true, "")

	code, stdout, stderr = invoke("list", "--ready", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, true, "")
}

func TestInitRefusesOverwrite(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	if code, _, _ := invoke("init", "--project", project); code != 0 {
		t.Fatal("initial init failed")
	}
	code, stdout, stderr := invoke("init", "--project", project, "--json")
	if code != 2 || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, false, "CONFIG_ERROR")
}

func TestShowMissingHasStableExitAndJSONError(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".issue-flow.yaml"), []byte(`
version: 1
provider:
  type: fake
  data_file: issues.json
workflow:
  ready_label: agent:ready
  review_label: agent:review
  lease_minutes: 120
  auto_close: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke("show", "99", "--project", project, "--json")
	if code != 4 || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, false, "NOT_FOUND")
}

func TestDryRunInitDoesNotWrite(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	code, stdout, stderr := invoke("init", "--project", project, "--dry-run", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, true, "")
	if _, err := os.Stat(filepath.Join(project, ".issue-flow.yaml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created config: %v", err)
	}
}

func invoke(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := (&cli{stdout: &stdout, stderr: &stderr}).run(context.Background(), args)
	return code, stdout.String(), stderr.String()
}

func assertEnvelope(t *testing.T, raw string, ok bool, errorCode string) {
	t.Helper()
	var envelope struct {
		SchemaVersion int `json:"schemaVersion"`
		OK            bool
		Error         *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("invalid JSON %q: %v", raw, err)
	}
	if envelope.SchemaVersion != 1 || envelope.OK != ok {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if errorCode != "" && (envelope.Error == nil || envelope.Error.Code != errorCode) {
		t.Fatalf("error = %+v, want %s", envelope.Error, errorCode)
	}
}
