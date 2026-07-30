# Issue Flow 需求规格

## 1. 文档目的

本文定义 Issue Flow 的产品范围、角色、业务流程、功能需求和验收标准。实现细节见《技术方案》。

## 2. 产品定位

Issue Flow 是一套面向 AI 辅助开发的、Provider 可扩展的 Issue 工作流基础设施。它连接业务系统中的问题反馈入口、代码托管平台的 Issue，以及本地开发 Agent。

产品核心价值：

- 让业务用户提交的问题成为结构化、可执行的开发任务。
- 让多个 Agent 安全地逐个领取任务，避免重复处理。
- 让开发 Agent 自动完成“领取 Bug、分析代码、开发修复、运行验证、回写结果”的闭环。
- 让开发过程、验证证据和交付物回写到 Issue。
- 让同一工作流可以跨项目、跨编辑器、跨 AI 产品复用。

Issue Flow 的主要使用者是能够开发代码的 Agent。它必须可被 VS Code、Codex、Cursor、Claude Code 等环境调用，而不依赖某一种模型、编辑器或自主 Agent 运行时。CLI 和适配层提供协议，代码阅读、修改与测试由宿主 Agent 执行。

## 3. 角色

| 角色 | 职责 |
|---|---|
| 反馈用户 | 从 Web 或命令行提交 Bug、需求及附件 |
| 项目维护者 | 配置仓库、标签、权限、验证命令和自动化等级 |
| 开发 Agent | 领取 Issue，分析、开发、验证并回写结果 |
| 审核者 | 审核变更、合并代码、决定是否关闭 Issue |
| Issue Provider | 保存 Issue、评论、标签和状态，第一期为 Gitee |

## 4. 核心用例

### UC-01 提交 Bug

用户填写问题描述、复现步骤、预期结果和实际结果。系统附加经过脱敏的环境上下文，在 Gitee 创建带 `type:bug` 和 `agent:ready` 标签的 Issue。

### UC-02 提交需求

用户填写背景、用户目标、功能范围和验收条件。系统创建带 `type:feature` 和适当优先级标签的 Issue。

### UC-03 Agent 获取任务

Agent 查询可处理 Issue。工具按优先级、依赖和创建时间返回任务，并输出机器可读 JSON。

### UC-04 Agent 领取任务

Agent 原子领取某个 Issue。成功后写入领取者、租约、分支建议和时间；失败时明确说明已被谁领取或为何不可领取。

### UC-05 Agent 执行任务

Agent 获取规范化上下文，读取目标项目的开发说明，修改代码并执行配置的验证命令。Issue Flow 不代替开发 Agent 修改代码。

### UC-06 Agent 汇报进度

Agent 定期续租或更新进度。长任务中可记录当前阶段、已完成项和阻塞原因。

### UC-07 Agent 交付

Agent 回写变更摘要、分支、提交、PR、测试结果和遗留风险。默认将 Issue 转为等待人工审核，不默认关闭。

### UC-08 失败和恢复

Agent 可以将任务标记为阻塞或释放租约。Agent 异常退出后，过期租约可以被回收。

## 5. 工作流状态

### 5.1 状态集合

| 规范状态 | 默认标签 | 含义 |
|---|---|---|
| `ready` | `agent:ready` | 信息满足最低要求，可以领取 |
| `claimed` | `agent:claimed` | 已获得有效租约，尚未开始或刚开始 |
| `working` | `agent:working` | Agent 正在开发 |
| `blocked` | `agent:blocked` | 缺少信息、权限或外部条件 |
| `review` | `agent:review` | 开发和验证完成，等待审核 |
| `done` | `agent:done` | 已被确认完成 |

### 5.2 合法转换

```text
ready → claimed → working → review → done
                    │          │
                    └→ blocked ┘

claimed/working/blocked → ready    释放或租约回收
review → working                  审核退回
```

非法状态转换必须失败，不得静默修改。

### 5.3 标签分类

类型：

- `type:bug`
- `type:feature`
- `type:improvement`

优先级：

- `priority:p0`
- `priority:p1`
- `priority:p2`
- `priority:p3`

工作流标签在同一时刻只能存在一个。类型和优先级原则上也各自唯一。

## 6. 功能需求

### FR-01 项目初始化

提供初始化命令，创建不含密钥的配置模板：

```bash
issue-flow init
```

初始化过程不得覆盖已有配置，除非用户显式确认。

### FR-02 Provider 配置

