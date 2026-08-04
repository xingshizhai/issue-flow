package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/output"
	"github.com/xingshizhai/issue-flow/internal/provider"
	"github.com/xingshizhai/issue-flow/internal/workflow"
)

func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("ISSUE_FLOW_TEST_HELPER") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		os.Exit(99)
	}
	code := (&cli{stdout: os.Stdout, stderr: os.Stderr}).run(context.Background(), os.Args[separator+1:])
	os.Exit(code)
}

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

func TestVersion(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := invoke("version")
	if code != 0 || stderr != "" {
		t.Fatalf("version code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "0.1.0-dev" {
		t.Fatalf("version output = %q", stdout)
	}
}

func TestWriteCommandAcceptsValidatedOperationID(t *testing.T) {
	t.Parallel()

	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "operation", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateReady, Version: "1", CreatedAt: time.Now().UTC(),
	})
	code, stdout, stderr := invoke(
		"claim", "1", "--agent", "agent-a", "--operation-id", "op_external.123",
		"--dry-run", "--project", project, "--json",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		OperationID string `json:"operationId"`
		Data        struct {
			Issue domain.Issue `json:"issue"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OperationID != "op_external.123" ||
		len(envelope.Data.Issue.Events) != 1 ||
		envelope.Data.Issue.Events[0].OperationID != "op_external.123" {
		t.Fatalf("envelope = %+v", envelope)
	}

	code, stdout, stderr = invoke(
		"claim", "1", "--agent", "agent-a", "--operation-id", "unsafe id",
		"--dry-run", "--project", project, "--json",
	)
	if code != 2 || stderr != "" {
		t.Fatalf("invalid ID code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, false, "INVALID_ARGUMENT")
}

func TestProviderErrorMappingAndRetryability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		code      string
		exitCode  int
		retryable bool
	}{
		{"authentication", provider.ErrAuthentication, "AUTHENTICATION_FAILED", 3, false},
		{"permission", provider.ErrPermission, "PERMISSION_DENIED", 3, false},
		{"not found", provider.ErrNotFound, "NOT_FOUND", 4, false},
		{"misconfigured", provider.ErrMisconfigured, "CONFIG_ERROR", 2, false},
		{"rate limited", provider.ErrRateLimited, "RATE_LIMITED", 6, true},
		{"unsupported", provider.ErrUnsupported, "UNSUPPORTED_CAPABILITY", 6, false},
		{"unavailable", provider.ErrUnavailable, "PROVIDER_UNAVAILABLE", 6, true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			command := &cli{stdout: &stdout, stderr: &stderr}
			exitCode := command.providerFailureWithID("json", "op_test", test.err)
			if exitCode != test.exitCode || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q", exitCode, stderr.String())
			}
			var envelope output.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.OperationID != "op_test" || envelope.Error == nil ||
				envelope.Error.Code != test.code ||
				envelope.Error.Retryable != test.retryable {
				t.Fatalf("envelope = %+v", envelope)
			}
		})
	}
}

func TestWorkflowProviderErrorPreservesOperationID(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	command := &cli{stdout: &stdout, stderr: &stderr}
	exitCode := command.workflowFailure("json", "op_write", provider.ErrPermission)
	if exitCode != 3 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", exitCode, stderr.String())
	}
	var envelope output.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OperationID != "op_write" || envelope.Error == nil ||
		envelope.Error.Code != "PERMISSION_DENIED" || envelope.Error.Retryable {
		t.Fatalf("envelope = %+v", envelope)
	}
}

func TestVerboseReportsSafeMetadataOnStderr(t *testing.T) {
	t.Parallel()

	project := seededProject(t)
	code, stdout, stderr := invoke("doctor", "--project", project, "--format", "json", "--verbose")
	if code != 0 {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, true, "")
	for _, expected := range []string{
		"issue-flow: debug:",
		"provider=fake",
		"transport=local",
		"credential=none",
		"read=true",
		"write=true",
	} {
		if !strings.Contains(stderr, expected) {
			t.Errorf("stderr %q does not contain %q", stderr, expected)
		}
	}
	for _, forbidden := range []string{"lease-token", "access_token", "tokenHash"} {
		if strings.Contains(stderr, forbidden) {
			t.Errorf("stderr contains sensitive field name %q: %q", forbidden, stderr)
		}
	}
}

func TestLeaseWorkflowAndAuthorization(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "fix bug", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateReady, Version: "1", CreatedAt: time.Now().UTC(),
	})
	code, stdout, stderr := invoke("claim", "1", "--agent", "codex-a", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("claim code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	token := leaseTokenFromEnvelope(t, stdout)
	if token == "" {
		t.Fatal("claim did not return a lease token")
	}
	if strings.Contains(stdout, "tokenHash") {
		t.Fatal("claim output leaked the stored token hash")
	}

	code, stdout, stderr = invoke("start", "1", "--agent", "codex-a", "--lease-token", "wrong", "--project", project, "--json")
	if code != 5 || stderr != "" {
		t.Fatalf("unauthorized start code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, false, "LEASE_CONFLICT")

	code, stdout, stderr = invoke("start", "1", "--agent", "codex-a", "--lease-token", token, "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("start code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = invoke("heartbeat", "1", "--agent", "codex-a", "--lease-token", token, "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("heartbeat code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = invoke("release", "1", "--agent", "codex-a", "--lease-token", token, "--reason", "handoff", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("release code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data struct {
			Issue domain.Issue `json:"issue"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Issue.WorkflowState != domain.StateReady || envelope.Data.Issue.Lease != nil {
		t.Fatalf("release result = %+v", envelope.Data.Issue)
	}
}

func TestNonHolderCannotUseLeaseCommands(t *testing.T) {
	t.Parallel()

	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "protected", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateReady, Version: "1", CreatedAt: time.Now().UTC(),
	})
	code, stdout, stderr := invoke("claim", "1", "--agent", "holder", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("claim code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	summary := filepath.Join(project, "summary.md")
	if err := os.WriteFile(summary, []byte("safe summary"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := [][]string{
		{"heartbeat", "1", "--agent", "intruder", "--lease-token", "wrong"},
		{"progress", "1", "--agent", "intruder", "--lease-token", "wrong", "--message", "unauthorized"},
		{"block", "1", "--agent", "intruder", "--lease-token", "wrong", "--reason", "unauthorized"},
		{"release", "1", "--agent", "intruder", "--lease-token", "wrong", "--reason", "unauthorized"},
		{"finish", "1", "--agent", "intruder", "--lease-token", "wrong", "--summary-file", summary},
	}
	for _, args := range tests {
		args = append(args, "--project", project, "--json")
		code, stdout, stderr = invoke(args...)
		if code != 5 || stderr != "" {
			t.Errorf("%s code=%d stdout=%q stderr=%q", args[0], code, stdout, stderr)
			continue
		}
		assertEnvelope(t, stdout, false, "LEASE_CONFLICT")
	}
}

func TestReclaimExpiredLease(t *testing.T) {
	t.Parallel()
	expiredToken := "expired-secret"
	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "expired", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateWorking, Version: "1", CreatedAt: time.Now().UTC(),
		Lease: &domain.Lease{
			ID: "lease_expired", AgentID: "old-agent",
			TokenHash: domain.HashLeaseToken(expiredToken),
			ClaimedAt: time.Now().Add(-3 * time.Hour),
			ExpiresAt: time.Now().Add(-time.Hour),
		},
	})
	code, stdout, stderr := invoke("reclaim", "1", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("reclaim code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data struct {
			Issue domain.Issue `json:"issue"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Issue.WorkflowState != domain.StateReady || envelope.Data.Issue.Lease != nil {
		t.Fatalf("reclaim result = %+v", envelope.Data.Issue)
	}
}

func TestClaimDryRunDoesNotMutate(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "dry run", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateReady, Version: "1", CreatedAt: time.Now().UTC(),
	})
	code, stdout, stderr := invoke("claim", "1", "--agent", "codex-a", "--dry-run", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("dry-run claim code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = invoke("show", "1", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("show code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data domain.Issue `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.WorkflowState != domain.StateReady || envelope.Data.Lease != nil {
		t.Fatalf("dry-run mutated issue: %+v", envelope.Data)
	}
}

func TestEveryLeaseWriteCommandDryRunDoesNotMutate(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	const token = "dry-run-secret"
	activeLease := &domain.Lease{
		ID: "lease_dry_run", AgentID: "codex-a", TokenHash: domain.HashLeaseToken(token),
		ClaimedAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Hour),
	}
	expiredLease := *activeLease
	expiredLease.ExpiresAt = now.Add(-time.Minute)
	tests := []struct {
		name  string
		issue domain.Issue
		args  []string
	}{
		{
			name:  "claim",
			issue: domain.Issue{WorkflowState: domain.StateReady},
			args:  []string{"claim", "1", "--agent", "codex-a"},
		},
		{
			name:  "start",
			issue: domain.Issue{WorkflowState: domain.StateClaimed, Lease: activeLease},
			args:  []string{"start", "1", "--agent", "codex-a", "--lease-token", token},
		},
		{
			name:  "heartbeat",
			issue: domain.Issue{WorkflowState: domain.StateWorking, Lease: activeLease},
			args:  []string{"heartbeat", "1", "--agent", "codex-a", "--lease-token", token},
		},
		{
			name:  "progress",
			issue: domain.Issue{WorkflowState: domain.StateWorking, Lease: activeLease},
			args:  []string{"progress", "1", "--agent", "codex-a", "--lease-token", token, "--message", "dry run"},
		},
		{
			name:  "block",
			issue: domain.Issue{WorkflowState: domain.StateWorking, Lease: activeLease},
			args:  []string{"block", "1", "--agent", "codex-a", "--lease-token", token, "--reason", "dry run"},
		},
		{
			name:  "release",
			issue: domain.Issue{WorkflowState: domain.StateWorking, Lease: activeLease},
			args:  []string{"release", "1", "--agent", "codex-a", "--lease-token", token, "--reason", "dry run"},
		},
		{
			name:  "reclaim",
			issue: domain.Issue{WorkflowState: domain.StateWorking, Lease: &expiredLease},
			args:  []string{"reclaim", "1"},
		},
		{
			name:  "finish",
			issue: domain.Issue{WorkflowState: domain.StateWorking, Lease: activeLease},
			args:  []string{"finish", "1", "--agent", "codex-a", "--lease-token", token},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.issue.ID = "1"
			test.issue.Number = "1"
			test.issue.Title = test.name
			test.issue.ProviderState = domain.ProviderStateOpen
			test.issue.Version = "1"
			test.issue.CreatedAt = now
			project := seededProject(t, test.issue)
			args := append([]string(nil), test.args...)
			if test.name == "finish" {
				summary := filepath.Join(project, "summary.md")
				if err := os.WriteFile(summary, []byte("dry-run summary"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--summary-file", summary)
			}
			storePath := filepath.Join(project, "issues.json")
			before, err := os.ReadFile(storePath)
			if err != nil {
				t.Fatal(err)
			}
			args = append(args, "--dry-run", "--project", project, "--json")
			code, stdout, stderr := invoke(args...)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			after, err := os.ReadFile(storePath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("dry-run mutated Fake Provider storage")
			}
		})
	}
}

func TestProgressBlockAndFinishWorkflow(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "deliver", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateReady, Version: "1", CreatedAt: time.Now().UTC(),
	})
	code, stdout, stderr := invoke("claim", "1", "--agent", "codex-a", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("claim code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	token := leaseTokenFromEnvelope(t, stdout)
	for _, command := range [][]string{
		{"start", "1", "--agent", "codex-a", "--lease-token", token},
		{"progress", "1", "--agent", "codex-a", "--lease-token", token, "--message", "tests are passing"},
		{"block", "1", "--agent", "codex-a", "--lease-token", token, "--reason", "waiting for review input"},
	} {
		args := append(command, "--project", project, "--json")
		code, stdout, stderr = invoke(args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%s code=%d stdout=%q stderr=%q", command[0], code, stdout, stderr)
		}
	}
	summaryPath := filepath.Join(project, "result.md")
	if err := os.WriteFile(summaryPath, []byte("# Result\n\nAll checks passed."), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = invoke(
		"finish", "1", "--agent", "codex-a", "--lease-token", token,
		"--summary-file", summaryPath, "--project", project, "--json",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("finish code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data struct {
			Issue domain.Issue `json:"issue"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	issue := envelope.Data.Issue
	if issue.WorkflowState != domain.StateReview || issue.Lease != nil {
		t.Fatalf("finish result = %+v", issue)
	}
	if got := issue.Events[len(issue.Events)-1]; got.Operation != "finish" ||
		!strings.Contains(got.Message, "All checks passed") {
		t.Fatalf("finish event = %+v", got)
	}
	conclusionPath := filepath.Join(project, "review.md")
	if err := os.WriteFile(conclusionPath, []byte("Reviewed and accepted."), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = invoke(
		"complete", "1", "--reviewer", "reviewer-a",
		"--conclusion-file", conclusionPath, "--operation-id", "op_review_complete",
		"--project", project, "--json",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("complete code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	issue = envelope.Data.Issue
	if issue.WorkflowState != domain.StateDone || issue.Lease != nil {
		t.Fatalf("complete result = %+v", issue)
	}
	if got := issue.Events[len(issue.Events)-1]; got.Operation != "complete" ||
		got.AgentID != "reviewer-a" || !strings.Contains(got.Message, "accepted") {
		t.Fatalf("complete event = %+v", got)
	}
	code, stdout, stderr = invoke(
		"complete", "1", "--reviewer", "reviewer-a",
		"--conclusion-file", conclusionPath, "--operation-id", "op_review_complete",
		"--project", project, "--json",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("complete replay code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestIssueOutputAndProgressWritesAreRedacted(t *testing.T) {
	t.Parallel()

	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "token=title-secret",
		Body: "password=body-secret", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateReady, Version: "1", CreatedAt: time.Now().UTC(),
	})
	code, stdout, stderr := invoke("show", "1", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("show code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, secret := range []string{"title-secret", "body-secret"} {
		if strings.Contains(stdout, secret) {
			t.Errorf("show output leaked %q: %s", secret, stdout)
		}
	}

	code, stdout, stderr = invoke("claim", "1", "--agent", "codex-a", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("claim code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	token := leaseTokenFromEnvelope(t, stdout)
	code, stdout, stderr = invoke(
		"progress", "1", "--agent", "codex-a", "--lease-token", token,
		"--message", "api_key=write-secret", "--project", project, "--json",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("progress code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	rawStore, err := os.ReadFile(filepath.Join(project, "issues.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawStore), "write-secret") {
		t.Fatalf("progress persisted a secret: %s", rawStore)
	}
	if !strings.Contains(string(rawStore), "[REDACTED]") {
		t.Fatalf("progress did not persist a redaction marker: %s", rawStore)
	}
}

func TestFinishRejectsSymlinkSummary(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "deliver", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateReady, Version: "1", CreatedAt: time.Now().UTC(),
	})
	target := filepath.Join(project, "target.md")
	link := filepath.Join(project, "result.md")
	if err := os.WriteFile(target, []byte("summary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(
		"finish", "1", "--agent", "codex-a", "--lease-token", "unused",
		"--summary-file", link, "--project", project, "--json",
	)
	if code != 2 || stderr != "" {
		t.Fatalf("finish code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, false, "INVALID_ARGUMENT")
}

func TestReadSummaryFileEnforcesTypeAndSize(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	if _, err := readSummaryFile(project); err == nil ||
		!errors.Is(err, workflow.ErrInvalidInput) {
		t.Fatalf("directory error = %v", err)
	}
	oversized := filepath.Join(project, "oversized.md")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), (64<<10)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSummaryFile(oversized); err == nil ||
		!errors.Is(err, workflow.ErrInvalidInput) ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized error = %v", err)
	}
	allowed := filepath.Join(project, "allowed.md")
	content := bytes.Repeat([]byte("a"), 64<<10)
	if err := os.WriteFile(allowed, content, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := readSummaryFile(allowed)
	if err != nil {
		t.Fatal(err)
	}
	if result != string(content) {
		t.Fatalf("summary length = %d", len(result))
	}
}

func TestProgressRequiresMessage(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "progress", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateReady, Version: "1", CreatedAt: time.Now().UTC(),
	})
	code, stdout, _ := invoke("claim", "1", "--agent", "codex-a", "--project", project, "--json")
	if code != 0 {
		t.Fatal(stdout)
	}
	token := leaseTokenFromEnvelope(t, stdout)
	code, stdout, stderr := invoke(
		"progress", "1", "--agent", "codex-a", "--lease-token", token,
		"--project", project, "--json",
	)
	if code != 2 || stderr != "" {
		t.Fatalf("progress code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertEnvelope(t, stdout, false, "INVALID_ARGUMENT")
}

func TestTwoCLIProcessesCannotBothClaim(t *testing.T) {
	project := seededProject(t, domain.Issue{
		ID: "1", Number: "1", Title: "process race", ProviderState: domain.ProviderStateOpen,
		WorkflowState: domain.StateReady, Version: "1", CreatedAt: time.Now().UTC(),
	})
	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, 2)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0],
			"-test.run=^TestCLIHelperProcess$", "--",
			"claim", "1", "--agent", "agent-"+string(rune('a'+i)),
			"--project", project, "--json",
		)
		commands[i].Env = append(os.Environ(), "ISSUE_FLOW_TEST_HELPER=1")
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &outputs[i]
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	successes := 0
	conflicts := 0
	for i, command := range commands {
		err := command.Wait()
		switch {
		case err == nil:
			successes++
		case command.ProcessState.ExitCode() == 5:
			conflicts++
		default:
			t.Fatalf("process %d error=%v output=%q", i, err, outputs[i].String())
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d outputs=%q", successes, conflicts, outputs)
	}
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
  ready_label: agent-ready
  review_label: agent-review
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

func TestContextJSONIncludesSafeProjectPolicy(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		ID: "IABC1", Number: "IABC1", Title: "Fix unsafe input",
		ProviderState: domain.ProviderStateOpen, WorkflowState: domain.StateReady,
		Version: "1", CreatedAt: time.Now().UTC(),
		Labels: []domain.Label{{Name: "type-bug"}},
	})
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("project instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke("context", "IABC1", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("context code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data struct {
			AutomationLevel  string `json:"automationLevel"`
			InstructionFiles []struct {
				Path   string `json:"path"`
				Exists bool   `json:"exists"`
			} `json:"instructionFiles"`
			Git struct {
				Branch string `json:"branch"`
			} `json:"git"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.AutomationLevel != "patch" ||
		envelope.Data.Git.Branch != "bug/issue-iabc1-fix-unsafe-input" ||
		len(envelope.Data.InstructionFiles) == 0 || !envelope.Data.InstructionFiles[0].Exists {
		t.Fatalf("context data = %+v", envelope.Data)
	}
	if strings.Contains(stdout, "tokenHash") {
		t.Fatal("context leaked token hash")
	}
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

func TestCreatePersistsReadyTypedIssue(t *testing.T) {
	t.Parallel()
	project := seededProject(t)
	bodyPath := filepath.Join(project, "body.md")
	if err := os.WriteFile(bodyPath, []byte("acceptance criteria"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(
		"create", "--type", "feature", "--title", "Add feature",
		"--body-file", bodyPath, "--project", project, "--json",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("create code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data domain.Issue `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Number != "1" || envelope.Data.WorkflowState != domain.StateReady ||
		envelope.Data.Body != "acceptance criteria" {
		t.Fatalf("created issue = %+v", envelope.Data)
	}
	if len(envelope.Data.Labels) != 2 ||
		envelope.Data.Labels[0].Name != "type-feature" ||
		envelope.Data.Labels[1].Name != "agent-ready" {
		t.Fatalf("labels = %+v", envelope.Data.Labels)
	}
}

func TestCreateMergesCustomLabelsAndDedupes(t *testing.T) {
	t.Parallel()
	project := seededProject(t)
	bodyPath := filepath.Join(project, "body.md")
	if err := os.WriteFile(bodyPath, []byte("acceptance criteria"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(
		"create", "--type", "bug", "--title", "Add feature",
		"--body-file", bodyPath, "--label", "priority:high", "--label", "type-bug",
		"--project", project, "--json",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("create code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data domain.Issue `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, label := range envelope.Data.Labels {
		names = append(names, label.Name)
	}
	want := []string{"type-bug", "agent-ready", "priority:high"}
	if len(names) != len(want) {
		t.Fatalf("labels = %v, want %v", names, want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("labels = %v, want %v", names, want)
		}
	}
}

func TestListUnmanagedFiltersToNoWorkflowLabel(t *testing.T) {
	t.Parallel()
	project := seededProject(t,
		domain.Issue{Number: "1", Title: "unmanaged one", Version: "1"},
		domain.Issue{Number: "2", Title: "ready", WorkflowState: domain.StateReady, Version: "1"},
		domain.Issue{Number: "3", Title: "unmanaged two", Version: "1"},
	)
	code, stdout, stderr := invoke("list", "--unmanaged", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data struct {
			Items []domain.Issue `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 2 {
		t.Fatalf("items = %+v, want 2 unmanaged issues", envelope.Data.Items)
	}
	for _, issue := range envelope.Data.Items {
		if issue.WorkflowState != "" {
			t.Fatalf("issue %s has workflow state %q, want unmanaged", issue.Number, issue.WorkflowState)
		}
	}
}

func TestListUnmanagedRejectsCombinationWithReady(t *testing.T) {
	t.Parallel()
	project := seededProject(t)
	code, stdout, _ := invoke("list", "--unmanaged", "--ready", "--project", project, "--json")
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	assertEnvelope(t, stdout, false, "INVALID_ARGUMENT")
}

func TestAdoptSetsReadyAndRejectsAlreadyManaged(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		Number: "1", Title: "unmanaged", Version: "1",
	}, domain.Issue{
		Number: "2", Title: "already ready", WorkflowState: domain.StateReady, Version: "1",
	})

	code, stdout, stderr := invoke("adopt", "1", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("adopt code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data domain.Issue `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.WorkflowState != domain.StateReady {
		t.Fatalf("workflowState = %s, want ready", envelope.Data.WorkflowState)
	}

	code, stdout, _ = invoke("adopt", "2", "--project", project, "--json")
	if code != 5 {
		t.Fatalf("code = %d, want 5 (STATE_CONFLICT)", code)
	}
	assertEnvelope(t, stdout, false, "STATE_CONFLICT")
}

func TestLeaseTokenFileWorksLikeInlineToken(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		Number: "1", Title: "fix bug", WorkflowState: domain.StateReady, Version: "1",
	})
	_, claimStdout, _ := invoke("claim", "1", "--agent", "codex-a", "--project", project, "--json")
	token := leaseTokenFromEnvelope(t, claimStdout)
	if token == "" {
		t.Fatal("claim did not return a lease token")
	}

	tokenPath := filepath.Join(t.TempDir(), "lease-token")
	// Trailing newline on purpose — mirrors `echo "$token" > file`, which the
	// reader must trim.
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := invoke("start", "1", "--agent", "codex-a", "--lease-token-file", tokenPath, "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("start code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, token) {
		t.Fatal("start output should not echo the lease token back")
	}
}

func TestLeaseTokenAndLeaseTokenFileAreMutuallyExclusive(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		Number: "1", Title: "fix bug", WorkflowState: domain.StateReady, Version: "1",
	})
	tokenPath := filepath.Join(t.TempDir(), "lease-token")
	if err := os.WriteFile(tokenPath, []byte("whatever"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := invoke("start", "1", "--agent", "codex-a",
		"--lease-token", "inline", "--lease-token-file", tokenPath, "--project", project, "--json")
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	assertEnvelope(t, stdout, false, "INVALID_ARGUMENT")
}

func TestLeaseTokenFileRejectsSymlink(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		Number: "1", Title: "fix bug", WorkflowState: domain.StateReady, Version: "1",
	})
	root := t.TempDir()
	target := filepath.Join(root, "real-token")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "lease-token-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	code, stdout, _ := invoke("start", "1", "--agent", "codex-a", "--lease-token-file", link, "--project", project, "--json")
	if code != 2 || !strings.Contains(stdout, "symlink") {
		t.Fatalf("code=%d stdout=%q, want a symlink rejection", code, stdout)
	}
}

func TestCommentAcceptsBodyFileAndRejectsBothFlags(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		Number: "1", Title: "existing", WorkflowState: domain.StateReady, Version: "1",
	})
	bodyPath := filepath.Join(project, "comment-body.md")
	if err := os.WriteFile(bodyPath, []byte("## multi-line\n\nvia --body-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke("comment", "1", "--body-file", bodyPath, "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("comment code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data domain.Comment `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Body != "## multi-line\n\nvia --body-file" {
		t.Fatalf("comment = %+v", envelope.Data)
	}

	code, stdout, _ = invoke("comment", "1", "--body", "inline", "--body-file", bodyPath, "--project", project, "--json")
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	assertEnvelope(t, stdout, false, "INVALID_ARGUMENT")
}

func TestCommentAddsPlainTextWithoutLease(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		Number: "1", Title: "existing", WorkflowState: domain.StateReady, Version: "1",
	})
	code, stdout, stderr := invoke("comment", "1", "--body", "checking in", "--project", project, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("comment code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data domain.Comment `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Body != "checking in" {
		t.Fatalf("comment = %+v", envelope.Data)
	}
	_, showStdout, _ := invoke("show", "1", "--project", project, "--json")
	var showEnvelope struct {
		Data domain.Issue `json:"data"`
	}
	if err := json.Unmarshal([]byte(showStdout), &showEnvelope); err != nil {
		t.Fatal(err)
	}
	if len(showEnvelope.Data.Comments) != 1 || showEnvelope.Data.Comments[0].Body != "checking in" {
		t.Fatalf("show comments = %+v", showEnvelope.Data.Comments)
	}
}

func TestCommentRequiresBody(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{Number: "1", WorkflowState: domain.StateReady})
	code, stdout, _ := invoke("comment", "1", "--project", project, "--json")
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	assertEnvelope(t, stdout, false, "INVALID_ARGUMENT")
}

func TestMissingLabelWarningsReportsOnlyAbsentLabels(t *testing.T) {
	t.Parallel()
	got := missingLabelWarnings(
		[]string{"type-bug", "agent-ready", "priority:high"},
		[]domain.Label{{Name: "type-bug"}, {Name: "agent-ready"}},
	)
	if len(got) != 1 || !strings.Contains(got[0], `"priority:high"`) {
		t.Fatalf("warnings = %v", got)
	}
	if none := missingLabelWarnings(
		[]string{"type-bug", "agent-ready"},
		[]domain.Label{{Name: "type-bug"}, {Name: "agent-ready"}},
	); len(none) != 0 {
		t.Fatalf("expected no warnings when every label is present, got %v", none)
	}
}

func TestCreateReturnsNoWarningsWhenAllLabelsLand(t *testing.T) {
	t.Parallel()
	project := seededProject(t)
	bodyPath := filepath.Join(project, "body.md")
	if err := os.WriteFile(bodyPath, []byte("details"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(
		"create", "--type", "bug", "--title", "t",
		"--body-file", bodyPath, "--label", "priority:high", "--project", project, "--json",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("create code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	// Fake provider attaches every requested label verbatim (unlike real
	// Gitee, which can silently drop ones that don't exist on the repo), so
	// this exercises the happy path: no false-positive warnings.
	if len(envelope.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", envelope.Warnings)
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

func seededProject(t *testing.T, issues ...domain.Issue) string {
	t.Helper()
	project := t.TempDir()
	configBody := []byte(`
version: 1
provider:
  type: fake
  data_file: issues.json
workflow:
  ready_label: agent-ready
  review_label: agent-review
  lease_minutes: 120
  auto_close: false
`)
	if err := os.WriteFile(filepath.Join(project, ".issue-flow.yaml"), configBody, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "issues": issues})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "issues.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return project
}

func leaseTokenFromEnvelope(t *testing.T, raw string) string {
	t.Helper()
	var envelope struct {
		Data struct {
			LeaseToken string `json:"leaseToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data.LeaseToken
}
