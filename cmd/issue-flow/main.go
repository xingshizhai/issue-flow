package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xingshizhai/issue-flow/internal/app"
	"github.com/xingshizhai/issue-flow/internal/config"
	"github.com/xingshizhai/issue-flow/internal/domain"
	"github.com/xingshizhai/issue-flow/internal/envfile"
	"github.com/xingshizhai/issue-flow/internal/output"
	"github.com/xingshizhai/issue-flow/internal/projectcontext"
	"github.com/xingshizhai/issue-flow/internal/provider"
	"github.com/xingshizhai/issue-flow/internal/redact"
	"github.com/xingshizhai/issue-flow/internal/workflow"
)

var (
	version     = "0.2.0-dev"
	buildCommit = "unknown"
)

const workflowProtocolVersion = 2

type cli struct {
	stdout io.Writer
	stderr io.Writer
}

type globals struct {
	config  string
	project string
	envFile string
	format  string
	dryRun  bool
	verbose bool
}

func main() {
	code := (&cli{stdout: os.Stdout, stderr: os.Stderr}).run(context.Background(), os.Args[1:])
	os.Exit(code)
}

func (c *cli) run(ctx context.Context, args []string) int {
	g, args, err := parseGlobals(args)
	if err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	if len(args) == 0 {
		c.usage(c.stderr)
		return 2
	}
	switch args[0] {
	case "help", "--help", "-h":
		c.usage(c.stdout)
		return 0
	case "version", "--version":
		return c.versionInfo(g)
	case "capabilities":
		return c.capabilities(g)
	case "init":
		return c.init(g, args[1:])
	case "doctor":
		return c.doctor(ctx, g, args[1:])
	case "create":
		return c.create(ctx, g, args[1:])
	case "comment":
		return c.comment(ctx, g, args[1:])
	case "adopt":
		return c.adopt(ctx, g, args[1:])
	case "complete":
		return c.complete(ctx, g, args[1:])
	case "list":
		return c.list(ctx, g, args[1:])
	case "show":
		return c.show(ctx, g, args[1:])
	case "context":
		return c.context(ctx, g, args[1:])
	case "claim", "start", "heartbeat", "progress", "block", "release", "reclaim", "finish":
		return c.leaseCommand(ctx, g, args[0], args[1:])
	default:
		return c.fail(g.format, "INVALID_ARGUMENT", fmt.Errorf("unknown command %q", args[0]), 2)
	}
}

type buildInfo struct {
	Version                 string   `json:"version"`
	Commit                  string   `json:"commit"`
	ConfigSchemaVersion     int      `json:"configSchemaVersion"`
	OutputSchemaVersion     int      `json:"outputSchemaVersion"`
	WorkflowProtocolVersion int      `json:"workflowProtocolVersion"`
	Features                []string `json:"features"`
}

func currentBuildInfo() buildInfo {
	return buildInfo{
		Version: version, Commit: buildCommit,
		ConfigSchemaVersion: config.CurrentVersion, OutputSchemaVersion: output.SchemaVersion,
		WorkflowProtocolVersion: workflowProtocolVersion,
		Features: []string{
			"configurable-finish-state", "delivery-evidence", "external-attachment-refs",
			"create-extra-labels", "create-priority", "comment-body-file", "provider-state-sync",
		},
	}
}

func (c *cli) versionInfo(g globals) int {
	info := currentBuildInfo()
	return c.success(g.format, info, info.Version)
}

func (c *cli) capabilities(g globals) int {
	info := currentBuildInfo()
	data := struct {
		Commands []string `json:"commands"`
		Features []string `json:"features"`
	}{
		Commands: []string{"init", "doctor", "create", "comment", "adopt", "complete", "list", "show", "context", "claim", "start", "heartbeat", "progress", "block", "release", "reclaim", "finish", "version", "capabilities"},
		Features: info.Features,
	}
	return c.success(g.format, data, strings.Join(data.Features, "\n"))
}