支持通过配置文件指定 Provider、仓库、标签映射、租约时间和项目规则。Token 只能引用环境变量或外部密钥源。

Gitee Provider 必须支持 REST API 和 Gitee MCP Server 两种访问通道；REST API 支持环境变量 Token 与 OAuth。访问通道和认证方式必须解耦，通过 capability 表达能力；不支持的操作必须明确失败，不能静默改变工作流语义。

### FR-03 查询 Issue

```bash
issue-flow list --ready
issue-flow list --state blocked
issue-flow show 123
```

查询应支持分页、数量限制和 JSON 输出。`show` 应返回正文、评论、标签、负责人及工作流元数据。

### FR-04 领取与并发保护

```bash
issue-flow claim 123 --agent <agent-id>
```

领取必须：

1. 检查 Issue 当前可领取。
2. 检查是否有未过期租约。
3. 写入领取元数据和状态。
4. 重新读取并确认调用方持有租约。
5. 遇到并发冲突时失败，不得覆盖其他 Agent。

成功领取只返回一次不可预测的明文租约 token。后续持有者操作必须同时提供
agent ID 和租约 token；Provider 只能持久化 token 哈希和公开 lease ID，
通用 Issue 输出不得暴露 token 或 token 哈希。

Gitee API 若无法提供真正的条件更新，MVP 必须明确其“尽力原子”限制，并通过变更前后校验、唯一 claim token 和冲突检测降低风险。

### FR-05 租约与心跳

```bash
issue-flow heartbeat 123 --agent <agent-id> --lease-token <token>
issue-flow release 123 --agent <agent-id> --lease-token <token>
```

- 默认租约建议为 120 分钟，可配置。
- 只有当前租约持有者可以续租或主动释放。
- 释放时记录原因。
- 回收过期租约应是显式命令，MVP 不要求后台定时任务。

### FR-06 上下文生成

```bash
issue-flow context 123
```

输出至少包含：

- Issue 基本信息和验收标准。
- 有效评论与附件链接。
- 当前工作流状态和租约信息。
- 项目指令文件列表。
- 建议分支名。
- 允许的自动化权限。
- 必须运行的验证命令。

支持 Markdown 和 JSON 输出。

### FR-07 进度、阻塞和交付

```bash
issue-flow progress 123 --message "..."
issue-flow block 123 --reason "..."
issue-flow finish 123 --summary-file result.md
issue-flow complete 123 --reviewer reviewer-id --conclusion-file review.md
```

`finish` 支持记录：

- 修改摘要。
- 分支名称。
- 提交哈希。
- PR URL。
- 已执行测试及结果。
- 未验证项。
- 风险和后续工作。

默认目标状态为 `review`。只有配置明确允许且调用方显式传参时，才能关闭 Issue。
人工审核通过后，`complete` 记录审核人和结论并将状态推进到 `done`；
该状态转换本身不关闭 Provider Issue。

### FR-08 Dry-run

所有写操作必须支持：

```bash
issue-flow claim 123 --dry-run
```

Dry-run 输出将要执行的变化，不发送写请求。

### FR-09 结构化错误

错误至少区分：

- 配置错误。
- 认证错误。
- 权限不足。
- Issue 不存在。
- 状态冲突。
- 租约冲突。
- Provider 限流或不可用。
- 网络错误。
- 输入校验失败。

JSON 模式下必须返回稳定的错误码和非零进程退出码。

### FR-10 Web 提交接口

第二阶段提供服务端 SDK 或 Gateway，用于业务项目安全创建 Issue。浏览器不能持有 Provider Token。

输入字段至少包括：

- 类型、标题、描述、优先级。
- Bug 复现步骤或需求验收条件。
- 当前页面和应用版本。
- 可选截图、日志摘要和请求 ID。

服务端必须完成校验、限流、脱敏和审计。

### FR-11 跨 Agent 适配

至少提供：

- Codex `SKILL.md`。
- Claude Code 适配说明。
- Cursor Rule。
- VS Code Agent Instructions。
- 可由 `AGENTS.md` 引用的通用工作流片段。
- 面向开发 Agent 的中英文 README，包含安装、配置、安全检查和完整使用流程。

适配层只描述触发条件和 CLI 调用流程，不复制 Provider 实现。

各平台适配必须支持同一自动处理闭环：发现、领取、上下文、开发、验证、续租或汇报、阻塞/释放或交付。更换开发环境不应改变状态、租约和 JSON 协议。

### FR-12 Provider 扩展

