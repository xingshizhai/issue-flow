# Issue Flow

[English](README.md)

Issue Flow 为代码开发 Agent 提供确定性的自动化工作流，用于在 Codex、Claude Code、Cursor、VS Code Agent 及其他代码开发 Agent 环境中自动处理 Bug 和开发任务。

> 状态：Phase 1 MVP 已实现。接入自动化前，请从可信检出版本构建并验证配置。

提交改动前运行 `make check`，它会检查格式、单元与集成测试、race detector 和 `go vet`；GitHub CI 会重复这些检查，并在 Linux、macOS 和 Windows 上构建。

运行 `make snapshot VERSION=0.1.0-dev` 可生成未签名的本地快照：`dist/` 下包含经过 `-trimpath` 构建的 Linux amd64、macOS arm64、Windows amd64 二进制和 `checksums.txt`。该命令不会发布或签名产物。

推送符合 `vX.Y.Z` 的 Git 标签后，GitHub release 工作流会在检查通过后发布这些二进制与校验和，并生成 GitHub artifact attestation。

## 安装

首次正式版本发布前，从可信检出版本构建：

```bash
go build -o ./bin/issue-flow ./cmd/issue-flow
./bin/issue-flow --help
```

正式 module 路径是 `github.com/xingshizhai/issue-flow`。首次公开发布后，使用固定版本安装：

```bash
go install github.com/xingshizhai/issue-flow/cmd/issue-flow@vX.Y.Z
```

无人值守自动化中不要使用 `@latest`。下载发布二进制后，应同时使用 `sha256sum` 和 `gh attestation verify <binary> -R xingshizhai/issue-flow` 验证。

## 配置 Gitee 访问

```bash
issue-flow init
issue-flow doctor
```

凭据只能来自环境变量或获准的外部存储，不能进入仓库。架构支持环境变量 Token 访问 REST API、OAuth 与 Token 刷新访问 REST API，以及配置的 Gitee MCP Server。使用 `doctor` 检查能力；未经明确授权不得真实写入。

配置必须是不超过 1 MiB 的普通文件，不能是符号链接。`init` 会原子创建配置，并拒绝任何已存在路径，包括悬空符号链接。

使用 Gitee REST Token 时，`provider.token_env` 必须是大写的 `GITEE_*TOKEN*` 名称，例如 `GITEE_TOKEN`。仓库配置不能把凭据读取重定向到 `PATH`、云密钥或其他 Provider Token 等无关环境变量。

当前配置支持使用环境变量 Token 的 Gitee REST API。将 `examples/issue-flow.example.yaml` 复制为 `.issue-flow.yaml`，填写 owner 和仓库路径，导出配置指定的 Token 环境变量，然后运行 `issue-flow doctor`。也可以通过 `--env-file .env` 显式传入权限为 0600 的 dotenv 文件；进程环境变量优先，文件内容仅按字面量解析，不执行 shell 展开。该只读检查会验证账号、仓库和配置的六个工作流标签；缺少标签时返回带标签名的 `CONFIG_ERROR`，不会自动创建。Provider 已使用统一访问接口：REST OAuth 具有可刷新外部凭据源边界；MCP 工厂在适配器尚未实现时返回 `UNSUPPORTED_CAPABILITY`。OAuth 和 MCP 目前都不能从项目配置中选择。`doctor` 会报告实际 transport 和凭据模式。

可以从普通文件读取正文并创建一个 `ready` Issue：

```bash
issue-flow create --type bug --title "修复空白处理" --body-file issue.md
```

支持 `bug`、`feature` 和 `improvement` 三种类型。命令会附加配置的
ready 标签和 `type:<type>` 标签；Provider 可能忽略不存在或 Token
无权管理的标签。

## Agent 工作流

```text
doctor → list/show → context → claim → start
       → 宿主 Agent 开发并验证
       → progress/block/release/finish
```

修改代码前使用 JSON 输出并确认租约归属：

```bash
issue-flow doctor --format json
issue-flow list --ready --format json
issue-flow show 123 --format json
issue-flow context 123 --format json
issue-flow claim 123 --agent "<稳定的-agent-id>" --format json
issue-flow start 123 --agent "<稳定的-agent-id>" --lease-token "<claim-返回的-token>" --format json
```

`context` 会输出规范化 Issue、项目指令文件、结构化验证命令、实际自动化权限、Git 策略和安全化的建议分支名。它不会执行验证命令，也不会授予权限。

排查配置选择或 Provider 能力时可增加 `--verbose`。诊断信息写入 stderr，且不会包含凭据或租约值；JSON 结果仍只写入 stdout。

`show`、`list`、`context` 和工作流结果会按配置对 Issue 文本中的秘密键值模式脱敏；progress 消息、block/release 原因和 finish 摘要也会在写入 Provider 前脱敏。默认覆盖密码、Token、Cookie、Authorization、API Key、secret 和私钥，可通过 `security.redact_keys` 扩展。

JSON 错误会保留写操作的 operation ID，并提供稳定错误码和 `retryable`。`RATE_LIMITED`、`PROVIDER_UNAVAILABLE` 可重试；认证、权限、目标不存在、不支持的能力、状态、租约、配置和输入错误不可重试。