func (c *cli) complete(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 || !validIssueReference(args[0]) {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("complete requires one valid issue number"), 2)
	}
	flags := flag.NewFlagSet("complete", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	reviewer := flags.String("reviewer", "", "stable human reviewer identifier")
	conclusionFile := flags.String("conclusion-file", "", "path to the review conclusion")
	requestedOperationID := flags.String("operation-id", "", "stable operation ID for retry correlation")
	if err := flags.Parse(args[1:]); err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	if flags.NArg() != 0 || *reviewer == "" {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("complete requires --reviewer and no extra arguments"), 2)
	}
	if *requestedOperationID != "" && !validOperationID(*requestedOperationID) {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--operation-id must start with op_ and contain 1 to 64 safe characters"), 2)
	}
	conclusion, err := readSummaryFile(*conclusionFile)
	if err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	runtime, code := c.open(g)
	if runtime == nil {
		return code
	}
	opID := operationID()
	if *requestedOperationID != "" {
		opID = *requestedOperationID
	}
	result, err := runtime.Workflow().Complete(ctx, args[0], *reviewer, opID, conclusion, g.dryRun)
	if err != nil {
		return c.workflowFailure(g.format, opID, err)
	}
	result.Issue = redact.New(runtime.Config.Security.RedactKeys).Issue(result.Issue.Public())
	return c.successWithID(g.format, opID, result,
		fmt.Sprintf("#%s is now %s", result.Issue.Number, result.Issue.WorkflowState))
}

func (c *cli) create(ctx context.Context, g globals, args []string) int {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	title := flags.String("title", "", "issue title")
	bodyFile := flags.String("body-file", "", "path to issue body")
	issueType := flags.String("type", "", "bug, feature, or improvement")
	priority := flags.String("priority", "", "issue priority: low, medium, high, or critical (written to the provider's native priority field, not a label)")
	var extraLabels stringSliceFlag
	flags.Var(&extraLabels, "label", "extra label to attach, beyond the type/ready labels (repeatable)")
	if err := flags.Parse(args); err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	if flags.NArg() != 0 || strings.TrimSpace(*title) == "" {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("create requires --title and no positional arguments"), 2)
	}
	if *issueType != "bug" && *issueType != "feature" && *issueType != "improvement" {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--type must be bug, feature, or improvement"), 2)
	}
	switch *priority {
	case "", "low", "medium", "high", "critical":
	default:
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--priority must be low, medium, high, or critical"), 2)
	}
	body, err := readSummaryFile(*bodyFile)
	if err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	runtime, code := c.open(g)
	if runtime == nil {
		return code
	}
	labels := []string{"type-" + *issueType, runtime.Config.Workflow.ReadyLabel}
	seen := map[string]bool{labels[0]: true, labels[1]: true}
	for _, label := range extraLabels {
		if seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
	}
	input := provider.CreateIssueInput{
		Title: strings.TrimSpace(*title), Body: body, Type: *issueType,
		Labels: labels, Priority: *priority,
	}
	if g.dryRun {
		return c.success(g.format, input, "Would create issue: "+input.Title)
	}
	creator, ok := runtime.Provider.(provider.IssueCreator)
	if !ok {
		return c.fail(g.format, "UNSUPPORTED_CAPABILITY", provider.ErrUnsupported, 6)
	}
	issue, err := creator.CreateIssue(ctx, input)
	if err != nil {
		return c.providerFailure(g.format, err)
	}
	issue = redact.New(runtime.Config.Security.RedactKeys).Issue(issue.Public())
	return c.success(g.format, issue, fmt.Sprintf("#%s %s", issue.Number, issue.Title),
		missingLabelWarnings(labels, issue.Labels)...)
}

// missingLabelWarnings reports requested labels the provider didn't actually
// attach. Gitee silently drops labels that don't already exist on the repo
// (and possibly others a token lacks permission to manage) instead of
// erroring or auto-creating them — without this, a caller has no way to
// know a label like "priority:high" never landed.
func missingLabelWarnings(requested []string, actual []domain.Label) []string {
	present := make(map[string]bool, len(actual))
	for _, label := range actual {
		present[label.Name] = true
	}
	var warnings []string
	for _, name := range requested {
		if !present[name] {
			warnings = append(warnings, fmt.Sprintf("label %q was requested but is not on the created issue (Gitee silently drops labels that don't already exist on the repo, or that the token can't manage)", name))
		}
	}
	return warnings
}

