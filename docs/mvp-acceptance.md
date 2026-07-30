# MVP acceptance record

Last updated: 2026-07-30

This record maps the acceptance criteria in
[`requirements.md`](requirements.md) to reproducible evidence. Do not mark a
criterion complete solely because implementation code exists.

| # | Criterion | Status | Evidence |
|---:|---|---|---|
| 1 | `issue-flow init` creates valid configuration in an empty directory | Automated | `TestInitDoctorAndListJSON`, `TestWriteDefaultAndLoad` |
| 2 | Fake Provider completes `ready → claimed → working → review` | Automated | `TestProgressBlockAndFinishWorkflow` |
| 3 | At most one of two concurrent claims succeeds | Automated | `TestConcurrentUpdateHasOneWinner`, `TestClaimRejectsTokenWhenAnotherClaimWinsConvergence` |
| 4 | A non-holder cannot heartbeat, release, block, or finish | Automated | CLI authorization table in `TestLeaseWorkflowAndAuthorization` and workflow transition coverage |
| 5 | An expired lease can be reclaimed explicitly | Automated | `TestReclaimExpiredLease` |
| 6 | Every write command supports dry-run | Automated | CLI dry-run integration coverage and workflow dry-run tests |
| 7 | Main commands support JSON and stable exit codes | Automated | CLI envelope, command, Provider mapping, and retryability tests |
| 8 | Provider credentials and stored token hashes do not appear in logs or ordinary command output | Automated | lease-publication, transport-error, Provider-error, and persisted-redaction tests; the one-time plaintext lease token is intentionally returned only by successful claim |
| 9 | Gitee Provider completes an authorized repository flow | Manually verified | Gitee test repository `beijing-tongwei/test-use`, Issue `IK59IW`; explicit E2E remains environment-gated |
| 10 | Codex follows the Skill through a Fake Provider workflow | Ready, not executed | Tracked fixture and integrity test exist; run [`skill-forward-test.md`](skill-forward-test.md) in a genuinely fresh Agent session |
| 11 | English and Chinese READMEs guide setup and Fake workflow | Automated | `TestReadmesDocumentCompleteFakeWorkflow` |
| 12 | REST Token, REST OAuth, and MCP share capability/test boundaries | Automated | `Transport`, `Credential`, `OAuthCredentialSource`, access capability tests, and explicit MCP unsupported result |

## Required checks

Run before accepting a change:

```bash
make check
make snapshot VERSION=0.1.0-dev
```

Real Gitee tests remain opt-in and require an explicitly authorized test
repository. Never put credentials or lease tokens in this record.

## Remaining acceptance action

Criterion 10 is the only criterion not yet executed. It must use a fresh agent
session that has not inspected the fixture implementation. Record the date,
agent product/version, result, and a secret-free observation below.

| Date | Agent | Result | Observation |
|---|---|---|---|
| — | — | Pending | Isolated fixture is ready; no fresh-session result recorded |

## Release decisions outside functional MVP acceptance

Before a public release, decide and document:

- the canonical Go module path;
- the release host and tag policy;
- artifact signing and provenance;
- the supported OS and architecture matrix.

Snapshot builds are local, unsigned, and unpublished until those decisions are
made.
