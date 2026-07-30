package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"issue-flow/internal/clock"
	"issue-flow/internal/config"
	"issue-flow/internal/provider"
	"issue-flow/internal/provider/fake"
	"issue-flow/internal/workflow"
)

type Runtime struct {
	ConfigPath string
	Config     config.Config
	Provider   provider.Provider
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
		return nil, fmt.Errorf("gitee provider is not implemented")
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider.Type)
	}
	return &Runtime{ConfigPath: path, Config: cfg, Provider: p}, nil
}

type DoctorResult struct {
	ConfigPath   string                `json:"configPath"`
	ProviderType string                `json:"providerType"`
	TokenEnv     string                `json:"tokenEnv,omitempty"`
	TokenSet     bool                  `json:"tokenSet"`
	Capabilities provider.Capabilities `json:"capabilities"`
}

func (r *Runtime) Doctor(ctx context.Context) DoctorResult {
	result := DoctorResult{
		ConfigPath:   r.ConfigPath,
		ProviderType: r.Config.Provider.Type,
		Capabilities: r.Provider.Capabilities(ctx),
	}
	if r.Config.Provider.TokenEnv != "" {
		result.TokenEnv = r.Config.Provider.TokenEnv
		_, result.TokenSet = os.LookupEnv(r.Config.Provider.TokenEnv)
	}
	return result
}
