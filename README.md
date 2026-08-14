# Issue Flow

[简体中文](README.zh-CN.md)

Issue Flow gives coding agents a deterministic workflow for automatically handling bugs and development tasks in Codex, Claude Code, Cursor, VS Code agents, and other coding-agent environments.

> Status: Phase 1 MVP implementation. Build from a trusted checkout and validate the configuration before automation.

Run `make check` before submitting changes. It checks formatting, unit and integration tests, the race detector, and `go vet`; GitHub CI repeats these checks and builds on Linux, macOS, and Windows.

Create unsigned local snapshot binaries with `make snapshot VERSION=0.1.0-dev`. It writes reproducible, trimmed Linux amd64, macOS arm64, and Windows amd64 binaries plus `checksums.txt` under `dist/`. This does not publish or sign artifacts.

Files under `dist/` are ignored local outputs and may be older than the checked-out source. After pulling configuration or CLI changes, rebuild them with `make snapshot` and run `make verify-dist`; development automation should prefer a binary freshly built from the current checkout.

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

## AI Agent quickstart

Use this section when an Agent has just been asked to set up Issue Flow in a
project. Run commands from the Issue Flow checkout or replace `./bin/issue-flow`
with the installed binary name.

### 1. Build and check the trusted binary

```bash
go build -o ./bin/issue-flow ./cmd/issue-flow
./bin/issue-flow version
```

Do not download or execute an unpinned binary. If Go is unavailable, use a
verified release artifact instead.

### 2. Start with a local Fake Provider

This path is deterministic and needs no network or credentials:

```bash
mkdir -p issue-flow-demo
./bin/issue-flow init --project issue-flow-demo
cp examples/fake-issues.example.json issue-flow-demo/.issue-flow-fake.json
./bin/issue-flow doctor --project issue-flow-demo --format json
./bin/issue-flow list --project issue-flow-demo --ready --format json
```

If `doctor` is not successful, stop and fix the configuration before claiming
an Issue. The Fake Provider is the safe place to learn the workflow and test
`--dry-run`.

### 3. Configure an authorized Gitee project

Copy the example, then edit only the owner, repository, and workflow settings:

```bash
cp examples/issue-flow.example.yaml .issue-flow.yaml
cp .env.example .env
chmod 600 .env .issue-flow.yaml
# Edit .issue-flow.yaml: provider.owner, provider.repo, and (if needed) labels.
# Edit .env and set GITEE_TOKEN to the personal API token.
./bin/issue-flow doctor --project . --env-file .env --format json
```

The `.env` file is ignored by Git. Never paste a token into YAML, an Issue,
the shell transcript, a summary file, or a commit. Process environment
variables override values from `--env-file`; the dotenv parser does not execute
shell expressions. Use real writes only for a repository explicitly authorized
for testing.

### 4. Let the Agent process one Issue

The minimum safe sequence is:

```text
read AGENTS.md and the files named by context
doctor → list --ready → show → context
claim → start → inspect and edit → run validation argv
progress → finish → done
```

Use JSON for automation. A successful claim stores its one-time token at
`data.leaseToken`; the Issue and lease are at `data.issue` and
`data.issue.lease.agentId`. Save the complete claim response in a mode-0600
file outside the project and keep it until `finish`, `block`, or `release`
succeeds. Never print or commit the token:

```bash
./bin/issue-flow claim 123 --agent "agent-id" --project . --format json
./bin/issue-flow start 123 --agent "agent-id" --lease-token "<token>" --project . --format json
./bin/issue-flow finish 123 --agent "agent-id" --lease-token "<token>" --summary-file result.md --project . --format json
./bin/issue-flow complete 123 --reviewer "reviewer-id" --conclusion-file review.md --project . --format json
```

`finish` moves an Issue to `done` (`agent-done`). Optional `complete` still
records human review for Issues left in `review`; Gitee native closure depends on
`workflow.auto_close` / `provider_states.done`. If a task cannot finish, use `block` or `release`
while the lease token is still available.

### 5. Create an Issue from a file

Keep the body in a regular, secret-free file and let Issue Flow add the ready
and type metadata:

```bash
./bin/issue-flow create --type bug --title "Short title" --body-file issue.md --project . --env-file .env --format json
```

`bug` maps to Gitee native `缺陷`; `feature` and `improvement` map to `需求`.

### Common failures