// comment posts a plain-text comment to an issue. Unlike claim/start/.../finish,
// it requires no lease: it is meant for relaying a short message (e.g. from an
// in-app assistant) without performing a workflow state transition.
func (c *cli) comment(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 || !validIssueReference(args[0]) {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("comment requires one valid issue number"), 2)
	}
	number := args[0]
	flags := flag.NewFlagSet("comment", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	body := flags.String("body", "", "comment text")
	bodyFile := flags.String("body-file", "", "path to comment text, instead of --body")
	if err := flags.Parse(args[1:]); err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	if flags.NArg() != 0 {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("comment accepts only one issue number"), 2)
	}
	if *body != "" && *bodyFile != "" {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--body and --body-file are mutually exclusive"), 2)
	}
	if *bodyFile != "" {
		fileBody, err := readTextFile("comment body file", *bodyFile, 64<<10)
		if err != nil {
			return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
		}
		*body = fileBody
	}
	if strings.TrimSpace(*body) == "" {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("comment requires --body or --body-file"), 2)
	}
	runtime, code := c.open(g)
	if runtime == nil {
		return code
	}
	if g.dryRun {
		return c.success(g.format, map[string]any{"number": number, "body": *body}, "Would comment on #"+number)
	}
	commenter, ok := runtime.Provider.(provider.IssueCommenter)
	if !ok {
		return c.fail(g.format, "UNSUPPORTED_CAPABILITY", provider.ErrUnsupported, 6)
	}
	comment, err := commenter.AddComment(ctx, number, *body)
	if err != nil {
		return c.providerFailure(g.format, err)
	}
	comment = redact.New(runtime.Config.Security.RedactKeys).Comment(comment)
	return c.success(g.format, comment, fmt.Sprintf("Commented on #%s", number))
}

// adopt brings an issue that was created outside issue-flow (no workflow
// label at all — e.g. filed by other tooling, or predating issue-flow's
// adoption in this repo) under management by attaching the ready label. It
// fails with STATE_CONFLICT if the issue already has a workflow label,
// rather than silently reinterpreting whatever state it's already in.
func (c *cli) adopt(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 || !validIssueReference(args[0]) {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("adopt requires one valid issue number"), 2)
	}
	number := args[0]
	flags := flag.NewFlagSet("adopt", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	if err := flags.Parse(args[1:]); err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	if flags.NArg() != 0 {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("adopt accepts only one issue number"), 2)
	}
	runtime, code := c.open(g)
	if runtime == nil {
		return code
	}
	if g.dryRun {
		return c.success(g.format, map[string]any{"number": number}, "Would adopt #"+number)
	}
	adopter, ok := runtime.Provider.(provider.IssueAdopter)
	if !ok {
		return c.fail(g.format, "UNSUPPORTED_CAPABILITY", provider.ErrUnsupported, 6)
	}
	issue, err := adopter.AdoptIssue(ctx, number)
	if err != nil {
		return c.providerFailure(g.format, err)
	}
	issue = redact.New(runtime.Config.Security.RedactKeys).Issue(issue.Public())
	return c.success(g.format, issue, fmt.Sprintf("#%s adopted into ready", issue.Number),
		missingLabelWarnings([]string{runtime.Config.Workflow.ReadyLabel}, issue.Labels)...)
}