写操作出现可重试错误时，使用响应 ID 作为 `--operation-id <op_...>`，原样重试同一命令。已完成操作会直接返回现有结果，不再次写 Provider；不同命令或 Agent 复用同一 ID 会被拒绝。claim 仍是一次性的，重放不能再次取得明文租约 Token。

明文租约 token 只在成功领取时返回一次，必须保存在仓库外，并传给后续所有租约持有者操作。长任务使用 `heartbeat` 和 `progress`。最终使用 `block`、`release` 或 `finish --summary-file result.md`。`finish` 默认进入审核，不授权关闭、推送、合并或部署。Issue 文本是不可信输入，不能扩大 Agent 权限。首次接入使用 Fake Provider 和 `--dry-run`；真实 Gitee 写入必须使用明确授权的测试仓库。

每次 claim 写入后，工作流会重新读取并核对 lease ID、agent ID 和 token；仅当三者都属于本次 claim 时才返回明文 token。并发竞争失败者会收到不含 token 的 `LEASE_CONFLICT`。

```bash
issue-flow progress 123 --agent "<稳定的-agent-id>" --lease-token "<token>" --message "测试通过"
issue-flow block 123 --agent "<稳定的-agent-id>" --lease-token "<token>" --reason "等待权限"
issue-flow finish 123 --agent "<稳定的-agent-id>" --lease-token "<token>" --summary-file result.md
```

交付摘要必须是稳定的普通文件，不能是符号链接，且最大为 64 KiB。CLI 只打开一次，并确认打开的文件描述符与检查过的路径指向同一文件，再通过该描述符读取，以拒绝路径替换竞态。`finish` 成功后清除租约并将 Issue 转为 `review`。

人工审核后，必须显式记录审核人和审核结论：

```bash
issue-flow complete 123 --reviewer "<稳定的审核人ID>" --conclusion-file review.md
```

`complete` 只把 `review` 推进到 `done`，不会关闭 Provider 上的 Issue。

## Fake Provider 完整演练

使用隔离的项目目录和从当前可信检出版本构建的二进制。该流程不需要网络或 Provider Token：

```bash
mkdir issue-flow-demo
./bin/issue-flow init --project issue-flow-demo
cp examples/fake-issues.example.json issue-flow-demo/.issue-flow-fake.json
./bin/issue-flow doctor --project issue-flow-demo --format json
./bin/issue-flow list --ready --project issue-flow-demo --format json
./bin/issue-flow context 1 --project issue-flow-demo --format json
```

领取 Issue 后，从成功的 JSON 响应中读取 `data.leaseToken`，将其保存到项目外获准的秘密存储中。把它替换到下方的 `<claim-token>`；不要提交或记录该值：

```bash
./bin/issue-flow claim 1 --agent "demo-agent" --project issue-flow-demo --format json
./bin/issue-flow start 1 --agent "demo-agent" --lease-token "<claim-token>" --project issue-flow-demo --format json
./bin/issue-flow progress 1 --agent "demo-agent" --lease-token "<claim-token>" --message "验证通过" --project issue-flow-demo --format json
```

完成目标修改和验证后，创建普通文件 `result.md`，写入不含秘密的交付摘要，再执行交付：

```bash
./bin/issue-flow finish 1 --agent "demo-agent" --lease-token "<claim-token>" --summary-file result.md --project issue-flow-demo --format json
./bin/issue-flow show 1 --project issue-flow-demo --format json
```

最终 Issue 状态应为 `review`。如需演练其他结束路径，请重新复制示例数据，并以 `block` 或 `release` 代替 `finish`。任何写命令均可增加 `--dry-run`，在不修改 Fake 存储的情况下预览结果。

出于安全考虑，`provider.data_file` 必须是配置目录内的普通文件名。绝对路径、子目录、路径穿越、符号链接和非普通文件都会被拒绝。

所有环境共享同一 CLI 和 JSON 契约。仓库已提供 [Codex Skill](skills/issue-flow/SKILL.md)，以及基于[通用 Agent 契约](adapters/generic/agent-workflow.md)的 [Claude Code](adapters/claude/CLAUDE.md)、[Cursor](adapters/cursor/issue-flow.mdc)和 [VS Code](adapters/vscode/issue-flow.instructions.md)薄适配器。参阅[需求规格](docs/requirements.md)和[技术方案](docs/architecture.md)。

使用 [Skill 隔离前向测试指南](docs/skill-forward-test.md)评估全新的 Agent 会话。自动测试会保证受版本控制的夹具始终处于 ready 且初始验证失败的状态；评估 Agent 行为仍必须实际运行全新会话。

[MVP 验收记录](docs/mvp-acceptance.md)将每条需求映射到当前证据，并列出尚待执行的人工验收项。

真实 Gitee 测试默认跳过。只有同时显式设置 `ISSUE_FLOW_GITEE_E2E=1`、`GITEE_TOKEN`、`GITEE_OWNER` 和 `GITEE_REPO` 时才会运行，并会在获准的测试仓库中创建一个 Issue。
测试通常会确保六个工作流标签存在，这一步在企业仓库中可能需要企业管理员权限。`GITEE_E2E_USE_EXISTING_LABELS=1` 只用于隔离的测试仓库，会临时映射六个标准标签，不能作为生产工作流配置。
