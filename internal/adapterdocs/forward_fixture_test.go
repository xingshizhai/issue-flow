package adapterdocs

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"issue-flow/internal/config"
	"issue-flow/internal/domain"
	"issue-flow/internal/provider"
	"issue-flow/internal/provider/fake"
)

func TestSkillForwardFixtureStartsReadyAndFailing(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	fixture := filepath.Join(root, "testdata", "skill-forward")
	cfg, err := config.Load(filepath.Join(fixture, config.DefaultName))
	if err != nil {
		t.Fatal(err)
	}
	store := fake.New(fake.ResolvePath(filepath.Join(fixture, config.DefaultName), cfg.Provider.DataFile))
	page, err := store.ListIssues(context.Background(), provider.ListQuery{
		State: domain.StateReady,
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Number != "1" {
		t.Fatalf("ready issues = %+v", page.Items)
	}

	command := exec.Command("go", "test", "./...")
	command.Dir = fixture
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("forward fixture must initially fail validation")
	}
	if len(output) == 0 {
		t.Fatal("failing validation produced no diagnostic")
	}
}