func (c *cli) context(ctx context.Context, g globals, args []string) int {
	if len(args) != 1 || !validIssueReference(args[0]) {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("context requires one valid issue number"), 2)
	}
	runtime, code := c.open(g)
	if runtime == nil {
		return code
	}
	result, err := runtime.Context(ctx, args[0])
	if err != nil {
		if isProviderError(err) {
			return c.providerFailure(g.format, err)
		}
		return c.fail(g.format, "CONFIG_ERROR", err, 2)
	}
	if g.format == "json" {
		return c.success(g.format, result, "")
	}
	fmt.Fprintf(c.stdout, "# Issue #%s: %s\n\n", result.Issue.Number, result.Issue.Title)
	fmt.Fprintf(c.stdout, "State: %s\nAutomation: %s\nBranch: %s\n\n",
		result.Issue.WorkflowState, result.AutomationLevel, result.Git.Branch)
	fmt.Fprintln(c.stdout, "## Description")
	fmt.Fprintln(c.stdout, result.Issue.Body)
	fmt.Fprintln(c.stdout, "\n## Project instructions")
	for _, instruction := range result.InstructionFiles {
		status := "missing"
		if instruction.Exists {
			status = "present"
		}
		fmt.Fprintf(c.stdout, "- %s (%s)\n", instruction.Path, status)
	}
	fmt.Fprintln(c.stdout, "\n## Validation")
	if len(result.Validation) == 0 {
		fmt.Fprintln(c.stdout, "- none configured")
	}
	for _, command := range result.Validation {
		fmt.Fprintf(c.stdout, "- argv: %q", command.Argv)
		if command.Timeout != "" {
			fmt.Fprintf(c.stdout, " (timeout %s)", command.Timeout)
		}
		fmt.Fprintln(c.stdout)
	}
	fmt.Fprintf(c.stdout, "\n## Git policy\n- commit: %t\n- push: %t\n- pull request: %t\n",
		result.Git.AllowCommit, result.Git.AllowPush, result.Git.AllowPullRequest)
	return 0
}

// isProviderError keeps context's two failure domains separate: fetching the
// Issue can fail at the remote Provider, while building the local project
// context can fail because of project configuration or filesystem policy.
// Provider failures must retain the same stable code/retryability as show/list
// instead of being flattened into CONFIG_ERROR.
func isProviderError(err error) bool {
	return errors.Is(err, provider.ErrAuthentication) ||
		errors.Is(err, provider.ErrPermission) ||
		errors.Is(err, provider.ErrNotFound) ||
		errors.Is(err, provider.ErrMisconfigured) ||
		errors.Is(err, provider.ErrRateLimited) ||
		errors.Is(err, provider.ErrUnsupported) ||
		errors.Is(err, provider.ErrPreconditionFailed) ||
		errors.Is(err, provider.ErrUnavailable)
}