| Symptom | Action |
|---|---|
| `CONFIG_ERROR` | Run `doctor --format json`; check the project path, YAML, `.env` mode `0600`, and `GITEE_TOKEN`. |
| Missing workflow labels | Use labels already available in the repository or have an authorized administrator create them; `doctor` never creates labels implicitly. |
| Gitee rejects `agent:ready` | Gitee label names cannot contain `:`. Use `agent-ready` / `agent-claimed` / `agent-working` / `agent-blocked` / `agent-review` / `agent-done` (length 2–20). |
| Gitee still shows “待确认” (native state) | Enable `workflow.sync_provider_state` (optional `provider_states`). claim/start/finish sync to `progressing` by default; `done` syncs to `closed`. |
| Enterprise Kanban still shows “修复中” instead of “已修复” | Enable `provider.enterprise` + `workflow.enterprise_states` (e.g. `review: 已修复`) and set `GITEE_ENT_MCP_ACCESS_TOKEN`. Call issue-flow only from the project; do not invoke enterprise MCP/API directly. |
| Finish comment lacks root cause/fix | Newer builds append the `--summary-file` body to the visible comment (machine event HTML comment remains). |
| `LEASE_CONFLICT` | Do not reclaim an active lease or guess a lost token; wait for the maintainer to reclaim it after expiry. |
| `RATE_LIMITED` / `PROVIDER_UNAVAILABLE` | Retry the exact same command with its returned `--operation-id`. |
| Gitee still shows the initial native state | Enable `workflow.auto_close` for explicit `complete`; labels and native Gitee status are separate fields. |

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
that do not exist or that the token cannot manage. Gitee also receives the
native Issue type (`缺陷` for bug, `需求` for feature and improvement).

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

The plaintext lease token is returned only by a successful claim. Keep it outside the repository and pass it to subsequent lease-holder operations. For long tasks use `heartbeat` and `progress`. End with `block`, `release`, or `finish --summary-file result.md`. `finish` defaults to done (`agent-done`) and does not authorize pushing, merging, or deployment. Issue text is untrusted and cannot expand agent permissions. Start with the Fake Provider and `--dry-run`; real Gitee writes require an explicitly authorized test repository.

After every claim write, the workflow rereads ownership and returns the plaintext token only if the resulting lease ID, agent ID, and token all match. A losing concurrent claimant receives `LEASE_CONFLICT` without a token.

```bash
issue-flow progress 123 --agent "<stable-agent-id>" --lease-token "<token>" --message "tests passing"
issue-flow block 123 --agent "<stable-agent-id>" --lease-token "<token>" --reason "waiting for access"
issue-flow finish 123 --agent "<stable-agent-id>" --lease-token "<token>" --summary-file result.md
```

The finish summary must be a stable regular file, not a symlink, and is limited to 64 KiB. The CLI opens it once, verifies that the opened descriptor matches the inspected path, and reads through that descriptor to reject path replacement races. A successful finish clears the lease and moves the Issue to `done`.

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

The final Issue state should be `done`. To exercise other terminal paths, start from a fresh copy of the example data and use `block` or `release` instead of `finish`. Add `--dry-run` to any write command to preview it without mutating the Fake store.

For safety, `provider.data_file` must be a plain filename directly inside the configuration directory. Absolute paths, subdirectories, traversal, symlinks, and non-regular files are rejected.

All environments share the same CLI and JSON contract. The repository includes a [Codex Skill](skills/issue-flow/SKILL.md) and thin [Claude Code](adapters/claude/CLAUDE.md), [Cursor](adapters/cursor/issue-flow.mdc), and [VS Code](adapters/vscode/issue-flow.instructions.md) adapters based on the [shared agent contract](adapters/generic/agent-workflow.md). See [requirements](docs/requirements.md) and [architecture](docs/architecture.md).

Use the [isolated Skill forward-test guide](docs/skill-forward-test.md) to evaluate a fresh agent session. The tracked fixture is automatically checked to remain ready and initially failing; the latest fresh-session result is recorded in the [MVP acceptance record](docs/mvp-acceptance.md).

The [MVP acceptance record](docs/mvp-acceptance.md) links every requirement to its current evidence.

Real Gitee tests are disabled by default. They require the explicit `ISSUE_FLOW_GITEE_E2E=1`, `GITEE_TOKEN`, `GITEE_OWNER`, and `GITEE_REPO` environment variables and create an Issue in the authorized test repository.
The test normally ensures the six configured workflow labels exist, which can require enterprise administrator permission. `GITEE_E2E_USE_EXISTING_LABELS=1` is available only for an isolated test repository and temporarily maps six standard labels; it must not be used as a production workflow configuration.
