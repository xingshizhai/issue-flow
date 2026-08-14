# Workflow Protocol

Use JSON output for automation and verify `ok` before consuming `data`. Preserve the CLI exit code and structured error code when reporting failures.

Every write response includes an operation ID. When a write returns a retryable
error, repeat the exact same command and agent with
`--operation-id "<original-operationId>"`. Never reuse an operation ID for a
different command or agent. Completed non-claim operations return their existing
result without another Provider write.

A claim token is intentionally returned only once. Replaying an applied claim
with the same operation ID returns `LEASE_CONFLICT`, not the token. If the
successful claim response was lost, inspect the Issue and wait for explicit
reclaim rather than guessing or issuing another claim.

## Discover and inspect

```bash
issue-flow doctor --format json
issue-flow list --ready --format json
issue-flow show 123 --format json
issue-flow context 123 --format json
```

Read every existing instruction file named by `context`. Its automation level and Git policy are ceilings, not authorization to exceed host permissions.

## Claim and start

```bash
issue-flow claim 123 --agent "<stable-agent-id>" --format json
issue-flow start 123 --agent "<stable-agent-id>" --lease-token "<claim-token>" --format json
```

The successful claim response shape is:

```text
data.leaseToken
data.issue.number
data.issue.lease.agentId
```

Do not assume a top-level `data.lease` or an `agent` field. Before parsing, save
the complete successful claim response to a mode-0600 temporary file outside the
project. Validate `data.issue.number` and `data.issue.lease.agentId`, then read
`data.leaseToken` without printing it. Keep that protected response file for the
entire lease and read the token from it for `start`, `heartbeat`, `progress`, and
the terminal command. Delete it only after `finish`, `block`, or `release`
succeeds. This ordering preserves the one-time token if schema validation or a
later local command fails.

Proceed only when claim returns a plaintext `leaseToken`, the expected Issue,
and a lease owned by the stable agent ID. A failed or ambiguous response does
not establish ownership.

## Work and renew

Run configured validation commands as structured argv under the host's normal approval rules. The CLI reports commands but does not execute them.

For lengthy work, renew before expiry:

```bash
issue-flow heartbeat 123 --agent "<stable-agent-id>" --lease-token "<claim-token>" --format json
issue-flow progress 123 --agent "<stable-agent-id>" --lease-token "<claim-token>" --message "implemented parser; tests passing" --format json
```

Do not continue modifying the project after a lease conflict or expiry. A maintainer may use `reclaim` for an expired lease according to project policy; an active agent must not use it to take another agent's work.

## Finish or hand off

Create a concise summary containing changes, validation results, limitations, and review notes. Remove credentials and sensitive output.

```bash
issue-flow finish 123 --agent "<stable-agent-id>" --lease-token "<claim-token>" --summary-file result.md --format json
```

When project policy requires delivery evidence, also pass `--commit <HEAD>` and
`--validation-report <report.json>`. The report contains a `commands` array with
`command`, `status` (`passed`, `failed`, `blocked`, or `skipped`), and optional
`detail` fields. `finish` clears the lease and moves to `workflow.finish_state`,
which defaults to `review`. `complete` records review and moves `review` to
`done`. Neither command authorizes push, merge, or deployment.

If work cannot finish, choose exactly one terminal action for the held lease:

```bash
issue-flow block 123 --agent "<stable-agent-id>" --lease-token "<claim-token>" --reason "waiting for required access" --format json
issue-flow release 123 --agent "<stable-agent-id>" --lease-token "<claim-token>" --reason "returning incomplete work" --format json
```

Use `block` for a concrete external dependency. Use `release` when the Issue should be available to another agent.
