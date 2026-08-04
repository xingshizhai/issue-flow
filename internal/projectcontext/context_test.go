package projectcontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
)

func TestBuildContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Project.InstructionFiles = []string{"AGENTS.md", "MISSING.md"}
	cfg.Automation.Level = "patch"
	cfg.Git.AllowCommit = true
	cfg.Validation.Commands = []config.ValidationCommand{{
		Argv: []string{"go", "test", "./..."}, Timeout: "5m",
	}}
	result, err := Build(domain.Issue{
		Number: "IABC1", Title: "Fix: unsafe / input", WorkflowState: domain.StateReady,
		Labels: []domain.Label{{Name: "type-bug"}},
	}, cfg, root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Git.Branch != "bug/issue-iabc1-fix-unsafe-input" {
		t.Fatalf("branch = %q", result.Git.Branch)
	}
	if result.Git.AllowCommit {
		t.Fatal("patch automation level must restrict allow_commit")
	}
	if !result.InstructionFiles[0].Exists || result.InstructionFiles[1].Exists {
		t.Fatalf("instructions = %+v", result.InstructionFiles)
	}
	if len(result.Validation) != 1 || result.Validation[0].Argv[0] != "go" {
		t.Fatalf("validation = %+v", result.Validation)
	}
}

func TestBuildRejectsInstructionTraversal(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Project.InstructionFiles = []string{"../outside.md"}
	_, err := Build(domain.Issue{Number: "1", Title: "test"}, cfg, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "escapes project root") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildRejectsSymlinkOutsideRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Project.InstructionFiles = []string{"AGENTS.md"}
	_, err := Build(domain.Issue{Number: "1", Title: "test"}, cfg, root)
	if err == nil || !strings.Contains(err.Error(), "outside project root") {
		t.Fatalf("error = %v", err)
	}
}

func TestBranchNameFallsBackForNonASCII(t *testing.T) {
	t.Parallel()
	branch, err := BranchName("{type}/issue-{number}-{slug}", domain.Issue{
		Number: "I中文1", Title: "修复登录问题",
	})
	if err != nil {
		t.Fatal(err)
	}
	if branch != "task/issue-i-1-task" {
		t.Fatalf("branch = %q", branch)
	}
}

func TestBranchNameRejectsUnknownPlaceholder(t *testing.T) {
	t.Parallel()
	_, err := BranchName("{unknown}/{number}", domain.Issue{Number: "1", Title: "test"})
	if err == nil {
		t.Fatal("unknown placeholder should fail")
	}
}
