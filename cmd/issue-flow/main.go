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
	"strconv"
	"strings"

	"issue-flow/internal/app"
	"issue-flow/internal/config"
	"issue-flow/internal/domain"
	"issue-flow/internal/output"
	"issue-flow/internal/provider"
)

const version = "0.1.0-dev"

type cli struct {
	stdout io.Writer
	stderr io.Writer
}

type globals struct {
	config  string
	project string
	format  string
	dryRun  bool
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
		fmt.Fprintln(c.stdout, version)
		return 0
	case "init":
		return c.init(g, args[1:])
	case "doctor":
		return c.doctor(ctx, g, args[1:])
	case "list":
		return c.list(ctx, g, args[1:])
	case "show":
		return c.show(ctx, g, args[1:])
	default:
		return c.fail(g.format, "INVALID_ARGUMENT", fmt.Errorf("unknown command %q", args[0]), 2)
	}
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
	result := runtime.Doctor(ctx)
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
	number, err := strconv.Atoi(args[0])
	if err != nil || number <= 0 {
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
	text := fmt.Sprintf("#%d %s\nState: %s\nURL: %s\n\n%s",
		issue.Number, issue.Title, issue.WorkflowState, issue.URL, issue.Body)
	return c.success(g.format, issue, text)
}

func (c *cli) open(g globals) (*app.Runtime, int) {
	runtime, err := app.Open(g.config, g.project)
	if err != nil {
		return nil, c.fail(g.format, "CONFIG_ERROR", err, 2)
	}
	return runtime, 0
}

func (c *cli) providerFailure(format string, err error) int {
	if errors.Is(err, provider.ErrNotFound) {
		return c.fail(format, "NOT_FOUND", err, 4)
	}
	return c.fail(format, "PROVIDER_UNAVAILABLE", err, 6)
}

func (c *cli) success(format string, data any, text string) int {
	if format == "json" {
		_ = output.JSON(c.stdout, output.Envelope{
			OK: true, OperationID: operationID(), Data: data,
		})
	} else if text != "" {
		fmt.Fprintln(c.stdout, text)
	}
	return 0
}

func (c *cli) fail(format, code string, err error, exitCode int) int {
	if format == "json" {
		_ = output.JSON(c.stdout, output.Envelope{
			OK: false, OperationID: operationID(),
			Error: &output.Error{Code: code, Message: err.Error()},
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
  list       List issues
  show       Show one issue
  version    Print version

Global flags (accepted before or after the command):
  --config <path>
  --project <path>
  --format text|json
  --json
  --dry-run`)
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
		case arg == "--config" || arg == "--project" || arg == "--format":
			if i+1 >= len(args) {
				return g, nil, fmt.Errorf("%s requires a value", arg)
			}
			i++
			switch arg {
			case "--config":
				g.config = args[i]
			case "--project":
				g.project = args[i]
			case "--format":
				g.format = args[i]
			}
		case strings.HasPrefix(arg, "--config="):
			g.config = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "--project="):
			g.project = strings.TrimPrefix(arg, "--project=")
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
