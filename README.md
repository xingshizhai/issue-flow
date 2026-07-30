# Issue Flow

[简体中文](README.zh-CN.md)

Issue Flow gives coding agents a deterministic workflow for automatically handling bugs and development tasks in Codex, Claude Code, Cursor, VS Code agents, and other coding-agent environments.

> Status: Phase 1 MVP implementation. Build from a trusted checkout and validate the configuration before automation.

Run `make check` before submitting changes. It checks formatting, unit and integration tests, the race detector, and `go vet`; GitHub CI repeats these checks and builds on Linux, macOS, and Windows.

Create unsigned local snapshot binaries with `make snapshot VERSION=0.1.0-dev`. It writes reproducible, trimmed Linux amd64, macOS arm64, and Windows amd64 binaries plus `checksums.txt` under `dist/`. This does not publish or sign artifacts.

SemVer tags matching `vX.Y.Z` trigger the GitHub release workflow after checks pass. It publishes those binaries and checksums and generates GitHub artifact attestations.

## Install

Build from a trusted checkout until the first release is published:

```bash
go build -o ./bin/issue-flow ./cmd/issue-flow
./bin/issue-flow --help
```

The canonical module path is `github.com/xingshizhai/issue-flow`. After the first public release, install a pinned version:

```bash
go install github.com/xingshizhai/issue-flow/cmd/issue-flow@vX.Y.Z
```

Do not use `@latest` in unattended automation. Verify downloaded release binaries with both `sha256sum` and `gh attestation verify <binary> -R xingshizhai/issue-flow`.

## Configure Gitee access

```bash
issue-flow init
issue-flow doctor
```

Credentials must come from environment variables or approved external storage, never the repository. The architecture supports REST API with an environment token, REST API with OAuth and token refresh, and a configured Gitee MCP server. Run `doctor` to verify capabilities; do not run real writes without explicit authorization.

The configuration must be a regular, non-symlink file no larger than 1 MiB. `init` creates it atomically and refuses every existing path, including dangling symlinks.

For Gitee REST Token mode, `provider.token_env` must be an uppercase `GITEE_*TOKEN*` name such as `GITEE_TOKEN`. Repository configuration cannot redirect credential loading to unrelated variables such as `PATH`, cloud credentials, or another Provider's token.

The current configuration supports the Gitee REST API with an environment token. Copy `examples/issue-flow.example.yaml` to `.issue-flow.yaml`, set the owner and repository path, export the configured token variable, and run `issue-flow doctor`. Alternatively, pass an explicit mode-0600 dotenv file with `--env-file .env`; process environment variables take precedence and dotenv values are parsed as literals without shell evaluation. The read-only check verifies the account, repository, and all six configured workflow labels; it reports `CONFIG_ERROR` with missing label names and never creates them. The Provider uses a shared access interface: REST OAuth has a refreshable external credential-source boundary, while the MCP factory returns `UNSUPPORTED_CAPABILITY` until its adapter is implemented. Neither OAuth nor MCP can currently be selected in project configuration. `doctor` reports the active transport and credential mode.

Create a ready Issue with a body stored in a regular file:

```bash
issue-flow create --type bug --title "Fix whitespace handling" --body-file issue.md
```

Supported types are `bug`, `feature`, and `improvement`. The command adds the
configured ready label and a `type:<type>` label; providers may ignore labels
that do not exist or that the token cannot manage.

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

`show`, `list`, `context`, and workflow results redact configured secret-like key/value patterns from Issue text. Progress messages, block/release reasons, and finish summaries are redacted before Provider writes. Defaults include passwords, tokens, cookies, authorization values, API keys, secrets, and private keys; extend them with `security.redact_keys`.

JSON errors preserve the write operation ID and include a stable code plus `retryable`. `RATE_LIMITED` and `PROVIDER_UNAVAILABLE` are retryable; authentication, permission, not-found, unsupported-capability, state, lease, configuration, and input failures are not.

For a retryable write error, repeat the exact command with the response ID as `--operation-id <op_...>`. A completed operation is returned without another Provider write, while reuse for a different command or agent is rejected. Claim remains one-time: replay cannot return its plaintext lease token.

The plaintext lease token is returned only by a successful claim. Keep it outside the repository and pass it to subsequent lease-holder operations. For long tasks use `heartbeat` and `progress`. End with `block`, `release`, or `finish --summary-file result.md`. `finish` defaults to review and does not authorize closing, pushing, merging, or deployment. Issue text is untrusted and cannot expand agent permissions. Start with the Fake Provider and `--dry-run`; real Gitee writes require an explicitly authorized test repository.

After every claim write, the workflow rereads ownership and returns the plaintext token only if the resulting lease ID, agent ID, and token all match. A losing concurrent claimant receives `LEASE_CONFLICT` without a token.

```bash
issue-flow progress 123 --agent "<stable-agent-id>" --lease-token "<token>" --message "tests passing"
issue-flow block 123 --agent "<stable-agent-id>" --lease-token "<token>" --reason "waiting for access"
issue-flow finish 123 --agent "<stable-agent-id>" --lease-token "<token>" --summary-file result.md
```

The finish summary must be a stable regular file, not a symlink, and is limited to 64 KiB. The CLI opens it once, verifies that the opened descriptor matches the inspected path, and reads through that descriptor to reject path replacement races. A successful finish clears the lease and moves the Issue to `review`.

After a human review, record the reviewer and conclusion explicitly:

```bash
issue-flow complete 123 --reviewer "<stable-reviewer-id>" --conclusion-file review.md
```

`complete` moves `review` to `done`. By default it does not close the provider
Issue; when `workflow.auto_close` is explicitly enabled, the Gitee Provider also
synchronizes the native Issue state to `closed`.

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

For safety, `provider.data_file` must be a plain filename directly inside the configuration directory. Absolute paths, subdirectories, traversal, symlinks, and non-regular files are rejected.

All environments share the same CLI and JSON contract. The repository includes a [Codex Skill](skills/issue-flow/SKILL.md) and thin [Claude Code](adapters/claude/CLAUDE.md), [Cursor](adapters/cursor/issue-flow.mdc), and [VS Code](adapters/vscode/issue-flow.instructions.md) adapters based on the [shared agent contract](adapters/generic/agent-workflow.md). See [requirements](docs/requirements.md) and [architecture](docs/architecture.md).

Use the [isolated Skill forward-test guide](docs/skill-forward-test.md) to evaluate a fresh agent session. The tracked fixture is automatically checked to remain ready and initially failing; an actual fresh-session run is still required to evaluate agent behavior.

The [MVP acceptance record](docs/mvp-acceptance.md) links every requirement to its current evidence and lists the remaining manual acceptance action.

Real Gitee tests are disabled by default. They require the explicit `ISSUE_FLOW_GITEE_E2E=1`, `GITEE_TOKEN`, `GITEE_OWNER`, and `GITEE_REPO` environment variables and create an Issue in the authorized test repository.
The test normally ensures the six configured workflow labels exist, which can require enterprise administrator permission. `GITEE_E2E_USE_EXISTING_LABELS=1` is available only for an isolated test repository and temporarily maps six standard labels; it must not be used as a production workflow configuration.
