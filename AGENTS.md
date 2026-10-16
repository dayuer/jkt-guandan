# Agent 须知

本仓库是多 agent 协作开发，协议见 [docs/agents/COORDINATION.md](docs/agents/COORDINATION.md)。

每轮开工前：

```bash
scripts/agents/sync.sh <coord|impl|auditor|design>
```

要点：
- issue / PR / 评论第一行写角色 `[impl]` 等，附 `<!-- agent: <角色>-<本机|远程> -->`。
- 只推自己角色前缀的分支；合并只由 coord 执行。
- 提交信息、PR 描述不得含 AI 助手署名（本地 CI 的 `protocol` 会拦）。
- 不用 GitHub Actions。开 PR 前跑 `scripts/local-ci.sh --no-post`，推送后跑 `scripts/local-ci.sh` 把结论发成 commit status；coord 合并前跑 `scripts/pre-merge-check.sh`。见 COORDINATION.md §4.2。
- 进展落到 GitHub，不留在本机会话里。
- 遇到阻塞不要傻等：本机会话当场 SendMessage 问 coord，远程会话在 issue / PR 评论 `BLOCKED: <原因>`；会话之间要对方做事一律经 coord 转（COORDINATION.md §6.1）。

项目背景：[docs/h5-pwa-plan.md](docs/h5-pwa-plan.md)（方案）、[docs/server-design.md](docs/server-design.md)（服务端：Go 单一二进制 + SQLite + Docker）、[design/guandan-h5/README.md](design/guandan-h5/README.md)（H5 原型与令牌：视觉看 A–H 设计稿，数值看 tokens.css，状态看 prototype.html）。
