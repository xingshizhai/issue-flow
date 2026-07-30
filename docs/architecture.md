# Issue Flow 技术方案

## 1. 架构原则

1. **Provider 中立**：工作流领域模型不暴露 Gitee 专用字段。
2. **确定性优先**：状态、租约、幂等和 API 操作由 CLI 实现，不交给模型自由发挥。
3. **安全默认值**：默认只读或本地修改，不默认推送、关闭或部署。
4. **跨工具复用**：AI 平台适配层保持轻薄。
5. **可测试**：Fake Provider、可注入时钟和 dry-run 是第一等能力。
6. **渐进式实现**：先完成 CLI 闭环，再实现 Web SDK 和调度服务。
7. **访问与认证解耦**：领域能力不依赖 REST、MCP、Token 或 OAuth 的具体实现。
8. **开发环境中立**：自动处理 Bug 的协议在不同代码开发 Agent 环境中一致。

## 2. 总体架构

```text
┌────────────────────────────────────────────────────────────┐
│                        使用入口                            │
│  Human CLI │ Codex │ Claude │ Cursor │ VS Code │ Web App │
└──────────────────────────────┬─────────────────────────────┘
                               │
┌──────────────────────────────▼─────────────────────────────┐
│                    issue-flow Application                  │
│ Commands │ Config │ Output │ Policy │ Redaction │ Dry-run │
└──────────────────────────────┬─────────────────────────────┘
                               │
┌──────────────────────────────▼─────────────────────────────┐
│                      Workflow Domain                       │
│ State Machine │ Lease │ Claim Token │ Validation │ Events │
└──────────────────────────────┬─────────────────────────────┘
                               │
┌──────────────────────────────▼─────────────────────────────┐
│                     Provider Interface                     │
│          Gitee │ Fake │ future GitHub/GitLab/Forgejo       │
└────────────────────────────────────────────────────────────┘
```

Web App 不应直接调用 Provider。第二阶段链路：

```text
Web App → Issue Gateway / Server SDK → Workflow → Provider
```

开发 Agent 链路：`Codex / Claude / Cursor / VS Code → platform adapter → issue-flow CLI (JSON) → workflow → provider`。CLI 负责确定性操作，宿主 Agent 负责分析和修改代码；平台适配不得复制状态机、租约或 Gitee 调用逻辑。

## 3. 推荐技术选型

MVP 推荐使用 Go，目标是生成一个无运行时依赖的 `issue-flow` 可执行文件。

选择 Go 的原因：

- 跨平台构建和分发简单。
- 标准库足以覆盖 HTTP、JSON、时间和命令执行基础能力。
- 易于实现强类型 Provider 接口及可测试状态机。
- 与 LiteERP 现有 Gitee 服务相近，迁移经验成本较低。

CLI 框架可以选 Cobra，但应控制依赖规模。配置可采用 YAML。HTTP 层优先使用标准库并显式设置超时。

## 4. 建议仓库结构

```text
issue-flow/
├── cmd/
│   └── issue-flow/
│       └── main.go
├── internal/
│   ├── app/                    # 命令用例编排
│   ├── config/                 # 配置加载、校验、版本
│   ├── domain/                 # Issue、状态、租约、事件
│   ├── workflow/               # 状态转换和权限校验
│   ├── provider/
│   │   ├── provider.go         # 中立接口
│   │   ├── gitee/
│   │   └── fake/
│   ├── output/                 # text/json 渲染
│   ├── redact/                 # 脱敏
│   └── clock/                  # 可注入时钟
├── skills/
│   └── issue-flow/
│       ├── SKILL.md
│       ├── agents/
│       │   └── openai.yaml
│       └── references/
│           ├── workflow.md
│           └── safety.md
├── adapters/
│   ├── claude/
│   ├── cursor/
│   ├── vscode/
│   └── generic/
├── examples/
│   └── issue-flow.example.yaml
├── docs/
│   ├── requirements.md
│   └── architecture.md
├── go.mod
└── Makefile
```

Skill 本身保持精简。详细状态机和安全约束放入 `references/`，仅在触发相关操作时读取。

## 5. 核心领域模型

### 5.1 Issue

