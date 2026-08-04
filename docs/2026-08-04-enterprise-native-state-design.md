# Design: Enterprise issue_state sync (scheme C+A)

## Goal

- **Consumers** (e.g. slb-dpms) talk only to `issue-flow`.
- **issue-flow** keeps claim / lease / labels / workflow events on Gitee Open API (v5).
- **issue-flow** optionally syncs enterprise Kanban titles (修复中 / 已修复) via
  Enterprise HTTP (`api.gitee.com/enterprises`), the same API behind mcp-gitee-ent.

## Non-goals

- Spawning the MCP process from issue-flow (deferred).
- Replacing Open API for labels/comments/lease.
- Letting project agents call Open API or MCP directly.

## Config

```yaml
provider:
  type: gitee
  token_env: GITEE_TOKEN
  enterprise:
    enabled: true
    id: 12345                 # or resolve via path
    path: beijing-tongwei     # optional if id set
    token_env: GITEE_ENT_MCP_ACCESS_TOKEN
    api_base: https://api.gitee.com/enterprises
workflow:
  sync_provider_state: true
  provider_states: { review: progressing, done: closed, ... }
  enterprise_states:
    claimed: 修复中
    working: 修复中
    review: 已修复
```

## Flow

`UpdateIssue` → comment + labels (Open API) → `NativeStateSyncer.Sync`:

1. Open API PATCH `state` when `ProviderStateFor` is non-empty.
2. Enterprise PUT `issue_state_id` when `EnterpriseStateFor` is non-empty
   (resolve title → id via issue type states).
