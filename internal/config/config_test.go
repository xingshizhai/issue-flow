package config

import (
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
	if cfg.Provider.Type != "fake" || cfg.Workflow.LeaseMinutes != 120 {
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
