package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	CurrentVersion = 1
	DefaultName    = ".issue-flow.yaml"
)

type Config struct {
	Version  int      `yaml:"version" json:"version"`
	Provider Provider `yaml:"provider" json:"provider"`
	Workflow Workflow `yaml:"workflow" json:"workflow"`
	Project  Project  `yaml:"project" json:"project"`
	Security Security `yaml:"security" json:"security"`
}

type Provider struct {
	Type     string `yaml:"type" json:"type"`
	Owner    string `yaml:"owner" json:"owner"`
	Repo     string `yaml:"repo" json:"repo"`
	TokenEnv string `yaml:"token_env" json:"tokenEnv"`
	DataFile string `yaml:"data_file,omitempty" json:"dataFile,omitempty"`
}

type Workflow struct {
	ReadyLabel   string `yaml:"ready_label" json:"readyLabel"`
	ReviewLabel  string `yaml:"review_label" json:"reviewLabel"`
	LeaseMinutes int    `yaml:"lease_minutes" json:"leaseMinutes"`
	AutoClose    bool   `yaml:"auto_close" json:"autoClose"`
}

type Project struct {
	InstructionFiles []string `yaml:"instruction_files" json:"instructionFiles"`
}

type Security struct {
	RedactKeys []string `yaml:"redact_keys" json:"redactKeys"`
}

func Default() Config {
	return Config{
		Version: CurrentVersion,
		Provider: Provider{
			Type:     "fake",
			DataFile: ".issue-flow-fake.json",
		},
		Workflow: Workflow{
			ReadyLabel:   "agent:ready",
			ReviewLabel:  "agent:review",
			LeaseMinutes: 120,
		},
		Project: Project{InstructionFiles: []string{"AGENTS.md", "CLAUDE.md"}},
		Security: Security{RedactKeys: []string{
			"password", "token", "cookie", "authorization", "api_key", "secret",
		}},
	}
}

func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	switch c.Provider.Type {
	case "fake":
		if strings.TrimSpace(c.Provider.DataFile) == "" {
			return errors.New("provider.data_file is required for fake provider")
		}
	case "gitee":
		if c.Provider.Owner == "" || c.Provider.Repo == "" || c.Provider.TokenEnv == "" {
			return errors.New("provider owner, repo, and token_env are required for gitee")
		}
	default:
		return fmt.Errorf("unsupported provider type %q", c.Provider.Type)
	}
	if c.Workflow.LeaseMinutes <= 0 {
		return errors.New("workflow.lease_minutes must be positive")
	}
	return nil
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

func Find(explicit, project string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	start := project
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(current, DefaultName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", fmt.Errorf("%s not found from %s", DefaultName, start)
}

func WriteDefault(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("refusing to overwrite existing %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := yaml.Marshal(Default())
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}