核心代码不得直接依赖 Gitee 的字段作为领域模型。新增 Provider 时不应修改工作流核心逻辑。

## 7. 项目配置需求

建议配置：

```yaml
version: 1

provider:
  type: gitee
  owner: example
  repo: example-project
  token_env: GITEE_TOKEN

workflow:
  ready_label: agent:ready
  review_label: agent:review
  lease_minutes: 120
  auto_close: false

project:
  instruction_files:
    - AGENTS.md
    - CLAUDE.md

validation:
  commands:
    - argv: ["go", "test", "./..."]
      timeout: 10m

git:
  branch_pattern: "{type}/issue-{number}-{slug}"
  allow_commit: false
  allow_push: false
  allow_pull_request: false

automation:
  level: patch

security:
  redact_keys:
    - password
    - token
    - cookie
    - authorization
```

配置规范应支持未来版本迁移。未知字段可提示警告，但不能造成危险的默认行为。

验证命令必须使用参数数组；默认不经 shell 解释。项目指令文件必须是项目根目录内的
相对路径，解析后的符号链接也不得越过项目根目录。

## 8. 权限等级

| 等级 | 能力 |
|---|---|
| `inspect` | 读取和分析，不修改项目 |
| `patch` | 修改和测试，不提交 |
| `commit` | 允许创建分支和本地提交 |
| `delivery` | 允许推送、创建 PR，并按策略更新 Issue |

默认不得高于 `patch`。Issue Flow 配置只能限制权限，不能绕过当前 Agent 环境自身的授权机制。

以下情况默认要求人工确认：

- 数据库破坏性迁移。
- 权限、认证和密钥管理变更。
- 财务金额或结算逻辑变更。
- 删除数据或不可逆操作。
- 推送、合并、发布和生产部署。
- 自动关闭需求类 Issue。

## 9. 非功能需求

### NFR-01 安全

- Token 不进入前端、日志、Issue 正文和错误堆栈。
- 默认对密码、Token、Cookie、Authorization 和常见密钥字段脱敏。
- 写操作记录审计信息，但审计信息不得包含密钥。
- 外部文本均视为不可信输入，防范提示注入和命令注入。
- Issue 内容不能自动扩大 Agent 的系统权限。

### NFR-02 可移植性

- CLI 应支持 Linux、macOS，建议支持 Windows。
- 不要求业务项目使用特定语言或框架。
- JSON 输出保持稳定，便于 IDE 和脚本集成。

### NFR-03 可测试性

- Provider 必须可替换为 Fake。
- 单元测试不得依赖真实 Gitee。
- 真实 API 测试必须显式启用并使用专用测试仓库。
- 时间和租约逻辑应支持注入时钟。

### NFR-04 可观察性

- `--verbose` 输出调试信息。
- `--json` 输出机器可读结果。
- 每个写操作生成 operation ID。
- 网络重试不得造成重复评论或重复状态变更。

### NFR-05 兼容性

- 配置文件包含显式版本。
- Provider 能力差异通过 capability 表达。
- 不支持的能力必须明确报错或采用有文档的降级方案。

## 10. MVP 验收标准

MVP 完成必须满足：

1. 在空目录执行 `issue-flow init` 能生成有效配置。
2. Fake Provider 下可以完成 `ready → claimed → working → review`。
3. 两个模拟 Agent 同时领取同一 Issue 时，最终最多一个成功。
4. 非租约持有者不能 heartbeat、release、block 或 finish。
5. 过期租约可以通过显式命令回收。
6. 所有写命令支持 dry-run。
7. 所有主要命令支持 JSON 输出和稳定退出码。
8. Token 不出现在测试日志或命令输出中。
9. Gitee Provider 能在授权的测试仓库完成一次端到端流程。
10. Codex 能按 Skill 指令调用 CLI 完成一次 Fake Provider 工作流。
11. 中英文 README 足以指导新开发 Agent 完成安装、配置检查和 Fake Provider 工作流。
12. Gitee REST Token、REST OAuth 和 MCP 接入具有统一接口、能力模型和可测试边界；未实现的方式必须明确标注。

## 11. 暂不纳入 MVP

- 自动扫描并自主选择 Issue 的常驻服务。
- 自动拉起任意厂商的模型或 IDE。
- 多租户 SaaS 控制台。
- 自动代码评审和自动合并。
- 自动部署生产环境。
- 全量附件存储服务。
- GitHub、GitLab 和 Forgejo 的正式实现。
- 通用 Web UI 组件。
