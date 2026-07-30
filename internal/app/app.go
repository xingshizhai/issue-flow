package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"issue-flow/internal/clock"
	"issue-flow/internal/config"
	"issue-flow/internal/projectcontext"
	"issue-flow/internal/provider"
	"issue-flow/internal/provider/fake"
	"issue-flow/internal/provider/gitee"
	"issue-flow/internal/workflow"
)

type Runtime struct {
	ConfigPath  string
	ProjectRoot string
	Config      config.Config
	Provider    provider.Provider
}

func (r *Runtime) Workflow() *workflow.Service {
	return workflow.New(r.Provider, clock.Real{}, time.Duration(r.Config.Workflow.LeaseMinutes)*time.Minute)
}

func Open(configPath, project string) (*Runtime, error) {
	path, err := config.Find(configPath, project)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	var p provider.Provider
	switch cfg.Provider.Type {
	case "fake":
		p = fake.New(fake.ResolvePath(path, cfg.Provider.DataFile))
	case "gitee":
		token := os.Getenv(cfg.Provider.TokenEnv)
		if token == "" {
			return nil, fmt.Errorf("environment variable %s is not set", cfg.Provider.TokenEnv)
		}
		p = gitee.New(
			gitee.NewClient(token, 30*time.Second),
			cfg.Provider.Owner,
			cfg.Provider.Repo,
			cfg.Workflow,
		)
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider.Type)
	}
	return &Runtime{
		ConfigPath: path, ProjectRoot: filepath.Dir(path), Config: cfg, Provider: p,
	}, nil
}

func (r *Runtime) Context(ctx context.Context, number string) (projectcontext.Context, error) {
	issue, err := r.Provider.GetIssue(ctx, number)
	if err != nil {
		return projectcontext.Context{}, err
	}
	return projectcontext.Build(issue, r.Config, r.ProjectRoot)
}

type DoctorResult struct {
	ConfigPath   string                `json:"configPath"`
	ProviderType string                `json:"providerType"`
	TokenEnv     string                `json:"tokenEnv,omitempty"`
	TokenSet     bool                  `json:"tokenSet"`
	Capabilities provider.Capabilities `json:"capabilities"`
}

func (r *Runtime) Doctor(ctx context.Context) (DoctorResult, error) {
	result := DoctorResult{
		ConfigPath:   r.ConfigPath,
		ProviderType: r.Config.Provider.Type,
		Capabilities: r.Provider.Capabilities(ctx),
	}
	if r.Config.Provider.TokenEnv != "" {
		result.TokenEnv = r.Config.Provider.TokenEnv
		_, result.TokenSet = os.LookupEnv(r.Config.Provider.TokenEnv)
	}
	if err := r.Provider.Check(ctx); err != nil {
		return result, err
	}
	return result, nil
}