```go
type Issue struct {
    ID          string
    Number      string
    Title       string
    Body        string
    State       IssueState
    Labels      []Label
    Assignees   []Actor
    Comments    []Comment
    URL         string
    Version     string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

`Version` 表示 Provider 可提供的 ETag、更新时间或其他并发校验值，不规定具体格式。

### 5.2 WorkflowState

```go
type WorkflowState string

const (
    StateReady   WorkflowState = "ready"
    StateClaimed WorkflowState = "claimed"
    StateWorking WorkflowState = "working"
    StateBlocked WorkflowState = "blocked"
    StateReview  WorkflowState = "review"
    StateDone    WorkflowState = "done"
)
```

状态机必须集中实现，不允许各命令自行拼装标签。

### 5.3 Lease

```go
type Lease struct {
    ID         string
    TokenHash  string
    AgentID    string
    ClaimedAt  time.Time
    ExpiresAt  time.Time
    HeartbeatAt time.Time
}
```

租约元数据的持久化方案应经过 Gitee API 能力验证后决定。候选方案：

1. 固定格式的 Issue 评论。
2. Issue 正文中的隐藏标记区块。
3. 标签加评论的组合。
4. 外部协调存储。

MVP 优先使用“机器可解析的固定评论 + 工作流标签”，但必须：

- 评论带版本、公开 lease ID 和 token 哈希，不得包含明文租约 token。
- 明文 token 只在 claim 成功响应中返回一次，通用 Issue 输出必须隐藏 token 哈希。
- 解析时忽略不合法或被篡改的记录。
- 追加事件而非频繁覆盖用户正文。
- 文档说明 Gitee 缺少强 CAS 时只能实现尽力原子。

示例事件：

```markdown
<!-- issue-flow:event
{"version":1,"operation":"claim","token":"...","agent":"...","expiresAt":"..."}
-->
Issue Flow: agent `codex-a` 已领取此任务，租约至 2026-07-30T12:00:00Z。
```

## 6. Provider 接口

建议接口按能力拆分，避免形成过大的万能接口：

```go
type IssueReader interface {
    ListIssues(ctx context.Context, query ListQuery) (IssuePage, error)
    GetIssue(ctx context.Context, number string) (Issue, error)
}

type IssueWriter interface {
    UpdateIssue(ctx context.Context, number string, change IssueChange, precondition Precondition) (Issue, error)
}

