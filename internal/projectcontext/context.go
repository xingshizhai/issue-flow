package projectcontext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/redact"
)

type InstructionFile struct {
	Path         string `json:"path"`
	ResolvedPath string `json:"resolvedPath,omitempty"`
	Exists       bool   `json:"exists"`
}

type GitPolicy struct {
	Branch           string `json:"branch"`
	AllowCommit      bool   `json:"allowCommit"`
	AllowPush        bool   `json:"allowPush"`
	AllowPullRequest bool   `json:"allowPullRequest"`
}

type Context struct {
	Issue            domain.Issue               `json:"issue"`
	ProjectRoot      string                     `json:"projectRoot"`
	InstructionFiles []InstructionFile          `json:"instructionFiles"`
	AutomationLevel  string                     `json:"automationLevel"`
	Validation       []config.ValidationCommand `json:"validation"`
	Git              GitPolicy                  `json:"git"`
}

func Build(issue domain.Issue, cfg config.Config, projectRoot string) (Context, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return Context{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return Context{}, fmt.Errorf("inspect project root: %w", err)
	}
	if !info.IsDir() {
		return Context{}, errors.New("project root is not a directory")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Context{}, fmt.Errorf("resolve project root: %w", err)
	}
	instructions := make([]InstructionFile, 0, len(cfg.Project.InstructionFiles))
	for _, configured := range cfg.Project.InstructionFiles {
		instruction, err := resolveInstruction(root, configured)
		if err != nil {
			return Context{}, err
		}
		instructions = append(instructions, instruction)
	}
	branch, err := BranchName(cfg.Git.BranchPattern, issue)
	if err != nil {
		return Context{}, err
	}
	level := automationRank(cfg.Automation.Level)
	return Context{
		Issue:       redact.New(cfg.Security.RedactKeys).Issue(issue.Public()),
		ProjectRoot: root, InstructionFiles: instructions,
		AutomationLevel: cfg.Automation.Level,
		Validation:      append([]config.ValidationCommand(nil), cfg.Validation.Commands...),
		Git: GitPolicy{
			Branch: branch, AllowCommit: cfg.Git.AllowCommit && level >= automationRank("commit"),
			AllowPush:        cfg.Git.AllowPush && level >= automationRank("delivery"),
			AllowPullRequest: cfg.Git.AllowPullRequest && level >= automationRank("delivery"),
		},
	}, nil
}

func automationRank(level string) int {
	switch level {
	case "inspect":
		return 0
	case "patch":
		return 1
	case "commit":
		return 2
	case "delivery":
		return 3
	default:
		return -1
	}
}

func resolveInstruction(root, configured string) (InstructionFile, error) {
	if configured == "" || filepath.IsAbs(configured) {
		return InstructionFile{}, fmt.Errorf("project instruction path %q must be relative", configured)
	}
	cleaned := filepath.Clean(configured)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return InstructionFile{}, fmt.Errorf("project instruction path %q escapes project root", configured)
	}
	candidate := filepath.Join(root, cleaned)
	info, err := os.Lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return InstructionFile{Path: filepath.ToSlash(cleaned)}, nil
	}
	if err != nil {
		return InstructionFile{}, fmt.Errorf("inspect project instruction %q: %w", configured, err)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return InstructionFile{}, fmt.Errorf("resolve project instruction %q: %w", configured, err)
	}
	if !within(root, resolved) {
		return InstructionFile{}, fmt.Errorf("project instruction path %q resolves outside project root", configured)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		return InstructionFile{}, err
	}
	if info.IsDir() || !resolvedInfo.Mode().IsRegular() {
		return InstructionFile{}, fmt.Errorf("project instruction path %q is not a regular file", configured)
	}
	return InstructionFile{
		Path: filepath.ToSlash(cleaned), ResolvedPath: resolved, Exists: true,
	}, nil
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}

func BranchName(pattern string, issue domain.Issue) (string, error) {
	issueType := "task"
	for _, label := range issue.Labels {
		if value, found := strings.CutPrefix(strings.ToLower(label.Name), "type:"); found {
			switch value {
			case "bug", "feature", "improvement":
				issueType = value
			}
		}
	}
	replacements := map[string]string{
		"{type}": issueType, "{number}": safeComponent(issue.Number), "{slug}": slug(issue.Title),
	}
	branch := pattern
	for placeholder, value := range replacements {
		branch = strings.ReplaceAll(branch, placeholder, value)
	}
	if strings.ContainsAny(branch, "{}") {
		return "", errors.New("git.branch_pattern contains an unknown placeholder")
	}
	segments := strings.Split(filepath.ToSlash(branch), "/")
	clean := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = safeComponent(segment)
		if segment == "" || segment == "." || segment == ".." || strings.HasSuffix(segment, ".lock") {
			return "", errors.New("git.branch_pattern produced an unsafe branch segment")
		}
		clean = append(clean, segment)
	}
	branch = strings.Join(clean, "/")
	if len(branch) > 120 {
		branch = strings.TrimRight(branch[:120], "-.")
	}
	if branch == "" || strings.HasSuffix(branch, ".lock") || strings.Contains(branch, "..") {
		return "", errors.New("git.branch_pattern produced an unsafe branch name")
	}
	return branch, nil
}

func slug(value string) string {
	result := safeComponent(strings.ToLower(value))
	if result == "" {
		return "task"
	}
	if len(result) > 48 {
		result = strings.TrimRight(result[:48], "-.")
	}
	return result
}

func safeComponent(value string) string {
	var builder strings.Builder
	dash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if r >= 'A' && r <= 'Z' {
				r += 'a' - 'A'
			}
			builder.WriteRune(r)
			dash = false
			continue
		}
		if r == '.' || r == '_' || r == '-' {
			if builder.Len() > 0 && !dash {
				builder.WriteRune(r)
				dash = r == '-'
			}
			continue
		}
		if builder.Len() > 0 && !dash {
			builder.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(builder.String(), "-._")
}