func (c *cli) leaseCommand(ctx context.Context, g globals, command string, args []string) int {
	if len(args) == 0 {
		return c.fail(g.format, "INVALID_ARGUMENT", fmt.Errorf("%s requires one issue number", command), 2)
	}
	number := args[0]
	if !validIssueReference(number) {
		return c.fail(g.format, "INVALID_ARGUMENT", fmt.Errorf("invalid issue number %q", args[0]), 2)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	agentID := flags.String("agent", "", "stable agent identifier")
	leaseToken := flags.String("lease-token", "", "secret lease token returned by claim")
	leaseTokenFile := flags.String("lease-token-file", "", "path to a file containing the secret lease token, instead of --lease-token")
	reason := flags.String("reason", "", "release or block reason")
	message := flags.String("message", "", "progress message")
	summaryFile := flags.String("summary-file", "", "path to a finish summary")
	commit := flags.String("commit", "", "commit delivered by finish (must resolve to HEAD)")
	validationReport := flags.String("validation-report", "", "path to structured validation evidence JSON")
	requestedOperationID := flags.String("operation-id", "", "stable operation ID for retry correlation")
	if err := flags.Parse(args[1:]); err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	if flags.NArg() != 0 {
		return c.fail(g.format, "INVALID_ARGUMENT", fmt.Errorf("%s accepts only one issue number", command), 2)
	}
	if command != "reclaim" && *agentID == "" {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--agent is required"), 2)
	}
	if *leaseToken != "" && *leaseTokenFile != "" {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--lease-token and --lease-token-file are mutually exclusive"), 2)
	}
	if *leaseTokenFile != "" {
		fileToken, err := readLeaseTokenFile(*leaseTokenFile)
		if err != nil {
			return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
		}
		*leaseToken = fileToken
	}
	if command != "claim" && command != "reclaim" && *leaseToken == "" {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--lease-token or --lease-token-file is required"), 2)
	}
	if *requestedOperationID != "" && !validOperationID(*requestedOperationID) {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--operation-id must start with op_ and contain 1 to 64 safe characters"), 2)
	}
	runtime, code := c.open(g)
	if runtime == nil {
		return code
	}
	opID := operationID()
	if *requestedOperationID != "" {
		opID = *requestedOperationID
	}
	service := runtime.Workflow()
	var result workflow.Result
	var err error
	switch command {
	case "claim":
		result, err = service.Claim(ctx, number, *agentID, opID, g.dryRun)
	case "start":
		result, err = service.Start(ctx, number, *agentID, *leaseToken, opID, g.dryRun)
	case "heartbeat":
		result, err = service.Heartbeat(ctx, number, *agentID, *leaseToken, opID, g.dryRun)
	case "progress":
		result, err = service.Progress(ctx, number, *agentID, *leaseToken, opID, *message, g.dryRun)
	case "block":
		result, err = service.Block(ctx, number, *agentID, *leaseToken, opID, *reason, g.dryRun)
	case "release":
		result, err = service.Release(ctx, number, *agentID, *leaseToken, opID, *reason, g.dryRun)
	case "reclaim":
		result, err = service.Reclaim(ctx, number, opID, g.dryRun)
	case "finish":
		var summary string
		summary, err = readSummaryFile(*summaryFile)
		if err == nil {
			var evidence *domain.DeliveryEvidence
			evidence, err = collectDeliveryEvidence(ctx, runtime.ProjectRoot, *commit, *validationReport, runtime.Config)
			if err == nil {
				result, err = service.FinishWithEvidence(ctx, number, *agentID, *leaseToken, opID, summary, evidence, g.dryRun)
			}
		}
	}
	if err != nil {
		return c.workflowFailure(g.format, opID, err)
	}
	result.Issue = redact.New(runtime.Config.Security.RedactKeys).Issue(result.Issue.Public())
	text := fmt.Sprintf("#%s is now %s", result.Issue.Number, result.Issue.WorkflowState)
	if result.Issue.Lease != nil {
		text += fmt.Sprintf("\nAgent: %s\nExpires: %s",
			result.Issue.Lease.AgentID,
			result.Issue.Lease.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	if result.LeaseToken != "" {
		text += "\nLease token: " + result.LeaseToken
	}
	return c.successWithID(g.format, opID, result, text)
}

func readSummaryFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: --summary-file is required", workflow.ErrInvalidInput)
	}
	return readTextFile("summary file", path, 64<<10)
}

// readTextFile reads path as a TOCTOU-safe regular file (no symlinks, size
// capped at maxSize), used for any command's long-form text input
// (--body-file/--summary-file/--conclusion-file). label appears in error
// messages so callers get a message specific to the flag they used.
func readTextFile(label, path string, maxSize int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("%w: read %s: %v", workflow.ErrInvalidInput, label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s must be a regular file, not a symlink", workflow.ErrInvalidInput, label)
	}
	if info.Size() > maxSize {
		return "", fmt.Errorf("%w: %s exceeds %d bytes", workflow.ErrInvalidInput, label, maxSize)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%w: open %s: %v", workflow.ErrInvalidInput, label, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("%w: inspect opened %s: %v", workflow.ErrInvalidInput, label, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return "", fmt.Errorf("%w: %s changed while opening", workflow.ErrInvalidInput, label)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return "", fmt.Errorf("%w: read %s: %v", workflow.ErrInvalidInput, label, err)
	}
	if int64(len(raw)) > maxSize {
		return "", fmt.Errorf("%w: %s exceeds %d bytes", workflow.ErrInvalidInput, label, maxSize)
	}
	return string(raw), nil
}

// readLeaseTokenFile reads a lease token from a file instead of a command
// line argument — a command's full argv (including --lease-token's value)
// is visible to any other user on the same machine via `ps`, which sits
// oddly next to the rule that a plaintext lease token must never be logged
// or committed. Same TOCTOU-safe regular-file check as readSummaryFile,
// sized for a token rather than a long-form document, and trimmed since a
// token is typically written with a trailing newline (echo, printf).
func readLeaseTokenFile(path string) (string, error) {
	raw, err := readTextFile("lease token file", path, 4<<10)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", fmt.Errorf("%w: lease token file is empty", workflow.ErrInvalidInput)
	}
	return token, nil
}

