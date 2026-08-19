# MVP acceptance record

Last updated: 2026-07-30

This record maps the acceptance criteria in
[`requirements.md`](requirements.md) to reproducible evidence. Do not mark a
criterion complete solely because implementation code exists.

| # | Criterion | Status | Evidence |
|---:|---|---|---|
| 1 | `issue-flow init` creates valid configuration in an empty directory | Automated | `TestInitDoctorAndListJSON`, `TestWriteDefaultAndLoad` |
| 2 | Fake Provider completes `ready → claimed → working → review → done` | Automated | `TestProgressBlockAndFinishWorkflow` |
| 3 | At most one of two concurrent claims succeeds | Automated | `TestConcurrentUpdateHasOneWinner`, `TestClaimRejectsTokenWhenAnotherClaimWinsConvergence` |
| 4 | A non-holder cannot heartbeat, release, block, or finish | Automated | CLI authorization table in `TestLeaseWorkflowAndAuthorization` and workflow transition coverage |
| 5 | An expired lease can be reclaimed explicitly | Automated | `TestReclaimExpiredLease` |
| 6 | Every write command supports dry-run | Automated | CLI dry-run integration coverage and workflow dry-run tests |
| 7 | Main commands support JSON and stable exit codes | Automated | CLI envelope, command, Provider mapping, and retryability tests |
| 8 | Provider credentials and stored token hashes do not appear in logs or ordinary command output | Automated | lease-publication, transport-error, Provider-error, and persisted-redaction tests; the one-time plaintext lease token is intentionally returned only by successful claim |
| 9 | Gitee Provider completes an authorized repository flow | Manually verified | Gitee test repository `beijing-tongwei/test-use`, Issues `IK5A0U` and `IK5A0V`; both completed `ready → claimed → working → review → done`, and explicit `auto_close` synchronized Gitee native state from `意向` to `已完成`, with real REST writes on 2026-07-30 |
| 10 | Gitee create maps portable type to native enterprise type | Manually verified | Test Issue `IK5AFH` was created by `--type bug` as native `缺陷`, completed through the full flow, and closed as `已验收` on 2026-07-30 |
| 11 | Codex follows the Skill through a Fake Provider workflow | Manually verified | A genuinely fresh Codex CLI session passed [`skill-forward-test.md`](skill-forward-test.md) on 2026-07-30 after two safety-preserving iterations improved claim-token handling |
| 12 | English and Chinese READMEs guide setup and Fake workflow | Automated | `TestReadmesDocumentCompleteFakeWorkflow` |
| 13 | REST Token, REST OAuth, and MCP share capability/test boundaries | Automated | `Transport`, `Credential`, `OAuthCredentialSource`, access capability tests, and explicit MCP unsupported result |

## Required checks

Run before accepting a change:

```bash
make check
make snapshot VERSION=0.2.0-dev
```

Real Gitee tests remain opt-in and require an explicitly authorized test
repository. Never put credentials or lease tokens in this record.

## Skill forward-test result

The result below contains no transcript, claim token, token hash, or temporary
secret path.

| Date | Agent | Result | Observation |
|---|---|---|---|
| 2026-07-30 | Codex CLI 0.146.0 | Pass | Fresh session read project and Skill instructions, performed `doctor/list/show/context`, claimed and started before the one-line fix, ran `go test ./...`, finished to `review` with no lease, cleaned protected temporary files, and made no Git or network write |

The current development session has inspected the fixture and cannot provide a
valid forward-test result. Do not replace this requirement with another local
CLI integration test.

## Release baseline

The canonical module path is `github.com/xingshizhai/issue-flow`. SemVer tags
matching `vX.Y.Z` publish Linux amd64, macOS arm64, and Windows amd64 binaries
with SHA-256 checksums and GitHub artifact attestations.

No tag or release is created by ordinary development commits. Publishing still
requires an explicit authorized tag push.
