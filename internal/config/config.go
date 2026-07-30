package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"issue-flow/internal/domain"
)

const (
	CurrentVersion = 1
	DefaultName    = ".issue-flow.yaml"
)

type Config struct {
	Version    int        `yaml:"version" json:"version"`
	Provider   Provider   `yaml:"provider" json:"provider"`
	Workflow   Workflow   `yaml:"workflow" json:"workflow"`
	Project    Project    `yaml:"project" json:"project"`
	Validation Validation `yaml:"validation" json:"validation"`
	Git        Git        `yaml:"git" json:"git"`
	Automation Automation `yaml:"automation" json:"automation"`
	Security   Security   `yaml:"security" json:"security"`
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
	ClaimedLabel string `yaml:"claimed_label" json:"claimedLabel"`
	WorkingLabel string `yaml:"working_label" json:"workingLabel"`
	BlockedLabel string `yaml:"blocked_label" json:"blockedLabel"`
	ReviewLabel  string `yaml:"review_label" json:"reviewLabel"`
	DoneLabel    string `yaml:"done_label" json:"doneLabel"`
	LeaseMinutes int    `yaml:"lease_minutes" json:"leaseMinutes"`
	AutoClose    bool   `yaml:"auto_close" json:"autoClose"`
}

type Project struct {
	InstructionFiles []string `yaml:"instruction_files" json:"instructionFiles"`
}

type Validation struct {
	Commands []ValidationCommand `yaml:"commands" json:"commands"`
}

type ValidationCommand struct {
	Argv    []string `yaml:"argv" json:"argv"`
	Timeout string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type Git struct {
	BranchPattern    string `yaml:"branch_pattern" json:"branchPattern"`
	AllowCommit      bool   `yaml:"allow_commit" json:"allowCommit"`
	AllowPush        bool   `yaml:"allow_push" json:"allowPush"`
	AllowPullRequest bool   `yaml:"allow_pull_request" json:"allowPullRequest"`
}

type Automation struct {
	Level string `yaml:"level" json:"level"`
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
			ClaimedLabel: "agent:claimed",
			WorkingLabel: "agent:working",
			BlockedLabel: "agent:blocked",
			ReviewLabel:  "agent:review",
			DoneLabel:    "agent:done",
			LeaseMinutes: 120,
		},
		Project:    Project{InstructionFiles: []string{"AGENTS.md", "CLAUDE.md"}},
		Validation: Validation{Commands: []ValidationCommand{}},
		Git:        Git{BranchPattern: "{type}/issue-{number}-{slug}"},
		Automation: Automation{Level: "patch"},
		Security: Security{RedactKeys: []string{
			"password", "token", "cookie", "authorization", "api_key", "secret",
		}},
	}
}

func (w Workflow) LabelFor(state domain.WorkflowState) string {
	switch state {
	case domain.StateReady:
		return w.ReadyLabel
	case domain.StateClaimed:
		return w.ClaimedLabel
	case domain.StateWorking:
		return w.WorkingLabel
	case domain.StateBlocked:
		return w.BlockedLabel
	case domain.StateReview:
		return w.ReviewLabel
	case domain.StateDone:
		return w.DoneLabel
	default:
		return ""
	}
}