func (c *cli) init(g globals, args []string) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	if err := flags.Parse(args); err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	if flags.NArg() != 0 {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("init accepts no positional arguments"), 2)
	}
	base := g.project
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return c.fail(g.format, "INTERNAL", err, 1)
		}
	}
	path := g.config
	if path == "" {
		path = filepath.Join(base, config.DefaultName)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return c.fail(g.format, "CONFIG_ERROR", err, 2)
	}
	if g.dryRun {
		return c.success(g.format, map[string]any{"path": absolute, "wouldCreate": true}, "Would create "+absolute)
	}
	if err := config.WriteDefault(absolute); err != nil {
		return c.fail(g.format, "CONFIG_ERROR", err, 2)
	}
	return c.success(g.format, map[string]any{"path": absolute, "created": true}, "Created "+absolute)
}

func (c *cli) doctor(ctx context.Context, g globals, args []string) int {
	if len(args) != 0 {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("doctor accepts no positional arguments"), 2)
	}
	runtime, code := c.open(g)
	if runtime == nil {
		return code
	}
	result, err := runtime.Doctor(ctx)
	if err != nil {
		return c.providerFailure(g.format, err)
	}
	text := fmt.Sprintf("Configuration: %s\nProvider: %s\nRead issues: %t\nWrite issues: %t",
		result.ConfigPath, result.ProviderType,
		result.Capabilities.ReadIssues, result.Capabilities.WriteIssues)
	return c.success(g.format, result, text)
}

func (c *cli) list(ctx context.Context, g globals, args []string) int {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	ready := flags.Bool("ready", false, "list ready issues")
	state := flags.String("state", "", "workflow state")
	unmanaged := flags.Bool("unmanaged", false, "list issues with no workflow label at all (adopt candidates)")
	limit := flags.Int("limit", 50, "maximum issues")
	cursor := flags.String("cursor", "", "pagination cursor")
	if err := flags.Parse(args); err != nil {
		return c.fail(g.format, "INVALID_ARGUMENT", err, 2)
	}
	if flags.NArg() != 0 {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("list accepts no positional arguments"), 2)
	}
	if *ready && *state != "" {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--ready and --state cannot be combined"), 2)
	}
	if *unmanaged && (*ready || *state != "") {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--unmanaged cannot be combined with --ready or --state"), 2)
	}
	queryState := domain.WorkflowState(*state)
	if *ready {
		queryState = domain.StateReady
	}
	if queryState != "" && !domain.IsWorkflowState(queryState) {
		return c.fail(g.format, "INVALID_ARGUMENT", fmt.Errorf("unknown workflow state %q", queryState), 2)
	}
	if *limit <= 0 || *limit > 1000 {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("--limit must be between 1 and 1000"), 2)
	}
	runtime, code := c.open(g)
	if runtime == nil {
		return code
	}
	page, err := runtime.Provider.ListIssues(ctx, provider.ListQuery{
		State: queryState, Limit: *limit, Cursor: *cursor,
	})
	if err != nil {
		return c.providerFailure(g.format, err)
	}
	if *unmanaged {
		// --unmanaged has no server-side equivalent (ListQuery.State only
		// ever names one real workflow state) — filtered within whatever
		// page was fetched, so a repo with unmanaged issues scattered past
		// --limit needs --cursor to see the rest, same as any other filter
		// here.
		filtered := page.Items[:0]
		for _, issue := range page.Items {
			if issue.WorkflowState == "" {
				filtered = append(filtered, issue)
			}
		}
		page.Items = filtered
	}
	sanitizer := redact.New(runtime.Config.Security.RedactKeys)
	for i := range page.Items {
		page.Items[i] = sanitizer.Issue(page.Items[i].Public())
	}
	if g.format == "json" {
		return c.success(g.format, page, "")
	}
	for _, issue := range page.Items {
		output.TextIssue(c.stdout, issue.Number, issue.Title, string(issue.WorkflowState))
	}
	if page.NextCursor != "" {
		fmt.Fprintf(c.stdout, "Next cursor: %s\n", page.NextCursor)
	}
	return 0
}

