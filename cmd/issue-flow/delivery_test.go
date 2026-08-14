package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/xingshizhai/issue-flow/internal/config"
)

func TestCollectDeliveryEvidenceRequiresCurrentCommitAndReport(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "-c", "user.name=Issue Flow Test", "-c", "user.email=issue-flow@example.invalid", "commit", "-m", "test")
	head := runGit(t, root, "rev-parse", "HEAD")
	report := filepath.Join(t.TempDir(), "validation.json")
	if err := os.WriteFile(report, []byte(`{"commands":[{"command":"go test ./...","status":"passed"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Git.AllowCommit = true
	cfg.Git.RequireCommit = true
	cfg.Validation.RequireReport = true
	cfg.Validation.Commands = []config.ValidationCommand{{Argv: []string{"go", "test", "./..."}}}

	evidence, err := collectDeliveryEvidence(context.Background(), root, head, report, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Commit != head || evidence.WorktreeClean == nil || !*evidence.WorktreeClean || len(evidence.Validation) != 1 {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return stringTrimSpace(string(output))
}

func stringTrimSpace(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r' || value[len(value)-1] == ' ') {
		value = value[:len(value)-1]
	}
	return value
}
