# 多 agent 协作协议（GitHub 中转）

> 来源：[dayuer/nibwire 的 COORDINATION.md](https://github.com/dayuer/nibwire/blob/0c72b4d7550cf0aba5228924e69ae553304ac220/docs/agents/COORDINATION.md)。本仓库在原协议基础上做了三处调整：
> 1. 角色 `biz` 换成 `design`，负责设计稿和文案；
> 2. 增加阶段标签 `phase:*`；
> 3. 增加第 8 节「同步体系」，由脚本和 Action 维护看板。

本仓库由多个 agent 协作开发，有的在本机，有的在远程。远程 agent 收不到本机会话之间的消息，所以 **GitHub 是所有角色共享的唯一事实来源**。每个 agent 开始一轮工作前，先运行 `scripts/agents/sync.sh <角色>`，再读本文。

## 1. 角色与身份

所有 agent 都用仓库所有者的 GitHub 账号提交，GitHub 上分不出是哪个 agent 写的。因此，**每个 issue、PR 描述和评论的第一行**都必须写角色，并附上隐藏标记：

```
[auditor] 正文……
<!-- agent: auditor-remote -->
```

| 角色 | 标签 | 职责 |
|---|---|---|
| `coord` | `owner:coord` | 分诊，定优先级和阶段，维护协调看板。合并其他角色发起的 PR（自己发起的 PR 由仓库所有者合并，见 §4.1） |
| `impl` | `owner:impl` | 按计划实现、修复审计发现、开 PR |
| `auditor` | `owner:auditor` | 审计代码和 PR、提交发现、复审修复，在真机或浏览器上做验证 |
| `design` | `owner:design` | 设计稿、设计改版提示词、UX 文案、海报和 OG 图的视觉 |
| `user` | — | 仓库所有者，唯一的批准人。带 `needs-user` 的事项由他决定；负责复审并合并 coord 发起的 PR |

正文、提交信息和 PR 描述里**不得**出现 AI 助手的署名或尾注，比如指向 AI 的 `Co-Authored-By`、"Generated with …" 之类。作者和提交者一律是仓库所有者。CI 的 `protocol` 任务会检查这一点。

## 2. 标签

| 组 | 标签 |
|---|---|
| 类型 | `type:finding`（审计发现）、`type:task`（任务）、`type:decision`（需要拍板的选择） |
| 优先级 | `P0`（合并或进入下一阶段前必须修）、`P1`（本阶段内修）、`P2`（排进计划） |
| 状态 | 主线 `status:triage` → `status:accepted` → `status:claimed` → `status:in-review` → `status:verified`；旁支 `status:rejected`、`status:blocked` |
| 负责 | `owner:coord`、`owner:impl`、`owner:auditor`、`owner:design` |
| 阶段 | `phase:1`、`phase:2`、`phase:3`，含义见第 9 节 |
| 其他 | `needs-user`（等仓库所有者决定）、`board`（只用在协调看板上） |

一个 issue 同一时刻只能有一个 `status:*` 标签。改状态请用 `scripts/agents/status.sh`。Action 也会自动去掉旧的状态标签，详见第 8 节。

## 3. 审计发现的生命周期

1. **提出**（auditor）
   - 每条发现单独开一个 issue，使用 `audit-finding` 模板，打上标签 `type:finding`、`P*`、`status:triage`。
   - 一份审计报告里有多条发现时，报告本身可以作为 `docs/audits/` 下的文件走 PR，但**每条发现仍然要单独开 issue**，issue 正文链接到报告里对应的段落。
2. **分诊**（coord）
   - 接受：状态改为 `status:accepted`，加上 `owner:*` 和 `phase:*`，评论 `TRIAGE: accept owner=impl phase=1`。
   - 拒绝：状态改为 `status:rejected`，评论 `TRIAGE: reject` 并写明理由。auditor 认为理由不成立时，可以评论 `APPEAL` 重新讨论。
   - 需要决定：加 `needs-user`，交给仓库所有者。
3. **认领**（owner）：评论 `CLAIM`，状态改为 `status:claimed`。两条 `CLAIM` 冲突时，以时间早的为准，晚的一方撤回。
4. **修复**（owner）：PR 描述里写 `Fixes #N`，一个 PR 可以修多条。PR 进入复审时，issue 状态改为 `status:in-review`。
5. **复审**（auditor）
   - PR 合并后，评论 `VERIFIED sha=<合并后 main 的 40 位 SHA>`，状态改为 `status:verified` 并关闭 issue。
   - 修复不对时，评论 `REOPEN` 并写理由，状态退回 `status:accepted`。

`type:task` 走同样的流程。区别是：任务通常由 coord 直接以 `status:accepted` 开出；完成后由非作者的角色给出 `VERIFIED`。

## 4. PR 协议

- **分支前缀按角色区分**：`impl/…`、`audit/…`、`design/…`、`coord/…`。每个角色**只往自己的分支推送**。
- **`main` 有分支保护**：只能经 PR 合并，CI 必须全绿，禁止强制推送。
- **PR 描述使用模板**：第一行写角色标签，然后写清关联的 issue（`Fixes #N` 或 `Refs #N`）、测试证据，以及对计划的偏离。
- **复审结论用评论表达**。所有角色共用一个账号，无法用 GitHub 的 Approve 批准自己的 PR，所以写成：
  ```
  REVIEW: approve sha=<40 位 head SHA>
  REVIEW: changes sha=<40 位 head SHA>
  <理由，逐条>
  ```
- **复审之后又推送了新提交**：作者必须评论 `PUSHED sha=<新 SHA>` 并说明原因，之后需要重新 `REVIEW`。
- **合并条件**，必须同时满足：
  1. 有一条**有效的** `REVIEW: approve`（见下面的「复审独立性」），并且它的 sha 等于当前 head。合并时用 `gh pr merge --match-head-commit <该 SHA>` 锁定；
  2. CI 全绿。要求首跑即绿；靠重跑才通过的，需要在 PR 里说明原因；
  3. 关联的 `P0` 发现都已处理；
  4. 仓库所有者批准过这个 PR，或批准过它所属的阶段。

### 4.1 复审独立性

所有 agent 共用仓库所有者的账号，GitHub 和权限系统都只能看到「这个账号在批准自己的 PR」。所以复审是否独立，要靠**会话**来区分，而不是靠角色标签：

- **作者会话不复审、不合并自己的 PR**。哪怕它换一个角色标签去写 `REVIEW: approve`，也视为无效。判断依据是评论里的 `<!-- agent: … -->` 标记：它和 PR 描述里的标记相同，就算作者本人。
- **复审必须来自另一个 agent 会话**，并且这个会话的角色不能和作者相同：

  | PR 作者 | 复审方 | 合并方 |
  |---|---|---|
  | `impl` | `auditor`（首选）或 `coord` | `coord` |
  | `design` | `coord` | `coord` |
  | `auditor` | `coord` | `coord` |
  | `coord` | **仓库所有者** | **仓库所有者** |

- **coord 自己发起的 PR**（协议、流程、看板脚本等）由仓库所有者复审并合并。仓库所有者可以在 GitHub 上直接合并，也可以评论 `REVIEW: approve sha=<SHA>` 留个记录。合并之后，coord 只负责回写看板和相关 issue。
- **复审会话的标记**要写清楚角色和位置，比如 `<!-- agent: auditor-remote -->`。同一个会话在整个 PR 期间只能用同一个标记。
- **没有可用的复审会话时**，PR 停在合并队列里，在 PR 下评论 `BLOCKED: 等复审`。不得由作者会话代为复审。
- **看板会自动识别**：`board.sh` 生成合并队列时，会忽略标记和 PR 作者相同的 `REVIEW`，并把这类评论标成「⚠️ 作者自审，无效」。
- **design 的 PR 必须附上改动屏的截图**，放在 `design/guandan-offline/screenshots/`。

## 5. 协调看板

协调看板是一个置顶 issue，带 `board` 标签。正文分两部分：

- **手写区**（在 `<!-- board:auto:start -->` 之上），只由 coord 维护：
  - 当前阶段和目标
  - 阶段的批准状态
  - 风险和备注
- **自动区**（两个 `board:auto` 标记之间），由 Action 根据标签自动生成，任何人都不要手改：
  - 各角色手上的工作
  - 待分诊
  - 合并队列（PR、已复审的 SHA、是否与 head 一致）
  - `needs-user` 清单
  - 阻塞项

coord 每次合并或分诊后，确认一下手写区是否需要更新。其他角色不改看板正文，有事在看板下评论。

## 6. 本机与远程的中转

- **远程 agent 只看 GitHub。** 开工前运行：
  ```bash
  scripts/agents/sync.sh auditor   # 换成自己的角色
  ```
  它会依次输出：看板、自己名下打开的 issue、待复审的 PR、`needs-user`。
- **本机 agent 的进展必须落到 GitHub 上**（issue 评论或 PR），不能只留在本机会话的消息里，否则远程看不到。
- **coord** 把远程提出的事项转给本机 agent 时，要注明 issue 编号；本机 agent 的结论由 coord 回写到对应 issue。
- 不做高频轮询。每个角色在开始一轮工作时读一次 GitHub 就够了。看板由 Action 实时刷新，不需要任何 agent 盯着。

## 7. 评论关键字一览

| 关键字 | 谁写 | 含义 |
|---|---|---|
| `TRIAGE: accept owner=<角色> phase=<N>` / `TRIAGE: reject` | coord | 分诊结论 |
| `APPEAL` | auditor | 对拒绝提出异议 |
| `CLAIM` | owner | 认领 |
| `REVIEW: approve sha=<SHA>` / `REVIEW: changes sha=<SHA>` | 复审方 | PR 复审结论 |
| `PUSHED sha=<SHA>` | PR 作者 | 复审后又推送了新提交 |
| `VERIFIED sha=<SHA>` / `REOPEN` | auditor（任务则由非作者角色） | 验证结论 |
| `BLOCKED: <原因>` | 任何角色 | 被阻塞，同时把状态改为 `status:blocked` |

## 8. 同步体系

| 组件 | 作用 | 谁用 |
|---|---|---|
| `scripts/agents/labels.sh` | 创建或刷新全部标签。幂等，可以重复运行 | coord，仓库初始化时 |
| `scripts/agents/sync.sh <角色>` | 一轮工作开始时的只读快照 | 所有角色 |
| `scripts/agents/status.sh <N> <状态> [评论]` | 切换状态标签：去掉旧的 `status:*`，加上新的，可以顺带发一条评论 | 所有角色 |
| `scripts/agents/board.sh` | 重新生成看板的自动区 | Action；coord 也可以手动跑 |
| `.github/workflows/issue-sync.yml` | 打上新的 `status:*` 时自动去掉其他状态标签；issue、PR 或评论有变动时刷新看板；每天定时兜底刷新一次 | 自动 |
| `.github/workflows/protocol.yml` | 检查 PR 第一行的角色、AI 署名，以及 impl/audit 分支的 PR 是否关联了 issue | 自动 |

脚本默认操作 `dayuer/jkt-guandan`，可以用环境变量 `REPO=owner/name` 覆盖。依赖已登录的 `gh`。

## 9. 阶段

阶段对应 [docs/h5-pwa-plan.md](../h5-pwa-plan.md) 第 10 节：

| 标签 | 内容 |
|---|---|
| `phase:1` | 微信内报名、身份系统（含 claim）、海报和话术分享、日历；设计稿 H5 改版 |
| `phase:2` | Web Push（先 Android，再 iOS PWA）、11–14 引导屏、WhatsApp OG |
| `phase:3` | 蛋蛋 Agent、牌友关系、学牌 |

进入下一阶段的条件：本阶段的 `P0` 全部 `verified`，并且仓库所有者在看板下评论 `APPROVE phase=<N>`。
