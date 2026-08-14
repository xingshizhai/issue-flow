package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteDefaultAndLoad(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), DefaultName)
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Type != "fake" || cfg.Workflow.LeaseMinutes != 120 || cfg.Workflow.FinishState != "review" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if err := WriteDefault(path); err == nil {
		t.Fatal("second init must refuse overwrite")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), DefaultName)
	raw := []byte("version: 1\nunknown_dangerous_option: true\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field unknown_dangerous_option not found") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestFindWalksParents(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	configPath := filepath.Join(root, DefaultName)
	if err := WriteDefault(configPath); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	found, err := Find("", child)
	if err != nil {
		t.Fatal(err)
	}
	if found != configPath {
		t.Fatalf("Find() = %q, want %q", found, configPath)
	}
}

func TestValidateStructuredCommandsAndGitPolicy(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Validation.Commands = []ValidationCommand{{Argv: []string{"go", "test"}, Timeout: "invalid"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "positive Go duration") {
		t.Fatalf("invalid timeout error = %v", err)
	}
	cfg = Default()
	cfg.Workflow.FinishState = "closed"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "finish_state") {
		t.Fatalf("finish state error = %v", err)
	}
	cfg = Default()
	cfg.Git.RequireCommit = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "require_commit") {
		t.Fatalf("required commit error = %v", err)
	}
	cfg = Default()
	cfg.Git.AllowPush = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires git.allow_commit") {
		t.Fatalf("unsafe git policy error = %v", err)
	}
	cfg = Default()
	cfg.Git.BranchPattern = "{unknown}/{number}"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unknown placeholder") {
		t.Fatalf("unknown placeholder error = %v", err)
	}
}

func TestValidateRejectsFakeDataOutsideConfigDirectory(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"../issues.json", "nested/issues.json", "/tmp/issues.json"} {
		cfg := Default()
		cfg.Provider.DataFile = path
		if err := cfg.Validate(); err == nil ||
			!strings.Contains(err.Error(), "inside the configuration directory") {
			t.Errorf("data_file %q error = %v", path, err)
		}
	}
}

func TestLoadRejectsSymlinkAndOversizedConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target.yaml")
	if err := WriteDefault(target); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Load(link); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("symlink error = %v", err)
	}

	oversized := filepath.Join(root, "oversized.yaml")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(oversized); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestWriteDefaultRefusesDanglingSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "outside.yaml")
	link := filepath.Join(root, DefaultName)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := WriteDefault(link); err == nil ||
		!strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("WriteDefault() error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dangling symlink target was created: %v", err)
	}
}

func TestValidateRestrictsGiteeCredentialEnvironment(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"PATH", "HOME", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN",
		"gitee_token", "GITEE_CREDENTIAL",
	} {
		cfg := Default()
		cfg.Provider.Type = "gitee"
		cfg.Provider.Owner = "owner"
		cfg.Provider.Repo = "repo"
		cfg.Provider.TokenEnv = name
		if err := cfg.Validate(); err == nil ||
			!strings.Contains(err.Error(), "GITEE_*TOKEN*") {
			t.Errorf("token_env %q error = %v", name, err)
		}
	}

	for _, name := range []string{"GITEE_TOKEN", "GITEE_API_TOKEN", "GITEE_OAUTH_ACCESS_TOKEN"} {
		cfg := Default()
		cfg.Provider.Type = "gitee"
		cfg.Provider.Owner = "owner"
		cfg.Provider.Repo = "repo"
		cfg.Provider.TokenEnv = name
		if err := cfg.Validate(); err != nil {
			t.Errorf("token_env %q error = %v", name, err)
		}
	}
}