type Provider interface {
    IssueReader
    IssueWriter
    Capabilities(ctx context.Context) Capabilities
}
```

注意：

- `idempotencyKey` 即使 Provider 不原生支持，也应在本地事件内容中保留。
- `Precondition` 支持版本、更新时间或状态前置条件。
- Gitee Provider 负责把领域操作映射到标签和评论。
- Provider 的 HTTP DTO 不能泄漏到 `domain` 或 `workflow` 包。

### 6.1 Gitee 访问适配

Gitee Provider 对核心暴露统一接口，下层组合 REST transport（环境 Token 或 OAuth credential）或 MCP transport（已配置的 Gitee MCP Server）。transport 不得决定状态转换，所有方式共用 workflow、幂等、租约和审计。OAuth 刷新凭据不得进入项目配置或日志；MCP 返回同样按不可信输入校验和脱敏。缺少写入、幂等或版本前置条件时，通过 `Capabilities` 表达并返回 `UNSUPPORTED_CAPABILITY`。

当前 MVP 配置只启用 REST + Token。代码已抽取 `Transport`、`Credential` 和 `OAuthCredentialSource` 边界，OAuth access token 在每次请求时从外部源解析，以便刷新逻辑和 refresh token 留在项目配置之外；MCP 工厂在尚未实现时明确返回不支持错误，不得静默回退。`doctor` 的能力输出包含 transport、credential mode 和凭据是否可刷新。OAuth/MCP 配置接线与实际 MCP 适配器属于后续实现。

## 7. 命令设计

### 7.1 公共参数

```text
--config <path>      指定配置文件
--project <path>     指定目标项目目录
--format text|json   输出格式
--dry-run            不执行写操作
--verbose            详细诊断
--timeout <duration> 网络超时
```

建议同时接受 `--json` 作为 `--format json` 的快捷形式。

### 7.2 MVP 命令

```text
issue-flow init
issue-flow doctor
issue-flow list
issue-flow show <issue>
issue-flow context <issue>
issue-flow claim <issue>
issue-flow start <issue>
issue-flow heartbeat <issue>
issue-flow progress <issue>
issue-flow block <issue>
issue-flow release <issue>
issue-flow reclaim <issue>
issue-flow finish <issue>
```

`doctor` 用于只读检查配置、Token 是否存在、仓库访问、所需标签和 Provider 能力。默认不得自动创建标签；可另设显式 `doctor --fix`。

Gitee `doctor` 分页读取仓库标签并核对六个状态映射。任一标签缺失时返回
`CONFIG_ERROR` 和稳定排序的缺失标签名；检查过程只允许 GET，不得隐式创建标签。

### 7.3 JSON 响应信封

成功：

```json
{
  "ok": true,
  "operationId": "op_...",
  "data": {},
  "warnings": []
}
```

失败：

```json
{
  "ok": false,
  "operationId": "op_...",
  "error": {
    "code": "LEASE_CONFLICT",
    "message": "issue 123 is claimed by another agent",
    "retryable": false,
    "details": {}
  }
}
```

JSON 字段一旦发布应保持向后兼容。

`retryable` 只对 `RATE_LIMITED` 和 `PROVIDER_UNAVAILABLE` 为 `true`。认证、权限、
目标不存在、不支持的能力、状态、租约和输入错误必须为 `false`。工作流写操作的
Provider 错误沿用原 operation ID，不得在错误映射时生成新的 ID。

## 8. 退出码建议

| 退出码 | 类型 |
|---:|---|
| 0 | 成功 |
| 2 | 参数或配置错误 |
| 3 | 认证或权限错误 |
| 4 | 目标不存在 |
| 5 | 状态或租约冲突 |
| 6 | Provider 暂时不可用 |
| 7 | 验证失败 |
| 8 | 操作被安全策略拒绝 |
| 1 | 未分类内部错误 |

## 9. 配置加载

建议查找顺序：

1. `--config` 指定路径。
2. 从 `--project` 或当前目录向上查找 `.issue-flow.yaml`。
3. 用户级配置只保存非项目特定默认值。

配置优先级：

```text
命令行参数 > 环境变量 > 项目配置 > 用户默认配置 > 安全默认值
```

禁止在配置中直接保存 Token。`token_env` 指向环境变量名。输出配置时只能显示变量名和“已设置/未设置”，不能显示值。

## 10. 领取并发策略

Gitee 是否支持适合 Issue 更新的强条件写入，需要实现阶段基于官方 API 验证。在无强 CAS 的情况下采用以下流程：

1. 读取 Issue、标签、最新租约事件和版本标识。
2. 校验状态为 `ready` 且无有效租约。
3. 生成随机明文租约 token、token 哈希、公开 lease ID 和 `operationId`。
4. 写入 claim 事件。
5. 更新工作流标签为 `claimed`。
6. 再次读取 Issue。
7. 收集竞争窗口内的所有有效 claim。
8. 通过确定性规则选出胜者，例如最早 Provider 时间戳，再以公开 lease ID 排序。
9. 非胜者写入冲突事件并返回 `LEASE_CONFLICT`。

这仍不是分布式事务，但结果可收敛且不会让两个 Agent 都误以为长期持有租约。若未来需要强一致，可增加外部协调后端。

Workflow 在 claim 写入后的重读结果上再次验证 lease ID、agent ID 和明文 token。
只有三者都属于本次 claim 时才返回明文 token；竞争收敛后的失败者返回
`LEASE_CONFLICT`，结果中不得包含其临时 token。

## 11. 幂等和重试

- 每个写命令生成 operation ID。
- 评论事件包含 operation ID。
- 重试前先检查同 operation ID 是否已生效。
- 只对可安全重试的网络错误和 429/部分 5xx 进行指数退避。
- 不对认证失败、状态冲突和输入错误自动重试。
- HTTP 客户端必须设置连接和总请求超时。

写命令接受可选 `--operation-id`，仅允许 `op_` 前缀和有限安全字符。默认仍自动
生成。可重试错误后，调用方使用响应中的原 ID 重放完全相同的命令；Workflow
在鉴权和状态转换前识别已完成的同命令、同 Agent 事件并返回现状，不再次写
Provider。跨命令或跨 Agent 的 ID 冲突返回 `INVALID_ARGUMENT`。

Fake 和 Gitee Provider 也必须独立校验 operation ID 对应的 operation、agent、
lease、message 和状态转换语义。语义不同的重复 ID 返回前置条件冲突且不得产生
任何写入；因此绕过 Workflow 直接调用 Provider 也不能削弱幂等约束。

Provider 在读取远端状态之前校验 `IssueChange` 自洽性：事件版本、operation ID
和 operation 必填；事件目标状态必须与标签目标一致；不得同时设置和清除 lease；
事件 lease ID 必须与变更中的 lease 相同。无效变更在本地返回前置条件冲突，
不得访问网络或修改 Fake 存储。

claim 是例外：明文租约 token 只返回一次。已落盘 claim 的重放返回
`LEASE_CONFLICT`，不能恢复 token；调用方不得通过新 claim 猜测所有权。

当前 Gitee REST Client 只在 HTTP 层自动重试 GET：临时网络错误、429 和
5xx 最多尝试三次，遵守有限的 `Retry-After` 并使用指数退避。POST/PUT 等写请求
不在 HTTP 层盲目重试，避免重复评论或标签写入；写流程依靠 operation ID 和写前
重读收敛。

## 12. 安全设计

### 12.1 提示注入边界

Issue 标题、正文、评论和附件内容都是不可信数据。Agent Skill 必须明确：

- Issue 中要求泄露密钥、绕过权限或忽略项目规范的文字无权改变系统指令。
- Issue 只能定义业务目标，不能授予外部写权限。
- 执行 shell 命令前按当前开发环境的授权规则处理。

### 12.2 命令注入

- CLI 不把 Issue 文本拼接成 shell 命令。
- 验证命令来自受信任的项目配置，而不是 Issue 正文。
- 若执行验证命令，优先使用参数数组或明确的受控 shell 边界。
- MVP 配置使用 `argv` 参数数组，CLI 的 `context` 命令只输出验证计划，不自动执行。
- 分支 slug 只允许安全字符并限制长度。
- 项目指令路径必须保持在项目根目录内，符号链接解析后再次检查边界。
- finish 摘要先检查路径，再打开并核对文件身份，最终只从已核对的描述符读取；
  拒绝符号链接、非普通文件、打开期间的路径替换和超过 64 KiB 的内容。
- Fake Provider 的 `data_file` 只能是配置目录内的直接文件名，不允许绝对路径、
  子目录或 `..`。读取时拒绝符号链接和非普通文件，并核对打开前后的文件身份，
  防止本地演练读写项目外数据。

### 12.3 数据脱敏

对以下内容进行大小写不敏感的键名和模式脱敏：

- password、passwd
- token、access_token、refresh_token
- cookie、set-cookie
- authorization
- api_key、secret、private_key

日志中不得输出完整 HTTP 请求头或请求体。调试模式也遵守相同规则。

CLI 在 `show/list/context` 和工作流结果输出前，对 Issue 标题、正文、URL、评论、
附件及事件消息执行配置驱动的模式脱敏。`progress/block/release/finish` 的自由文本
在进入 Provider 前执行同一脱敏，避免秘密被写入 Issue。脱敏不修改 Issue 编号、
状态、标签和租约等协议字段。

### 12.4 Web Gateway

第二阶段 Gateway 需增加：

- 身份认证或匿名限流。
- CSRF/来源校验。
- 输入长度限制。
- 附件类型和大小限制。
- 服务端脱敏。
- 审计记录。
- 防止用户控制目标 owner/repo。

## 13. Skill 与平台适配

### 13.1 Codex Skill

Skill 名称建议为 `address-issues` 或 `issue-flow`。其 `SKILL.md` 只包含：

- 触发场景。
- 开始前的 `doctor/show/context/claim` 顺序。
- 必须确认领取成功。
- 项目指令优先级。
- 开发、验证、回写流程。
- 安全和停止条件。

详细协议放入：

```text
references/workflow.md
references/safety.md
```

确定性操作全部调用 CLI，不在 Skill 中复制 HTTP 示例。

### 13.2 其他平台

Cursor、Claude 和 VS Code 的适配内容来自同一份规范模板。生成时允许改变 frontmatter 或目录布局，但核心规则必须一致。

建议维护一个中立源文件：

```text
adapters/generic/agent-workflow.md
```

由脚本生成或人工同步各平台适配，测试中检查关键规则没有丢失。

根目录维护英文 `README.md` 和中文 `README.zh-CN.md`，面向新开发 Agent 说明安装、doctor、JSON、领取确认、心跳、开发验证、安全边界与 Fake Provider 流程，并通过文档测试保持命令与 CLI 一致。

## 14. 测试策略

### 14.1 单元测试

- 状态机合法和非法转换。
- 租约有效、过期、续租和释放。
- 标签规范化。
- 配置优先级和校验。
- 脱敏。
- JSON 输出和退出码。
- 分支名称安全化。

### 14.2 Provider Contract Tests

同一组契约测试运行在 Fake 和 Gitee Provider 上：

- 查询和读取 Issue。
- 修改标签。
- 添加评论。
- 处理分页。
- 映射认证、限流、404 和 5xx。
- 保留幂等 operation ID。

Gitee 契约测试默认跳过，仅在提供显式环境变量时执行。

### 14.3 并发测试

- 多 goroutine 同时 claim。
- claim 与 release 竞争。
- heartbeat 与 reclaim 竞争。
- 网络超时后重试不重复写入。

### 14.4 CLI 集成测试

在临时目录中使用 Fake Provider：

```text
init → doctor → list → claim → start → progress → finish
```

验证文件、stdout、stderr、JSON schema 和退出码。

### 14.5 Skill 前向测试

完成 Skill 后，用隔离的 Fake 项目向新的 Agent 会话提出真实任务，例如：

```text
使用 issue-flow 处理当前项目中下一个可领取的 Bug。
```

验证 Agent 是否：

- 先读取项目规范。
- 先领取再修改。
- 不执行越权操作。
- 运行配置的验证。
- 正确回写。

## 15. 实施阶段

### Phase 0：仓库基线

- 建立 Go module、格式化、lint 和测试入口。
- 放入需求和架构文档。
- 确定提交规范与 CI。

### Phase 1：领域与 Fake Provider

- 配置模型。
- 状态机与租约。
- Provider 接口。
- Fake Provider。
- JSON/text 输出。
- `init/doctor/list/show`。

### Phase 2：MVP 写流程

- `claim/start/heartbeat/progress/block/release/reclaim/finish`。
- Dry-run、幂等和冲突处理。
- 完整 CLI 集成测试。

### Phase 3：Gitee Provider

- 基于官方 API 完成认证、查询、标签和评论。
- 抽象 Gitee access transport 与 credential，首期实现 REST + Token，并为 REST + OAuth 和 MCP 保留可验证适配边界。
- Provider contract tests。
- 使用专用测试仓库进行显式端到端验证。

### Phase 4：Agent 适配

- 初始化 Codex Skill。
- 编写通用工作流和安全引用。
- 增加 Claude、Cursor、VS Code 适配。
- 维护面向开发 Agent 的中英文安装与使用 README。
- 进行隔离前向测试。

### Phase 5：LiteERP 接入

- 将 LiteERP 现有 Gitee 创建逻辑迁移到 Gateway/SDK。
- 保持现有 Web API 向后兼容。
- 增加 Web 输入校验、脱敏和结构化模板。

### Phase 6：扩展

- GitHub/GitLab Provider。
- 通用 Web 组件。
- 可选调度服务、worktree 隔离和 PR 自动化。

## 16. MVP 开发决策记录

以下决策可直接采用，除非实现时发现 Provider 限制：

| 议题 | 决策 |
|---|---|
| 核心形态 | CLI + library，不做模型绑定 Agent |
| 首个 Provider | Gitee |
| 推荐语言 | Go |
| 默认交付状态 | `agent:review` |
| 自动关闭 | 默认关闭 |
| 自动推送 | 默认关闭 |
| 锁机制 | 标签 + 版本化租约事件 + 冲突收敛 |
| Token | 环境变量引用 |
| 测试 | Fake 为默认，真实 Gitee 显式启用 |
| Skill | 薄 Skill，复杂细节放 references |

## 17. 开发完成定义

一个阶段只有在以下条件同时满足时才算完成：

- 实现满足对应需求。
- 单元测试和集成测试通过。
- 不需要真实 Token 的测试默认可运行。
- 文档和示例配置与实现一致。
- 没有密钥或敏感信息进入仓库和日志。
- 新 Agent 能仅根据仓库文档继续后续开发。
