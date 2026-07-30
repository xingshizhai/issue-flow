package adapterdocs

import (
	"strings"
	"testing"
)

func TestReleaseConfigurationMatchesCanonicalModule(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	goMod := readRepositoryFile(t, root, "go.mod")
	if !strings.HasPrefix(goMod, "module github.com/xingshizhai/issue-flow\n") {
		t.Fatalf("unexpected module declaration: %q", strings.SplitN(goMod, "\n", 2)[0])
	}
	workflow := readRepositoryFile(t, root, ".github/workflows/release.yml")
	for _, required := range []string{
		`tags:`,
		`- "v*"`,
		`make check`,
		`make snapshot VERSION=`,
		`actions/attest@v4`,
		`gh release create`,
		`persist-credentials: false`,
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("release workflow is missing %q", required)
		}
	}
}