func (c *cli) show(ctx context.Context, g globals, args []string) int {
	if len(args) != 1 {
		return c.fail(g.format, "INVALID_ARGUMENT", errors.New("show requires one issue number"), 2)
	}
	number := args[0]
	if !validIssueReference(number) {
		return c.fail(g.format, "INVALID_ARGUMENT", fmt.Errorf("invalid issue number %q", args[0]), 2)
	}
	runtime, code := c.open(g)
	if runtime == nil {
		return code
	}
	issue, err := runtime.Provider.GetIssue(ctx, number)
	if err != nil {
		return c.providerFailure(g.format, err)
	}
	external, warnings := projectcontext.ExternalAttachments(issue)
	issue.Attachments = append(issue.Attachments, external...)
	issue = redact.New(runtime.Config.Security.RedactKeys).Issue(issue.Public())
	text := fmt.Sprintf("#%s %s\nState: %s\nURL: %s\n\n%s",
		issue.Number, issue.Title, issue.WorkflowState, issue.URL, issue.Body)
	return c.success(g.format, issue, text, warnings...)
}

// stringSliceFlag implements flag.Value to accept a repeatable flag.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("--label value must not be empty")
	}
	*s = append(*s, trimmed)
	return nil
}

func validIssueReference(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (c *cli) open(g globals) (*app.Runtime, int) {
	lookup := os.LookupEnv
	if g.envFile != "" {
		values, err := envfile.Load(g.envFile)
		if err != nil {
			return nil, c.fail(g.format, "CONFIG_ERROR", err, 2)
		}
		lookup = func(key string) (string, bool) {
			if value, ok := os.LookupEnv(key); ok {
				return value, true
			}
			value, ok := values[key]
			return value, ok
		}
	}
	runtime, err := app.OpenWithLookup(g.config, g.project, lookup)
	if err != nil {
		return nil, c.fail(g.format, "CONFIG_ERROR", err, 2)
	}
	if g.verbose {
		capabilities := runtime.Provider.Capabilities(context.Background())
		fmt.Fprintf(c.stderr,
			"issue-flow: debug: config=%s provider=%s transport=%s credential=%s read=%t write=%t\n",
			runtime.ConfigPath,
			runtime.Config.Provider.Type,
			capabilities.AccessTransport,
			capabilities.CredentialMode,
			capabilities.ReadIssues,
			capabilities.WriteIssues,
		)
	}
	return runtime, 0
}

func (c *cli) providerFailure(format string, err error) int {
	return c.providerFailureWithID(format, operationID(), err)
}

func (c *cli) providerFailureWithID(format, operationID string, err error) int {
	switch {
	case errors.Is(err, provider.ErrAuthentication):
		return c.failWithIDRetryable(format, operationID, "AUTHENTICATION_FAILED", err, 3, false)
	case errors.Is(err, provider.ErrPermission):
		return c.failWithIDRetryable(format, operationID, "PERMISSION_DENIED", err, 3, false)
	case errors.Is(err, provider.ErrNotFound):
		return c.failWithIDRetryable(format, operationID, "NOT_FOUND", err, 4, false)
	case errors.Is(err, provider.ErrMisconfigured):
		return c.failWithIDRetryable(format, operationID, "CONFIG_ERROR", err, 2, false)
	case errors.Is(err, provider.ErrRateLimited):
		return c.failWithIDRetryable(format, operationID, "RATE_LIMITED", err, 6, true)
	case errors.Is(err, provider.ErrUnsupported):
		return c.failWithIDRetryable(format, operationID, "UNSUPPORTED_CAPABILITY", err, 6, false)
	case errors.Is(err, provider.ErrPreconditionFailed):
		return c.failWithIDRetryable(format, operationID, "STATE_CONFLICT", err, 5, false)
	default:
		return c.failWithIDRetryable(format, operationID, "PROVIDER_UNAVAILABLE", err, 6, true)
	}
}

func (c *cli) workflowFailure(format, operationID string, err error) int {
	switch {
	case errors.Is(err, workflow.ErrInvalidInput):
		return c.failWithID(format, operationID, "INVALID_ARGUMENT", err, 2)
	case errors.Is(err, provider.ErrNotFound):
		return c.failWithID(format, operationID, "NOT_FOUND", err, 4)
	case errors.Is(err, workflow.ErrStateConflict):
		return c.failWithID(format, operationID, "STATE_CONFLICT", err, 5)
	case errors.Is(err, workflow.ErrLeaseConflict), errors.Is(err, workflow.ErrLeaseExpired):
		return c.failWithID(format, operationID, "LEASE_CONFLICT", err, 5)
	default:
		return c.providerFailureWithID(format, operationID, err)
	}
}

func (c *cli) success(format string, data any, text string, warnings ...string) int {
	return c.successWithID(format, operationID(), data, text, warnings...)
}

func (c *cli) successWithID(format, operationID string, data any, text string, warnings ...string) int {
	if format == "json" {
		_ = output.JSON(c.stdout, output.Envelope{
			OK: true, OperationID: operationID, Data: data, Warnings: warnings,
		})
	} else {
		if text != "" {
			fmt.Fprintln(c.stdout, text)
		}
		for _, w := range warnings {
			fmt.Fprintln(c.stderr, "issue-flow: warning: "+w)
		}
	}
	return 0
}

func (c *cli) fail(format, code string, err error, exitCode int) int {
	return c.failWithID(format, operationID(), code, err, exitCode)
}

func (c *cli) failWithID(format, operationID, code string, err error, exitCode int) int {
	return c.failWithIDRetryable(format, operationID, code, err, exitCode, false)
}

func (c *cli) failWithIDRetryable(format, operationID, code string, err error, exitCode int, retryable bool) int {
	if format == "json" {
		_ = output.JSON(c.stdout, output.Envelope{
			OK: false, OperationID: operationID,
			Error: &output.Error{Code: code, Message: err.Error(), Retryable: retryable},
		})
	} else {
		fmt.Fprintf(c.stderr, "issue-flow: %s\n", err)
	}
	return exitCode
}

func (c *cli) usage(w io.Writer) {
	fmt.Fprintln(w, `Issue Flow

Usage:
  issue-flow [global flags] <command> [command flags]

Commands:
  init       Create a safe default configuration
  doctor     Validate configuration and inspect provider capabilities
  create     Create a ready bug, feature, or improvement issue
  adopt      Bring an unmanaged issue (no workflow label) into ready
  comment    Add a plain-text comment to an issue (no lease required)
  complete   Record human review and move a review Issue to done
  list       List issues
  show       Show one issue
  context    Build normalized agent context
  claim      Claim a ready issue
  start      Move a claimed issue to working
  heartbeat  Renew a held lease
  progress   Record progress without changing state
  block      Move a working issue to blocked
  release    Release a held lease back to ready
  reclaim    Recover an expired lease
  finish     Deliver evidence and move to the configured finish state
  version    Print version and protocol information
  capabilities Print supported commands and feature flags

Global flags (accepted before or after the command):
  --config <path>
  --project <path>
  --env-file <path>
  --format text|json
  --json
  --dry-run
  --verbose`)
}

func parseGlobals(args []string) (globals, []string, error) {
	g := globals{format: "text"}
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			g.format = "json"
		case arg == "--dry-run":
			g.dryRun = true
		case arg == "--verbose":
			g.verbose = true
		case arg == "--config" || arg == "--project" || arg == "--env-file" || arg == "--format":
			if i+1 >= len(args) {
				return g, nil, fmt.Errorf("%s requires a value", arg)
			}
			i++
			switch arg {
			case "--config":
				g.config = args[i]
			case "--project":
				g.project = args[i]
			case "--env-file":
				g.envFile = args[i]
			case "--format":
				g.format = args[i]
			}
		case strings.HasPrefix(arg, "--config="):
			g.config = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "--project="):
			g.project = strings.TrimPrefix(arg, "--project=")
		case strings.HasPrefix(arg, "--env-file="):
			g.envFile = strings.TrimPrefix(arg, "--env-file=")
		case strings.HasPrefix(arg, "--format="):
			g.format = strings.TrimPrefix(arg, "--format=")
		default:
			remaining = append(remaining, arg)
		}
	}
	if g.format != "text" && g.format != "json" {
		return g, nil, fmt.Errorf("unsupported format %q", g.format)
	}
	return g, remaining, nil
}

func operationID() string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "op_unknown"
	}
	return "op_" + hex.EncodeToString(value[:])
}

func validOperationID(value string) bool {
	if !strings.HasPrefix(value, "op_") || len(value) < 4 || len(value) > 67 {
		return false
	}
	for _, r := range value[3:] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}
