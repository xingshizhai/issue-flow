package adapterdocs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadmesDocumentCompleteFakeWorkflow(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	files := []string{"README.md", "README.zh-CN.md"}
	required := []string{
		"examples/fake-issues.example.json",
		"issue-flow init",
		"issue-flow doctor",
		"issue-flow list --ready",
		"issue-flow context",
		"issue-flow claim",
		"issue-flow start",
		"issue-flow progress",
		"issue-flow finish",
		"--lease-token",
		"--summary-file",
		"--dry-run",
		"review",
		"adapters/generic/agent-workflow.md",
	}

	for _, name := range files {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			content := strings.ToLower(readRepositoryFile(t, root, name))
			for _, phrase := range required {
				if !strings.Contains(content, strings.ToLower(phrase)) {
					t.Errorf("missing required documentation term %q", phrase)
				}
			}
		})
	}
}

func TestDocumentedCommandsExistInCLI(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	mainSource := readRepositoryFile(t, root, "cmd/issue-flow/main.go")
	for _, command := range []string{
		"init", "doctor", "list", "show", "context", "claim", "start",
		"heartbeat", "progress", "block", "release", "reclaim", "finish",
	} {
		usageLine := "\n  " + command
		if !strings.Contains(mainSource, usageLine) {
			t.Errorf("CLI usage does not contain documented command %q", command)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func readRepositoryFile(t *testing.T, root, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
