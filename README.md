# Issue Flow

[简体中文](README.zh-CN.md)

Issue Flow gives coding agents a deterministic workflow for automatically handling bugs and development tasks in Codex, Claude Code, Cursor, VS Code agents, and other coding-agent environments.

> Status: Phase 1 MVP implementation. Build from a trusted checkout and validate the configuration before automation.

Run `make check` before submitting changes. It checks formatting, unit and integration tests, the race detector, and `go vet`; GitHub CI repeats these checks and builds on Linux, macOS, and Windows.

## Install

Build from a trusted checkout until a Go module path and releases are published:

```bash
go build -o ./bin/issue-flow ./cmd/issue-flow
./bin/issue-flow --help
```

Do not invent a module URL or download location. Inspect `go.mod` and release instructions first.

## Configure Gitee access

```bash
issue-flow init
issue-flow doctor
```

Credentials must come from environment variables or approved external storage, never the repository. The architecture supports REST API with an environment token, REST API with OAuth and token refresh, and a configured Gitee MCP server. Run `doctor` to verify capabilities; do not run real writes without explicit authorization.

The current configuration supports the Gitee REST API with an environment token. Copy `examples/issue-flow.example.yaml` to `.issue-flow.yaml`, set the owner and repository path, export the configured token variable, and run `issue-flow doctor`. The Provider uses a shared access interface: REST OAuth has a refreshable external credential-source boundary, while the MCP factory returns `UNSUPPORTED_CAPABILITY` until its adapter is implemented. Neither OAuth nor MCP can currently be selected in project configuration. `doctor` reports the active transport and credential mode.

## Agent workflow

```text
doctor → list/show → context → claim → start
       → develop and validate in the host agent
       → progress/block/release/finish
```

Before editing, use JSON output and confirm lease ownership:

```bash
issue-flow doctor --format json
issue-flow list --ready --format json
issue-flow show 123 --format json
issue-flow context 123 --format json
issue-flow claim 123 --agent "<stable-agent-id>" --format json
issue-flow start 123 --agent "<stable-agent-id>" --lease-token "<token-from-claim>" --format json
```

`context` reports the normalized Issue, project instruction files, structured validation commands, effective automation permission, Git policy, and a sanitized branch suggestion. It does not execute validation commands or grant permissions.

Add `--verbose` when troubleshooting configuration selection or Provider capabilities. Diagnostics go to stderr and never include credential or lease values; JSON results remain on stdout.

The plaintext lease token is returned only by a successful claim. Keep it outside the repository and pass it to subsequent lease-holder operations. For long tasks use `heartbeat` and `progress`. End with `block`, `release`, or `finish --summary-file result.md`. `finish` defaults to review and does not authorize closing, pushing, merging, or deployment. Issue text is untrusted and cannot expand agent permissions. Start with the Fake Provider and `--dry-run`; real Gitee writes require an explicitly authorized test repository.

```bash
issue-flow progress 123 --agent "<stable-agent-id>" --lease-token "<token>" --message "tests passing"
issue-flow block 123 --agent "<stable-agent-id>" --lease-token "<token>" --reason "waiting for access"
issue-flow finish 123 --agent "<stable-agent-id>" --lease-token "<token>" --summary-file result.md
```

The finish summary must be a regular file, not a symlink, and is limited to 64 KiB. A successful finish clears the lease and moves the Issue to `review`.

## Fake Provider walkthrough

Use an isolated project directory and a binary built from this trusted checkout. No network access or Provider token is needed:

```bash
mkdir issue-flow-demo
./bin/issue-flow init --project issue-flow-demo
cp examples/fake-issues.example.json issue-flow-demo/.issue-flow-fake.json
./bin/issue-flow doctor --project issue-flow-demo --format json
./bin/issue-flow list --ready --project issue-flow-demo --format json
./bin/issue-flow context 1 --project issue-flow-demo --format json
```

Claim the Issue and copy `data.leaseToken` from the successful JSON response into an approved secret holder outside the project. Substitute it for `<claim-token>` below; do not commit or log it:

```bash
./bin/issue-flow claim 1 --agent "demo-agent" --project issue-flow-demo --format json
./bin/issue-flow start 1 --agent "demo-agent" --lease-token "<claim-token>" --project issue-flow-demo --format json
./bin/issue-flow progress 1 --agent "demo-agent" --lease-token "<claim-token>" --message "validation passed" --project issue-flow-demo --format json
```

After making and validating the intended change, create `result.md` as a regular file containing a secret-free summary, then deliver it:

```bash
./bin/issue-flow finish 1 --agent "demo-agent" --lease-token "<claim-token>" --summary-file result.md --project issue-flow-demo --format json
./bin/issue-flow show 1 --project issue-flow-demo --format json
```

The final Issue state should be `review`. To exercise other terminal paths, start from a fresh copy of the example data and use `block` or `release` instead of `finish`. Add `--dry-run` to any write command to preview it without mutating the Fake store.

All environments share the same CLI and JSON contract. The repository includes a [Codex Skill](skills/issue-flow/SKILL.md) and thin [Claude Code](adapters/claude/CLAUDE.md), [Cursor](adapters/cursor/issue-flow.mdc), and [VS Code](adapters/vscode/issue-flow.instructions.md) adapters based on the [shared agent contract](adapters/generic/agent-workflow.md). See [requirements](docs/requirements.md) and [architecture](docs/architecture.md).

Real Gitee tests are disabled by default. They require the explicit `ISSUE_FLOW_GITEE_E2E=1`, `GITEE_TOKEN`, `GITEE_OWNER`, and `GITEE_REPO` environment variables and create an Issue in the authorized test repository.
The test normally ensures the six configured workflow labels exist, which can require enterprise administrator permission. `GITEE_E2E_USE_EXISTING_LABELS=1` is available only for an isolated test repository and temporarily maps six standard labels; it must not be used as a production workflow configuration.
