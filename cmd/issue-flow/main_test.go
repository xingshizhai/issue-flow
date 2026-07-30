package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"issue-flow/internal/domain"
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

func TestContextJSONIncludesSafeProjectPolicy(t *testing.T) {
	t.Parallel()
	project := seededProject(t, domain.Issue{
		ID: "IABC1", Number: "IABC1", Title: "Fix unsafe input",
		ProviderState: domain.ProviderStateOpen, WorkflowState: domain.StateReady,
		Version: "1", CreatedAt: time.Now().UTC(),
		Labels: []domain.Label{{Name: "type:bug"}},
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
  ready_label: agent:ready
  review_label: agent:review
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
