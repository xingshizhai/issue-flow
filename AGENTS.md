# Issue Flow 项目交接说明

## 1. 项目目标

建设一个独立、可复用的 Issue 驱动开发工具，使不同项目都可以形成以下闭环：

1. 用户从项目 Web 页面提交 Bug 或需求。
2. 系统安全地创建 Gitee Issue。
3. 开发 Agent 查询并领取待处理 Issue。
4. Agent 在本地项目中分析、开发和验证。
5. 工具将分支、提交、测试结果和处理结论回写 Issue。
6. Issue 进入人工审核或完成状态。

该能力不能绑定 LiteERP，也不能绑定单一 AI 产品。它应能被 Codex、Claude Code、Cursor、VS Code Agent 等开发环境调用。

## 2. 核心设计结论

不要把核心能力只实现成某个平台的 Skill 或自主 Agent。采用以下三层结构：

```text
确定性核心：issue-flow CLI / library
工作流协议：状态、标签、租约、配置和安全规则
平台适配层：Codex Skill、Claude Skill、Cursor Rule、VS Code Instructions
```

其中：

- CLI 负责 API 调用、状态转换、并发领取、结构化输出和配置解析。
- Skill/Rule 负责指导 Agent 何时以及如何调用 CLI。
- AI 开发环境负责阅读项目、修改代码和运行测试。
- Gitee 是第一期 Provider，但核心接口必须允许后续扩展 GitHub、GitLab 和 Forgejo。

## 3. 新仓库建议位置

```text
/data/Work/issue-flow
```

本目录中的三个 Markdown 文件复制到新仓库后，建议放置为：

```text
issue-flow/
├── docs/
│   ├── requirements.md
│   └── architecture.md
└── AGENTS.md
```

对应关系：

| 当前文件 | 新仓库文件 |
|---|---|
| `00-项目交接说明.md` | 可作为初始 `AGENTS.md` 的背景输入 |
| `01-需求规格.md` | `docs/requirements.md` |
| `02-技术方案.md` | `docs/architecture.md` |

## 4. 新 Codex 会话的建议首条指令

```text
请先完整阅读 AGENTS.md、docs/requirements.md 和 docs/architecture.md，
检查仓库当前状态，然后按文档中的 Phase 1 实施 issue-flow MVP。
先给出任务拆分和准备修改的文件，随后直接开发并验证。
未经我明确授权，不要推送远程仓库，不要调用真实 Gitee 写接口。
```

## 5. 第一阶段交付目标

第一阶段只完成可测试的最小闭环：

- 初始化项目配置。
- Gitee Provider 抽象及实现。
- 查询、查看、领取、续租、释放、阻塞、完成 Issue。
- 文本和 JSON 两种输出。
- Dry-run 和本地 Fake Provider。
- Codex Skill 示例。
- 单元测试与端到端模拟测试。

第一阶段暂不实现：

- 常驻自主 Agent。
- 自动修改业务项目代码。
- Web 通用组件。
- GitHub/GitLab Provider。
- 自动推送、合并或部署。
- 无人值守地关闭 Issue。

## 6. 开发时必须优先确认的事项

开始编码前需要从仓库和用户处确认：

1. 新仓库计划使用的实现语言。
2. Gitee 仓库地址及测试仓库。
3. Gitee Token 的注入方式。
4. 是否允许使用测试仓库执行真实写入测试。
5. CLI 包名和发布方式。

若无额外决定，建议 MVP 使用 Go：

- 易于生成单文件可执行程序。
- 便于跨项目、跨编辑器安装。
- 与 LiteERP 当前技术栈一致，可复用已有 Gitee 调用经验。

不要把 Gitee Token、测试账号或仓库密钥写入代码、配置样例、测试快照和日志。

