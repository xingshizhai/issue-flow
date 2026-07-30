package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDoesNotEvaluateValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("GITEE_TOKEN='$(not-a-command)'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if values["GITEE_TOKEN"] != "$(not-a-command)" {
		t.Fatalf("unexpected value %q", values["GITEE_TOKEN"])
	}
}

func TestLoadRejectsUnsafeInput(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("GITEE_TOKEN=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestLoadRejectsInvalidKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("lower=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected invalid key error")
	}
}
