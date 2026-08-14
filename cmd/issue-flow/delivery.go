package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/workflow"
)

type validationReport struct {
	Commands []domain.ValidationEvidence `json:"commands"`
}

func collectDeliveryEvidence(ctx context.Context, root, requestedCommit, reportPath string, cfg config.Config) (*domain.DeliveryEvidence, error) {
	evidence := &domain.DeliveryEvidence{}

	inside, _ := gitOutput(ctx, root, "rev-parse", "--is-inside-work-tree")
	if inside == "true" {
		status := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "--untracked-files=normal")
		statusOutput, err := status.Output()
		if err != nil {
			return nil, fmt.Errorf("%w: inspect git worktree: %v", workflow.ErrInvalidInput, err)
		}
		clean := len(statusOutput) == 0
		evidence.WorktreeClean = &clean
		if cfg.Git.RequireClean && !clean {
			return nil, fmt.Errorf("%w: git worktree must be clean before finish", workflow.ErrInvalidInput)
		}
	} else if cfg.Git.RequireClean || cfg.Git.RequireCommit || requestedCommit != "" {
		return nil, fmt.Errorf("%w: project root is not a git worktree", workflow.ErrInvalidInput)
	}

	requestedCommit = strings.TrimSpace(requestedCommit)
	if cfg.Git.RequireCommit && requestedCommit == "" {
		return nil, fmt.Errorf("%w: --commit is required by project policy", workflow.ErrInvalidInput)
	}
	if requestedCommit != "" {
		if !safeGitRevision(requestedCommit) {
			return nil, fmt.Errorf("%w: --commit contains unsafe characters", workflow.ErrInvalidInput)
		}
		resolved, err := gitOutput(ctx, root, "rev-parse", "--verify", requestedCommit+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("%w: --commit does not resolve to a commit", workflow.ErrInvalidInput)
		}
		head, err := gitOutput(ctx, root, "rev-parse", "HEAD")
		if err != nil {
			return nil, fmt.Errorf("%w: resolve HEAD: %v", workflow.ErrInvalidInput, err)
		}
		if resolved != head {
			return nil, fmt.Errorf("%w: --commit must resolve to current HEAD", workflow.ErrInvalidInput)
		}
		evidence.Commit = resolved
	}

	if cfg.Validation.RequireReport && strings.TrimSpace(reportPath) == "" {
		return nil, fmt.Errorf("%w: --validation-report is required by project policy", workflow.ErrInvalidInput)
	}
	if strings.TrimSpace(reportPath) != "" {
		raw, err := readTextFile("validation report", reportPath, 64<<10)
		if err != nil {
			return nil, err
		}
		var report validationReport
		if err := json.Unmarshal([]byte(raw), &report); err != nil {
			return nil, fmt.Errorf("%w: parse validation report: %v", workflow.ErrInvalidInput, err)
		}
		if len(report.Commands) == 0 {
			return nil, fmt.Errorf("%w: validation report has no commands", workflow.ErrInvalidInput)
		}
		expected := make(map[string]bool, len(cfg.Validation.Commands))
		for _, command := range cfg.Validation.Commands {
			expected[strings.Join(command.Argv, " ")] = false
		}
		for i, item := range report.Commands {
			item.Command = strings.TrimSpace(item.Command)
			item.Status = strings.TrimSpace(item.Status)
			if item.Command == "" {
				return nil, fmt.Errorf("%w: validation command %d is empty", workflow.ErrInvalidInput, i)
			}
			if cfg.Validation.RequireReport {
				if _, ok := expected[item.Command]; !ok {
					return nil, fmt.Errorf("%w: validation report contains unconfigured command %q", workflow.ErrInvalidInput, item.Command)
				}
				expected[item.Command] = true
			}
			switch item.Status {
			case "passed", "blocked", "skipped":
			case "failed":
				return nil, fmt.Errorf("%w: validation %q failed", workflow.ErrInvalidInput, item.Command)
			default:
				return nil, fmt.Errorf("%w: validation %q has invalid status %q", workflow.ErrInvalidInput, item.Command, item.Status)
			}
			evidence.Validation = append(evidence.Validation, item)
		}
		for command, reported := range expected {
			if !reported {
				return nil, fmt.Errorf("%w: validation report is missing configured command %q", workflow.ErrInvalidInput, command)
			}
		}
	}

	if cfg.Workflow.FinishState == string(domain.StateDone) {
		for _, item := range evidence.Validation {
			if item.Status != "passed" {
				return nil, fmt.Errorf("%w: finish_state done requires all reported validations to pass", workflow.ErrInvalidInput)
			}
		}
	}
	return evidence, nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", root}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).Output()
	return strings.TrimSpace(string(output)), err
}

func safeGitRevision(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}
