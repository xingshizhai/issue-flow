package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xingshizhai/issue-flow/internal/clock"
	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/projectcontext"
	"github.com/xingshizhai/issue-flow/internal/provider"
	"github.com/xingshizhai/issue-flow/internal/provider/fake"
	"github.com/xingshizhai/issue-flow/internal/provider/gitee"
	"github.com/xingshizhai/issue-flow/internal/workflow"
)

type Runtime struct {
	ConfigPath  string
	ProjectRoot string
	Config      config.Config
	Provider    provider.Provider
	TokenSet    bool
}

func (r *Runtime) Workflow() *workflow.Service {
	return workflow.NewWithRedactKeys(
		r.Provider,
		clock.Real{},
		time.Duration(r.Config.Workflow.LeaseMinutes)*time.Minute,
		r.Config.Security.RedactKeys,
	).WithFinishState(domain.WorkflowState(r.Config.Workflow.FinishState))
}

func Open(configPath, project string) (*Runtime, error) {
	return OpenWithLookup(configPath, project, os.LookupEnv)
}

func OpenWithLookup(configPath, project string, lookup func(string) (string, bool)) (*Runtime, error) {
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
		dataPath, err := fake.ResolvePath(path, cfg.Provider.DataFile)
		if err != nil {
			return nil, err
		}
		p = fake.New(dataPath)
	case "gitee":
		token, _ := lookup(cfg.Provider.TokenEnv)
		if token == "" {
			return nil, fmt.Errorf("environment variable %s is not set", cfg.Provider.TokenEnv)
		}
		var enterprise *gitee.EnterpriseClient
		if cfg.Provider.Enterprise.Enabled {
			entTokenEnv := cfg.Provider.Enterprise.TokenEnv
			entToken, _ := lookup(entTokenEnv)
			if entToken == "" {
				return nil, fmt.Errorf("environment variable %s is not set", entTokenEnv)
			}
			enterprise = gitee.NewEnterpriseClient(
				entToken,
				cfg.Provider.Enterprise.APIBase,
				cfg.Provider.Enterprise.ID,
				cfg.Provider.Enterprise.Path,
				30*time.Second,
			)
		}
		p = gitee.NewWithEnterprise(
			gitee.NewClient(token, 30*time.Second),
			cfg.Provider.Owner,
			cfg.Provider.Repo,
			cfg.Workflow,
			enterprise,
		)
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider.Type)
	}
	runtime := &Runtime{
		ConfigPath: path, ProjectRoot: filepath.Dir(path), Config: cfg, Provider: p,
	}
	if cfg.Provider.TokenEnv != "" {
		_, runtime.TokenSet = lookup(cfg.Provider.TokenEnv)
	}
	return runtime, nil
}

func (r *Runtime) Context(ctx context.Context, number string) (projectcontext.Context, error) {
	issue, err := r.Provider.GetIssue(ctx, number)
	if err != nil {
		return projectcontext.Context{}, err
	}
	return projectcontext.Build(issue, r.Config, r.ProjectRoot)
}

type DoctorResult struct {
	ConfigPath    string                `json:"configPath"`
	ProviderType  string                `json:"providerType"`
	TokenEnv      string                `json:"tokenEnv,omitempty"`
	TokenSet      bool                  `json:"tokenSet"`
	Capabilities  provider.Capabilities `json:"capabilities"`
	EnterpriseOK  *bool                 `json:"enterpriseOk,omitempty"`
	EnterpriseErr string                `json:"enterpriseError,omitempty"`
}

func (r *Runtime) Doctor(ctx context.Context) (DoctorResult, error) {
	result := DoctorResult{
		ConfigPath:   r.ConfigPath,
		ProviderType: r.Config.Provider.Type,
		Capabilities: r.Provider.Capabilities(ctx),
	}
	if r.Config.Provider.TokenEnv != "" {
		result.TokenEnv = r.Config.Provider.TokenEnv
		result.TokenSet = r.TokenSet
	}
	if err := r.Provider.Check(ctx); err != nil {
		return result, err
	}
	if gp, ok := r.Provider.(interface {
		CheckEnterprise(context.Context) error
	}); ok && r.Config.Provider.Enterprise.Enabled {
		okFlag := true
		if err := gp.CheckEnterprise(ctx); err != nil {
			okFlag = false
			result.EnterpriseErr = err.Error()
		}
		result.EnterpriseOK = &okFlag
	}
	return result, nil
}