func (w Workflow) StateForLabel(label string) domain.WorkflowState {
	for _, state := range []domain.WorkflowState{
		domain.StateReady, domain.StateClaimed, domain.StateWorking,
		domain.StateBlocked, domain.StateReview, domain.StateDone,
	} {
		if label != "" && label == w.LabelFor(state) {
			return state
		}
	}
	return ""
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
		cleaned := filepath.Clean(c.Provider.DataFile)
		if filepath.IsAbs(c.Provider.DataFile) || cleaned == "." ||
			cleaned == ".." || filepath.Base(cleaned) != cleaned {
			return errors.New("provider.data_file must be a filename inside the configuration directory")
		}
	case "gitee":
		if c.Provider.Owner == "" || c.Provider.Repo == "" || c.Provider.TokenEnv == "" {
			return errors.New("provider owner, repo, and token_env are required for gitee")
		}
		if !validGiteeTokenEnv(c.Provider.TokenEnv) {
			return errors.New("provider.token_env must be an uppercase GITEE_*TOKEN* environment variable")
		}
	default:
		return fmt.Errorf("unsupported provider type %q", c.Provider.Type)
	}
	if c.Workflow.LeaseMinutes <= 0 {
		return errors.New("workflow.lease_minutes must be positive")
	}
	labels := make(map[string]domain.WorkflowState)
	for _, state := range []domain.WorkflowState{
		domain.StateReady, domain.StateClaimed, domain.StateWorking,
		domain.StateBlocked, domain.StateReview, domain.StateDone,
	} {
		label := strings.TrimSpace(c.Workflow.LabelFor(state))
		if label == "" {
			return fmt.Errorf("workflow label for %s is required", state)
		}
		if previous, exists := labels[label]; exists {
			return fmt.Errorf("workflow label %q is used for both %s and %s", label, previous, state)
		}
		labels[label] = state
	}
	switch c.Automation.Level {
	case "inspect", "patch", "commit", "delivery":
	default:
		return fmt.Errorf("automation.level must be inspect, patch, commit, or delivery")
	}
	if strings.TrimSpace(c.Git.BranchPattern) == "" {
		return errors.New("git.branch_pattern is required")
	}
	if !strings.Contains(c.Git.BranchPattern, "{number}") {
		return errors.New("git.branch_pattern must contain {number}")
	}
	knownPattern := strings.NewReplacer(
		"{type}", "", "{number}", "", "{slug}", "",
	).Replace(c.Git.BranchPattern)
	if strings.ContainsAny(knownPattern, "{}") {
		return errors.New("git.branch_pattern contains an unknown placeholder")
	}
	if c.Git.AllowPush && !c.Git.AllowCommit {
		return errors.New("git.allow_push requires git.allow_commit")
	}
	if c.Git.AllowPullRequest && !c.Git.AllowPush {
		return errors.New("git.allow_pull_request requires git.allow_push")
	}
	for i, command := range c.Validation.Commands {
		if len(command.Argv) == 0 || strings.TrimSpace(command.Argv[0]) == "" {
			return fmt.Errorf("validation.commands[%d].argv must not be empty", i)
		}
		for _, argument := range command.Argv {
			if strings.ContainsRune(argument, '\x00') {
				return fmt.Errorf("validation.commands[%d].argv contains NUL", i)
			}
		}
		if command.Timeout != "" {
			duration, err := time.ParseDuration(command.Timeout)
			if err != nil || duration <= 0 {
				return fmt.Errorf("validation.commands[%d].timeout must be a positive Go duration", i)
			}
		}
	}
	return nil
}

func validGiteeTokenEnv(value string) bool {
	if !strings.HasPrefix(value, "GITEE_") || !strings.Contains(value, "TOKEN") {
		return false
	}
	for index, r := range value {
		if (r >= 'A' && r <= 'Z') || r == '_' ||
			(index > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func Load(path string) (Config, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Config{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Config{}, errors.New("configuration must be a regular file, not a symlink")
	}
	const maximumConfigSize = 1 << 20
	if info.Size() > maximumConfigSize {
		return Config{}, fmt.Errorf("configuration exceeds %d bytes", maximumConfigSize)
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return Config{}, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return Config{}, errors.New("configuration changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumConfigSize+1))
	if err != nil {
		return Config{}, err
	}
	if len(raw) > maximumConfigSize {
		return Config{}, fmt.Errorf("configuration exceeds %d bytes", maximumConfigSize)
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
	raw, err := yaml.Marshal(Default())
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("refusing to overwrite existing %s", path)
	}
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(path)
		return err
	}
	return file.Close()
}
