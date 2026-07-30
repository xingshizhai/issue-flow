package adapterdocs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentAdaptersRetainSafetyContract(t *testing.T) {
	t.Parallel()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	files := []string{
		"skills/issue-flow/SKILL.md",
		"adapters/generic/agent-workflow.md",
		"adapters/claude/CLAUDE.md",
		"adapters/cursor/issue-flow.mdc",
		"adapters/vscode/issue-flow.instructions.md",
	}
	required := []string{
		"doctor", "context", "claim", "start", "heartbeat", "progress",
		"finish", "block", "release", "untrusted", "lease-token",
	}

	for _, name := range files {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			content := strings.ToLower(string(raw))
			for _, phrase := range required {
				if !strings.Contains(content, phrase) {
					t.Errorf("missing required workflow term %q", phrase)
				}
			}
		})
	}
}
