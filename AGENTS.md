# Agent 须知

本仓库是多 agent 协作开发，协议见 [docs/agents/COORDINATION.md](docs/agents/COORDINATION.md)。

每轮开工前：

```bash
scripts/agents/sync.sh <coord|impl|auditor|design>
```

要点：
- issue / PR / 评论第一行写角色 `[impl]` 等，附 `<!-- agent: <角色>-<本机|远程> -->`。
- 只推自己角色前缀的分支；合并只由 coord 执行。
- 提交信息、PR 描述不得含 AI 助手署名（CI 会拦）。
- 进展落到 GitHub，不留在本机会话里。

项目背景：[docs/h5-pwa-plan.md](docs/h5-pwa-plan.md)（方案）、[docs/server-design.md](docs/server-design.md)（服务端：Go 单一二进制 + SQLite + Docker）、[design/guandan-offline/redesign-prompts.md](design/guandan-offline/redesign-prompts.md)（设计改版）。
