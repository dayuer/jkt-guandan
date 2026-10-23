> 移植自 yardmaster 工作流，版本 dev，2026-10-23。
> 模板来源：dayuer/yardmaster 的 `docs/WORKFLOW.md`；项目名与仓库地址已替换。

# Master 工作流

> 本仓库默认**未启用 CI**；文中依赖 CI 的门禁（`go-test`、merge preview、分支保护）需要项目主人自行搭建后才成立。

本文定义 master 从需求到关闭 issue 的唯一流程。其他角色按此执行。与 `COORDINATION.md` 冲突时：**流程顺序以本文为准，权限与门禁仍按 COORDINATION.md。**

## 0. 一句话

需求 → 拆解 → 派工 → 回执 → 审计 → 裁决 → 合并 → 关闭。每一步都有消息和 issue 评论，缺一步不算完。

```
owner ──需求──> master ──拆解──> 子 issue
                    │
                    ├─ dispatch ──> worker ──done──> master
                    │                                  │
                    │                          开 PR、派审
                    │                                  ▼
                    │                              auditor
                    │                                  │
                    │                         approve / changes
                    │                                  │
                    ├─ 裁决（难題/分歧）<──────────────┘
                    │
                    ├─ ym-gate ──> merge ──> verified ──> close
                    └─ 全部子任务 verified ──> close requirement
```

## 1. 状态机

```
requirement: triage → accepted → in-progress → verified → closed

task:        accepted → claimed → in-review → verified → closed
                          │           │
                          └─ blocked ─┘
                          └─ changes ─┘   （verdict，不改 label，仍是 in-review）
```

- `blocked` 同时打 `status:blocked`，解除后回到 `claimed`。
- `changes` 是 `REVIEW:` 的结论，不是新 label；任务继续 `in-review`，修复后重新审计。
- `verified` 以合并后的 sha 为准；需要真机验证的写明「待真机」。

### 1.1 优先级与阻塞链

每轮先按 `P0 > P1 > P2` 排序；同一优先级里，先做**能解锁下游最多**的任务，再按创建时间。

- 拆解时每个 `type:task` 必须写 `deps`；没有依赖写「无」。
- 被阻塞的任务必须写明 blocker（issue/PR 编号），并挂 `status:blocked`。
- blocker 未开始或无人负责时，**先派 blocker**；不允许绕过去做低优先级新活。
- blocker 超过 30 分钟没有进展（无 claim/step/commit）→ master 改派或升级 owner，并记 `DECISION:`。
- 每轮巡检固定检查一次优先级与阻塞链，不只看收件箱。

示例看板（Yardmaster 当前快照）：

| 优先级 | issue | 状态 | 阻塞关系 |
|---|---|---|---|
| P0 | —（当前无） | — | — |
| P1 | 上游 agent 名字复用 | in-review | 解锁 上游 |
| P1 | 上游 master 写租约 | accepted | blocked-by 上游（已合并，可开工） |
| P1 | 上游 合并门禁 finding | triage | 待派工 |
| P1 | 上游 流程文档 | in-progress | — |
| P2 | 上游 审计发现汇总 | triage | — |

## 2. 七个阶段

### 2.0 每轮开始：收件箱优先分流

master 每轮的第一步不是写东西，也不是先做自己的事，而是**扫收件箱并逐条分流**。消息在收件箱里停留未判定，就算失职。

逐条三选一，按顺序执行：

| 类别 | 典型消息 | 动作 | 时机 |
|---|---|---|---|
| 立即派发 | `ask`/`blocked` 待裁决、`review changes`、`done`、需求待拆解 | 当轮发 `dispatch` / `review_request` / `DECISION` / `ym plan` | 判定后立刻发，不攒批 |
| 自处理队列 | `review approve` 待合并、`verified` 待关单、`ym post`、看板维护 | 入队，派完再取队首 | 每轮按优先级消费 |
| 记账关闭 | `claim`、`step`、过期回执 | `ack`，必要时落评论 | 随手处理 |
| 通告 | `reminder`、`idle` | 读完即可，不用 ack；ack 了也只记一次活动 | — |

自处理队列的优先级：

1. 阻塞他人的（`overdue`、`dead`、`blocked`、`ask`）；
2. 已通过两段审计、待合并（head 上 standard 与 final 各一张 approve，顺序正确）；
3. `verified` 待关单；
4. 落评论与看板维护；
5. 同优先级 FIFO。

规则：

- **派发不排队。** 判定为“该派”就立即发；自己的合并、关单可以等。
- **自处理有队列。** 队内按上述优先级，不按“先看到先做”。
- **一轮一清。** 每轮结束前收件箱不得有未判定消息；处理完 `ack`，留下的必须已入队。
- 新 `review approve` / `verified` 到达时，重排自处理队列。

### 2.0.1 每阶段执行者与复核者

| 阶段 | executor | reviewer | 复核产物 |
|---|---|---|---|
| 拆解 | master | owner | 范围、验收标准、依赖 |
| 派工 | master | — | 门禁即检查（无验收不派工） |
| 回执与开 PR | worker | master | commit、文件面、PR 描述 |
| 审计 | auditor（standard） | 另外一名独立 final auditor | 两段：standard approve → final approve；任一段 changes 即打回 |
| 裁决 | master | owner（`needs-user`） | `DECISION:` |
| 合并 | master | `ym-gate` + owner 批准 | 六道门禁 |
| 关闭 | master | auditor | `VERIFIED sha=` |

**独立复核红线**：复核必须由独立子代理执行，使用独立上下文；执行者不得复核自己的产物，也不得与复核者同体。同一 PR 的两段审计票（standard、final）必须来自两个不同 agent，且都不能是作者。

### 2.1 拆解

**谁**：master。**命令**：`ym req new`、`ym plan`。

1. 需求先成 `type:requirement` issue：`ym req new --title ... --body ... --priority P1`。
2. master 写 plan JSON（examples/plan.example.json）。
3. `ym plan --epic <n> --file plan.json` 批量建子 issue，并把任务列表写回需求 issue。
4. 每个子 issue 必须有：目标、范围、验收标准、依赖、波次、文件面。
5. `deps` 写具体 issue/PR 编号或「无」；被阻塞的任务挂 `status:blocked` 并写清 blocker。blocker 未开始或无人负责时先派 blocker（见 §1.1）。

**门禁**：没有验收标准的子 issue 不派工。没有拆解的需求不派工。blocker 不明或未排进派工顺序的任务不派工。

### 2.2 派工

**谁**：master → worker。**命令**：`ym supervise`、`ym send --event dispatch --task <issue> --summary <摘要>`、`ym post`。

派工顺序按 §1.1：`P0 > P1 > P2`；同级先派能解锁下游最多的任务，再按创建时间。阻塞链前方未开工时先派 blocker，不派低优先级新活。

1. `ym supervise --json` 找空闲 worker：`in_progress=0`、`queued` 少、在线。
2. 检查文件面：与其他活跃 `CLAIM` 重叠就不派，或先 `BLOCKED`。
3. `ym send --to worker-N --event dispatch --task owner/repo#N --summary "..." --goal ... --scope ... --acceptance ... --deps ... --wave ... --why ...`，带目标、范围、验收、依赖、波次，以及**必填的 `why`**：为什么是现在派这一单（排队位置、优先级依据）。缺 `why`（或少于 8 个字符）服务端直接拒绝，并一次报出所有缺失的字段；每条 dispatch 的 `why` 同时自动写成一条 `dispatch_rationale` 观察，`ym brief` 与 `ym memory search --source obs` 都能查到。派工前先跑 `ym brief <task>`。
4. `ym post <msg_id>` 落成 issue 评论。
5. issue 保持 `status:accepted`，等 worker `CLAIM`。

**门禁**：一个 worker 同时只接一个任务；文件面重叠时串行。

### 2.3 回执与开 PR

**谁**：worker → master。**命令**：`ym claim`、`ym commit`、`ym send --event step/done --task <issue> --summary <摘要>`、`ym pr body`、`gh pr create`。

1. worker 认领：`ym claim` → 发 `claim`（文件面、风险、≤4 步）。
2. 每步一个 commit（`ym commit -m ...`，它加 `Delivered-By: <project>/<agent>` trailer），发 `step`；每步测过，没验到的写出来。
3. 完成推分支，发 `done`（branch、commit、summary）。
4. master 收到 `done`：
   - 核 branch/commit 存在、文件面没越界；
   - `ym post <msg_id>` 落评论（`<!-- agent: ... -->` marker 自动注入）；
   - `ym pr body --task owner/repo#N --body-file draft.md > body.md` 生成 PR 正文：
     固定带一行可读的「Delivered by <agent> (model ...), done <msg_id>, branch ... @ <sha>」
     和一行机器可读的 `<!-- delivered-by: agent model msg branch commit -->`（交付
     agent 取自 `done` 消息，模型取自服务端 agent 的**当前**记录——消息里没有历史
     模型，所以它不是交付当时的快照，换了模型的 agent 会显示新模型；而且这个值是
     **入网时自报、未经核实**的，`ym whoami` 与真实运行的模型都可能不一致，闸门
     不得把它当作事实；机器行用 `msg:` 是 上游 闸门解析器的键名契约）；
   - `gh pr create --body-file body.md`，草稿里写 `Fixes #N` 和五项；
   - issue 改 `status:in-review`。

**为什么**：所有 agent 共用同一个 GitHub 登录与 git 作者，GitHub 侧分不出谁写的。
所以凡是 agent 写入 GitHub 的内容都要带 marker：评论走 `ym post` 自动注入，PR 正文走
`ym pr body` 的交付行，commit 走 `ym commit` 的 trailer。
5. worker 报 `blocked`/`ask`：master 先裁决或改派，不带着疑问开 PR。

**门禁**：没有 `done` 和可核对的 commit 不开 PR。

### 2.4 审计

**谁**：master → auditor。**命令**：`ym send --event review_request --task <issue> --summary <摘要>`、`ym post`、`ym send --event review --task <issue> --summary <摘要>`。

两段顺序执行：先 standard，后 final，**同一个 head**；standard 没 approve 之前不派 final。

1. master 选一个**非作者**的 standard auditor，独立子代理、独立上下文。
2. 发 `ym send --to auditor-S --event review_request --task owner/repo#N --summary "..." --sha <head> --acceptance ...`。
3. `ym post <msg_id>` 落评论。
4. standard auditor 独立审计：正确性、检查是否齐、是否按验收标准执行、设计四问；回 `review`：`approve` 或 `changes`，带 40 位 sha、checks、逐条 findings。审计阶段不改代码；打回后按下面的修复流程提交修复。
5. `approve` 后，master 把**同一个 head** 派给一个**非作者且不同**的 final auditor，重复 2–4；`changes` 则不派 final，走下面的修复流程。
6. 两段都 `approve` 后，各自把两行票评论贴到 PR 上（见下方“谁写 GitHub”与投票纪律）；findings 只走 `review` 消息，由 master 归档。

**打回与修复**：`changes` 必须带 `payload.fixes`（服务端 422 强制），每项 `{file, change, test?}`。

- **standard 阶段**：投 `changes` 的 auditor 自己提交修复到 PR 分支，用 `ym fix-commit -m "..."`（自动加 `Ym-Agent: <project>/<agent>` trailer），只改自己票里列出的问题；`review` 消息带 `fix_sha`（40 位）与 `fixes` 作为改动说明。
- **final 阶段**：final auditor **永不提交代码**，只投 `changes` 附 `fixes`；master 另派一名 standard auditor（X）提交修复（同样带 trailer）。
- master 收到 `fix_sha` 后用 `dispatch` 发还原 worker 检查：核对修复与 `fixes` 一致、跑 build/test/race/vet，worker 回 `done`（commit=fix_sha 或其上的修正）；有异议用 `ask`。
- worker 确认后重走两段：standard 重审换一名**没参与修复**的 standard auditor（Y≠X），再交 final 终审。
- 闸门只看**最新一条**带 `Ym-Agent` trailer 的提交：该作者不投以自己修复为最新修复的 head（`author of commit <sha>; cannot approve own fix`），所以 standard 修复者不能给自己修的 head 投 standard 票，final 阶段也永远不会是自审；之后别人再提交修复，前一位修复者即可重审。

**门禁**：当前 head 上要有 standard 与 final 各一张 `REVIEW: approve`，且 **final 票不早于 standard 票**（取两段各自最晚的一张比较：standard 在 final 之后重投，会让这张 final 失效）。任一段投 `changes` 即打回，换 head 后两段都作废。同一 agent 同一 sha 只计最新票；只认带 agent marker 的 `REVIEW:` 评论——无 marker 的评论与 GitHub native review 都不计票，避免同一审计者跨渠道投两票。作者、standard 与 final 必须是三个不同 agent，执行者不得复核自己的产物；作者取自 PR 正文的交付行，交付行缺失时这条检查降级为 best effort（`ym pr body` 总会写，手写正文才会缺）。auditor 的 stage 由管理员通过 `ym admin agent set-tier` 指定，评论里自报的不算；服务端没有给段位的 agent（未入网的名字、已吊销者）票**不计数**，只在 diagnostics 里列出——死身份不能签字，伪造 marker 也不能否决。只有段位来源本身取不到（凭证失败、服务端不可达）才整体 fail closed。

**谁写 GitHub**：worker 不写 GitHub（评论、PR 正文、label 一律不碰）；auditor 只允许贴一种内容——自己的 `REVIEW` 票评论（严格两行：首行 `[auditor] REVIEW: approve|changes sha=<40 位>`，第二行 `<!-- agent: <project>/auditor-<name> -->`，其后可选带 `msg:<review 消息的 msg_id>` 作为回溯链接；marker 本身必需，闸门只解析 agent 名，两种形态都计票），其余一律不写；master 落所有其他评论（派工记录、裁决、findings 归档、合并留痕）。理由：票必须由投票者本人发出才有归属，master 代贴等于能伪造任意 auditor 的票，双审机制随之失效。详细 findings 走 `ym send --event review` 发给 master，由 master 归档，不贴在票上。

**auditor 投票纪律**（每条都造成过实际损失）：

1. **票必须贴在 PR 上。** 闸门只认 PR 评论，以及该 PR 正文用 `Fixes`/`Closes`/`Resolves`/`Part of` 关联的 issue 上的评论；贴到别的 issue 上，闸门既不计票也不报错，票就这样凭空消失，直到有人发现 `approve_sha` 一直不过。一律贴 PR，不依赖关联关系。
2. **sha 必须完整 40 位。** 闸门的 `reviewRe` 只匹配 40 位十六进制，写 7 位短 sha 会被静默丢弃，同样不报错。
3. **投票前先核真实 head。** 收到派工单先跑 `gh pr view <pr> --json headRefOid --jq .headRefOid`，用它的输出核对派工单里的 sha 再投。派工单里的 sha 可能是错的（master 写错过一次，是 auditor 核出来的）；对着错 sha 投票，票落在不是 head 的提交上，闸门不计。核出不一致，先 `ym send --event ask` 告知 master，不要自己挑一个。
4. **重投是安全的。** 同一 agent 同一 sha 只计最新票，所以改主意或贴错后补一张即可，不必（也不能）删旧评论；head 一变，旧 head 上的票全部作废，必须在新 head 上重投。

**master 派票纪律**：`review_request` 里的 `--sha` 一律用 `gh pr view <pr> --json headRefOid --jq .headRefOid` 取到的完整 40 位 sha，禁止从短 sha 推、补或凭记忆写。派出后 head 又前移（rebase、追加提交）的，旧票作废，按新 head 重派。

### 2.5 裁决

**谁**：master；必要时 owner。**命令**：`ym send --event decision/ask --task <issue> --summary <摘要>`、`ym post`。

触发条件：

- worker 与 auditor 分歧；
- 修复两轮仍是 `changes`；
- 设计取舍、范围变化、跨任务冲突；
- 依赖未合、契约不清。

动作：

- master 出 `DECISION:`，写理由，按决定继续；
- 需要 owner 拍板的加 `needs-user`，停下等，不猜；
- master 不写代码、不复审；要修就派给 worker。

**门禁**：没有 `DECISION` 或 owner 批准，不越过分歧合并。

### 2.6 合并

**谁**：master。**命令**：`ym-gate check <pr>`、`ym-gate merge <pr>`。

六条缺一不可：

1. 当前 head 上先有 standard、后有 final 的有效 `REVIEW: approve`（同一 agent 同一 sha 只计最新票；任一段 changes 即不过；只认首行为 `[auditor] REVIEW:` 且带 auditor marker 的 PR/issue 评论，正文夹带不计票）；
2. head 上该跑的检查全部通过；
3. 合入后的代码已被 CI 验过：CI 跑在「本 PR 合入 base 当前 tip」的 merge preview（`refs/pull/N/merge`）上且为绿。有合并冲突（没有 preview）、或 CI 所测的 base 已不是 base 当前 tip，都不过；分支本身落后不要紧，不需要 rebase。要线性历史的仓库用 `--up-to-date strict`（或 `YM_GATE_UP_TO_DATE=strict`）回到「分支必须包含当前 base head」；启用 GitHub merge queue 时视为等价；
4. 关联 `P0` 全部关闭（本 PR 通过 `Fixes`/`Closes`/`Resolves` 将关闭的 P0 允许合并时关闭；`Part of` 的 P0 仍须已关闭）；
5. owner 批准过这条 PR 或它所属阶段；
6. 合并前重读最新评论，没有新的 `changes` 或阻塞。

**为什么 up_to_date 看 merge preview 而不是分支是否落后**：旧规则「分支必须包含当前 base」在 n 个 PR 的积压上是 O(n²)——合掉一个，其余全部失效；修复手段是 rebase，rebase 会让 head 前移，head 前移会让所有票作废、要重新审计。而真正要保证的是「合并之后的代码被测过」，这由 merge preview 上的 CI 给出：`pull_request` 工作流的 `actions/checkout` 默认检出的就是 `refs/pull/N/merge`（已用真实 run 的日志核实：`git fetch ... +<sha>:refs/remotes/pull/N/merge`，提交信息为 `Merge <head> into <base>`）。

**CI 测的是哪个 base，不能读 run 对象上的 `pull_requests[].base`**：它是按 PR 当前状态渲染的，不是 run 发生时的快照（实测：几小时前的 run 报的是今天的 tip，PR 关闭后则为空），信了它每个 run 都显得是新的，stale 永远不会触发。闸门读的是不会变的记录：run 的 checkout 日志给出它检出的 merge commit，该 commit 的第一个父提交就是当时合入的 base。每个 `pull_request` 工作流各取最新一个 run，都要在当前 tip 上跑绿。internal/gate/live_test.go 可以对真实 run 复核这一点（需设环境变量，只读）。

base 前进后，旧结果就是针对旧 base 的，gate 报 stale。刷新 preview 不需要动分支，也不动 head，票继续有效；办法是 `gh pr close N && gh pr reopen N`（`pull_request` 工作流监听 `reopened`），**但一次不够，要分两步**，gate 会按状态告诉你现在该做哪一步：

1. `merge ref is stale`：GitHub 还没重建 `refs/pull/N/merge`（它的第一个父提交还是旧 base）。close+reopen 让 GitHub 重建它。
2. `merge preview is stale for CI`：ref 已经是新的，但 CI 那次 run 测的还是旧 base。再 close+reopen 一次，让 CI 从已经重建好的 ref 起跑。

为什么要两步：reopen 同时做了两件事——让 GitHub 重建 merge ref，并立刻启动 CI——而这两件事在赛跑（赛跑是对观察到现象的解释；现象本身见下表）。所以第一次 reopen 先把 ref 换新，第二次才能让 CI 测到新的。三个条件都满足才通过：ref 的父1 等于 main 当前 tip，ref 合并的 head（父2）就是 PR 当前 head，且 CI 所测的 base（run 检出的 merge commit 的父1）也等于 main 当前 tip。

**第二步的实测证据（测量于合并前，2026-10-22）**。PR 132 合并后 `refs/pull/132/merge` 已经 404，ref 本身无法再复核；下面每个 run 的 sha 都留下了，run 日志与 merge commit 不会随之消失。run 号、时间、head、merge commit 及其第一个父提交，是 2026-10-22 对 GitHub 只读核对的结果（`gh run view <id> --log` 里 checkout 的 `+<sha>:refs/remotes/pull/N/merge`，再 `gh api repos/dayuer/yardmaster/git/commits/<sha>` 取 parents）：

| PR（head） | run | 起跑（UTC） | CI 检出的 merge commit | 其第一个父提交 = CI 所测 base | 结论 |
|---|---|---|---|---|---|
| 132（`d68bdfe`） | 39741139732 | 07:31:59 | `6c2fe03` | `861d38d`（旧） | 起点：base 已落后 |
| 132 | 39745608606 | 08:09:03 | `6c2fe03`（与上一个 run 完全相同） | `861d38d`（旧） | **第一次 reopen 之后，CI 仍检出旧 merge commit** |
| 132 | 39746824228 | 08:19:01 | `16bb88a` | `635c810`（当时的 main tip） | **第二次 reopen 之后，CI 检出重建后的 merge commit**；绿；132 于 08:23:13 合入 |
| 144（`6941da2`） | 39749417072 | 08:39:49 | `18eb4cd` | `00a5499`（当时的 main tip） | 绿；144 于 08:43:28 合入 |

两点要分开说清：(1) 表里的事实，即哪个 run 检出了哪个 merge commit、其父1 是谁，是上面核对出来的，可复核；(2) 「第一次 reopen 之后 ref 已经是新的、CI 却仍旧」中「ref 已新」这一半，以及 gate 当时报 `up_to_date` 转 PASS，是 master 当时对 gate 输出的观察，ref 已不在，无法重现。PR 144 的两步过程同样来自 master 的观察，这里只留下了最后那个 run 的可核对数据。PR 148 同样先后两步后通过并合入，master 观察所得，本文没有逐 run 核对，不作为证据。

**已实测与未实测，分开写：**

- 已实测：reopen 会让 GitHub 重算合并预览（但 CI 的那次 run 可能仍检出旧 merge commit，见上表）。用 PR 90（当时落后一次合并）做被试：close+reopen 之前，merge ref 的第一个父提交是旧 base（`97cbb90`）；之后是当时的 main tip（`91a1b8c`），与 main 一致。这同时印证了闸门取 base 的方式：merge ref 的 parent 1 就是该预览对应的 base。
- 未实测：重跑旧 run（`gh run rerun`）不会刷新预览。这是按 `actions/checkout` 的机制推断的（它取的是原事件那个具体的 merge commit sha，重跑应复用它），不是观察到的结果。所以文案写「重跑不是刷新」并标明是推断；要证明它需要再做一次写 GitHub 的实验。
- 未实测：「有合并冲突时 GitHub 不生成 merge ref」。闸门对冲突的判断读的是 PR 的 `mergeable` 字段（`CONFLICTING`），并不依赖这一点，但这条分支没有在真实仓库上跑过。

这条放宽没有放掉语义冲突：两个分支各自 CI 绿、合起来红（一边改函数签名、一边新增调用），在刷新后的 preview 上 CI 会红，up_to_date 与 checks 都不过。internal/gate/preview_semantic_test.go 用真实的 git 仓库、真实的 merge commit 和 `go build` 复现了这个场景，并让 base 真的前进一次。

**P0/BLOCKED 一票否决**：关联 `P0` 未关闭，或 PR/关联 issue 上有 `BLOCKED:` / ⛔ finding，直接否决合并，不受两段票型影响。修复并推送后旧 head 的票全部作废，必须在新 head 上重新投票。

```bash
ym-gate check <pr>     # 只读，逐条输出六条 pass/fail 与原因
ym-gate merge <pr>     # 六条全过后 gh pr merge --match-head-commit，并在 PR 留门禁评论
ym-gate verify-main    # 合并后检查 main push 的 go-test
```

**check 与 merge 之间 base 会前移**：`--match-head-commit` 只锁 head，不锁 base。「CI 跑在当前 base tip 的 merge preview 上」说的是某一个 base tip；而每合入一个 PR，base 就前进、其余所有 PR 的 preview 随之陈旧，所以 check 通过到 merge 落地之间 base 在动是常态，不是罕见竞态。`merge` 因此在写任何东西（状态、合并、评论）之前重读一次 base tip，与 up_to_date 判定所依据的 tip 不一致就拒绝，读不到、或 check 没记下 tip 也拒绝（未知的 base 不当作没变）。preview 模式的拒绝信息要求刷新 preview（`gh pr close N && gh pr reopen N`，等 CI，再 check），**不是 rebase**：rebase 会作废票。strict 模式则需要 rebase。这把窗口缩小到「重读」与 GitHub 执行合并之间的一次 API 往返（秒级以内），**不能消除**：没有服务端串行化的情况下 what lands is tested 只在这个间隙之外成立。根治是 merge queue / `ym-autopilot`。

实现状态：cmd/ym-gate + internal/gate。`check` 不写 GitHub；`merge` 任一条件不满足即返回非零且不设状态、不合并、不评论。合并命令固定带 `--match-head-commit <head>`：head 在检查与合并之间变动时 GitHub 拒绝合并，闸门绝不合并未被检查过的 sha。合并成功后自动在 PR 留一条评论，逐条列出六道门禁的实际结果，账本可回溯（评论失败只告警，不回滚已完成的合并）。

**`GATE_TOKEN`**：用途仅是给 head 写 `review-gate` commit status（供分支保护作为 GitHub 侧信号）。它是专用 token，与 master 的 gh 凭据分离，值不进消息、评论、提交。没有它时 `merge` 降级而不是拒绝：六道门禁全过仍然合并，只跳过 `review-gate` 状态，并在输出（`gate_status: skipped_no_gate_token`）和 PR 评论里明确标注。选择降级的理由：真正放行合并的是本地六道门禁与 `--match-head-commit`，状态只是附加信号；若缺 token 就拒绝，闸门会再次变成不能合并的只读检查器，逼人回到 `gh pr merge`。

**闸门是唯一合并入口**：合并一律走 `ym-gate merge`。直接用 `gh pr merge` 属于绕过闸门，只有 owner 明确指示才允许，并且必须在 PR 或关联 issue 上留痕（谁指示、为何绕过、合并的 sha）。

**合并后必须验 main**：main push 的 `go-test` 跑完后执行 `ym-gate verify-main`。红或缺失时立即 reopen 关联 issue 并开 hotfix，把结果写回 issue；不要只看 PR head 的绿。启用 merge queue 时仍要做这一步，它检查的是合并结果而不是 PR。

**自我进化（把 main 装成正在跑的服务）**：master 用 scripts/self-evolve.sh 做这一步。它不是第二份升级实现：`ym upgrade` 可用时只调它并把安装前缀传进去，五条终态判据仍由脚本自己独立核对——安装成功、7435/7436 由同一个进程服务、重启后的进程打开的 `yardmaster.db` 与旧进程是同一个文件、`/healthz` 返回 `ok:true`、`healthz.commit` 与本机 `git rev-parse HEAD` 是同一个 commit。任何一条不满足即非零退出并保留现场，不「尽力继续」；data dir 拿不到就报错，不允许降级成只比 cwd。**install 失败不重启**：让旧版本继续跑，比一个起不来或起错版本的服务端更好。安装前缀从 `command -v ym` 推导，不硬编码；旧进程的 `--config` 原样传给新进程；外部命令可注入，测试不得 kill 真实服务端或写真实安装目录。

必需 check：`go-test`（.github/workflows/test.yml，PR 的 `opened`/`synchronize`/`reopened` 与 push 到 `main` 触发，跑 build/vet/test -race）。分支保护需把 `go-test` 设为 required；`ym-gate` 的 checks 门禁依赖这个固定名称。

冲突：派 conflict-fix 任务给 worker；head 变了就重新审计。

**门禁**：不跳 gate；head 上的两段（standard → final）不齐，或任一段投了 `changes`，都不合并。

### 2.6.1 闸门的文本匹配规则（锚点一览）

闸门从评论里读三类信号：approve 票、阻塞、owner 批准。**它们各自的匹配规则必须写在这一处、能互相对照**：上游 已经为「一类只认行首、另一类做裸子串」付过代价（讨论标记本身就触发标记，把自己的 PR 堵死过），不要再出现。代码注释替代不了这一页，因为这一页是给写评论的人看的，不是给读代码的人看的。

下面每个例子都由 internal/gate/anchor_rules_test.go 逐条跑过真实的匹配函数，并断言它们出现在本节；各正则的源码也由该测试断言与本节逐字一致。改了正则而没改这一页，测试会失败。例子里 `\n` 表示换行，`<40hex>` 表示 40 位十六进制，`U+26D4` 表示那个禁止符号本身（不在文档里写字面量，它正是会触发阻塞的那个字符）。

正则源码（逐字）：

```
firstLineReviewRe   ^\[[^\]]+\]\s*REVIEW:
reviewRe            (?i)REVIEW:\s*(approve|changes)\s+sha=([0-9a-f]{40})
agentMarkerRe       (?i)<!--\s*agent:\s*([^\s>]+)
auditorAgentRe      ^[^/]+/auditor(?:-[^/]+)?$
blockerLineRe       (?i)^[-*>#`\d. \t]*(?:\[[^\]]+\]\s*)?(?:BLOCKED:|\x{26D4})
ownerRe             (?im)^\s*(?:\[[^\]]+\]\s*)?APPROVED by user\b
```

`blockedRe` 是旧名。上游 之后它是 `blockerLineRe` 加扫描器 `findBlocker`（逐行、跳过围栏代码块、去掉行内代码后再匹配）。

`ownerRe` 由扫描器 `ownerApproved` 逐行应用：闭合的围栏代码块（``` 与 ~~~）整体跳过，引用格式示例不算批准；**未闭合的围栏不算代码块**，其后的行按普通文本继续扫——否则一个忘记收尾的围栏会吞掉真实批准。代价是未闭合块里的 `APPROVED by user` 会被当真，写评论时请把围栏闭合（测试：`TestAnchorRulesOwnerApprovalSkipsClosedFences`）。

| 匹配器 | 读什么 | 锚点 | 大小写 | 匹配的例子 | 不匹配的典型例子 |
|---|---|---|---|---|---|
| `firstLineReviewRe` | 票的首行形态 | **行锚**，锚在评论的**第一个非空行**的行首（前面的空行和行首空白会被忽略）；必须有 `[角色]`，括号里写什么无所谓，身份看 marker | **区分**：必须是大写 `REVIEW:` | `[auditor] REVIEW: approve sha=<40hex>\n<!-- agent: jkt-guandan/auditor-1 -->` | `REVIEW: approve sha=<40hex>`（没有 `[角色]`）；`Looks fine.\n[auditor] REVIEW: approve sha=<40hex>`（票不在第一个非空行）；`- [auditor] REVIEW: approve sha=<40hex>`（不容忍 markdown 前缀）；`[auditor] review: approve sha=<40hex>`（小写 review） |
| `reviewRe` | 票型和 sha | **不锚**：在首行里做子串匹配（只有过了上一条才会跑到这里）；sha 后没有边界 | 不区分 | `[auditor] REVIEW: APPROVE SHA=<40hex>`；`[auditor]REVIEW:approve sha=<40hex>` | `[auditor] REVIEW: approve sha=abc1234`（短 sha）；`[auditor] REVIEW: maybe sha=<40hex>`（票型只有 approve/changes）；`[auditor] REVIEW: approve`（没有 sha） |
| `agentMarkerRe` | 投票者是谁 | **不锚**：在**整段正文任意位置**取**第一个**；「第二行」只是约定，不是规则；`msg:` 等后缀被忽略 | 不区分 | `[auditor] REVIEW: approve sha=<40hex> <!-- agent: jkt-guandan/auditor-1 -->`（和票同一行也算）；`<!-- agent: jkt-guandan/auditor-1 msg:m_abc -->` | 没有 marker（整张票不计）；正文里有两个 marker 时只看第一个，第一个是 `jkt-guandan/worker-1` 就不计 |
| `auditorAgentRe` | marker 里的名字是否是 auditor | **整串锚**（`^…$`），作用在 marker 取出的名字上 | 名字先转小写再比 | `jkt-guandan/auditor-1`；`jkt-guandan/auditor` | `auditor-1`（缺项目前缀）；`jkt-guandan/worker-1`；`jkt-guandan/auditor-1/x` |
| `blockerLineRe` + `findBlocker` | 阻塞 | **行锚**，逐行：行首允许 markdown 装饰（`-` `*` `>` `#` 数字 `.` 反引号 空白）和一个 `[tag]`，然后必须是 `BLOCKED:` 或 `U+26D4`；围栏代码块和行内代码里的**不算** | 不区分 | `BLOCKED: do not merge`；`- BLOCKED: x`；`> BLOCKED: x`；`1. BLOCKED: x`；`**BLOCKED:** x`；`[auditor] BLOCKED: no`；`context\nBLOCKED: x`（任意一行的行首都算）；`- U+26D4 x` | `the gate parses BLOCKED: markers`（不在行首）；`BLOCKED by x`（没有冒号）；`` `BLOCKED: x` is the marker ``（行内代码）；围栏代码块里的 `BLOCKED: x` |
| `ownerRe` + `ownerApproved` | owner 批准 | **行锚**（多行模式，任意一行的行首），允许缩进和一个 `[tag]`；`\b` 让 `users` 不匹配；**不容忍 markdown 前缀**；闭合的围栏代码块**不算**，未闭合围栏按普通文本继续扫 | 不区分 | `APPROVED by user`；`[coord] APPROVED by user`；`   APPROVED by user`；`context\nAPPROVED by user` | `- APPROVED by user`；`> APPROVED by user`；`NOT APPROVED by user`；`the owner said APPROVED by user yesterday`；`APPROVED by users`；`` `APPROVED by user` ``（行内代码，因为行首是反引号）；闭合围栏代码块里的 `APPROVED by user` |

**这张表里已知的不一致（如实登记，本节不改行为）：**

1. **owner 不容忍 markdown 前缀，blocker 容忍。** `- BLOCKED: x` 阻塞，`- APPROVED by user` 不算批准。这个方向是安全的（该批准没批准会被看见，该阻塞的不会漏）。
2. **`agentMarkerRe` 不是行锚。** marker 在正文任意位置、取第一个。写票的人把别人的 marker 引用在自己的 marker 前面，闸门就会读到别人的名字；`ym post` 会拆开 agent 文本里的 `<!--`，但人直接贴的评论不经过它。
3. **`firstLineReviewRe` 区分大小写，其余不区分。** 小写的 `review:` 不是票，也不会有任何提示（上游 的 ignored-votes 诊断会把它列出来）。
4. **`reviewRe` 的 sha 后没有边界。** 41 位十六进制会取前 40 位；它几乎一定不等于 head，所以不计票，但原因是「sha 不是当前 head」而不是「sha 位数不对」。

原已知不一致 1（owner 不排除代码、blocker 排除）已由 上游 修复：owner 与 blocker 现在都跳过闭合围栏代码块。

**流水线水位（事不能停）**：master 每轮开始先 `ym status`（别名 `ym st`），看每个 worker/auditor 的 `STATE`。worker 的未终结单低于 `min_queue`（默认 2）是 `STARVING`，0 单是 `AVAILABLE`，高于 `max_wip`（默认 3）是 `OVERLOADED`；`ym send --event dispatch` 超限返回 409 并点名当前队列，`--force` 可绕过但会写进消息 payload 与审计表并记日志（不绕过角色、租约和重复派工守卫；`review_request` 不适用）。阈值在 `ym doctor --min-queue` / `ym supervise --min-queue --max-wip` 与服务端 `[supervise]` 配置里，`ym status` 汇总行显示当前生效值。

水位低时的报告必须分两种，处置完全不同：**有可派工 issue** = master 的动作缺口（`ym doctor` 直接建议配对，但仍由 master 派）；**没有可派工 issue** = 供给枯竭，不是调度问题，从以下来源开新单：① auditor 的非阻塞 findings；② 里程碑里尚未拆的子单；③ 已合 PR 留下的显式后续；④ 复盘得到的流程缺口。可派工 = open 且带 `status:accepted` 或 `owner:impl`，排除 `status:triage` 与 `type:decision`（待 owner 拍板的不算）。

agent 未终结单清零并持续超过 `idle_notice_after`（默认 5m）时，服务端向 master 投一条 `idle` 通告；同一空闲区间只投一次。`idle` 是通告不是工作单：不计入 `IN_PROGRESS`/`QUEUED_DEPTH`，没有 ack 期限，不需要被 ack 才能清除。`ym doctor` 只报告和限流，不自动派工——自动派工是 autopilot 的范畴，其 `enabled` 开关由 owner 决定。

### 2.7 关闭

**谁**：auditor 验证，master 关闭。**命令**：`ym send --event verified --task <issue> --summary <摘要>`、`gh issue close`。

1. 合并后 auditor 发 `VERIFIED sha=<merge sha>`；需要真机的写明「待真机」。
2. master `ym post` 落验证评论。
3. `gh issue close` 关闭子 issue。
4. 需求的全部子 issue `verified` 后，master 关闭 requirement issue。
5. 更新看板。

**门禁**：关闭前必须 merged + verified；豁免要写明理由并记在 issue 上。

### 2.7 守端进程 ym-autopilot

把不需要判断的机械动作交给一个不调用任何模型的进程，吞吐就不再被人的注意力限制。它做四件事，也只做这四件：

| 动作 | 触发 | 幂等靠什么 |
|---|---|---|
| 开 PR | master 收件箱里有未处理的 worker `done`（带分支），且任务属于本仓库、`done` 的发件人就是该任务最近一条（未取消的）dispatch 的收件人 | 该分支已有 PR（任意状态，含已合并、已关闭）就不再开；PR 正文以 `Closes #<任务单>` 开头并带 `<!-- autopilot:open ... -->` 标记 |
| 合并 | 开着的 PR 经 `ym-gate check` 六道全过 | 只经 `Gate.Merge`（`--match-head-commit`、合并前重读 base tip）；合并留痕按标记去重 |
| 验证并关单 | 最近合并的 PR 正文里有 `Fixes/Closes/Resolves #N` 且 #N 仍开着 | 先在合并提交上跑 `go build ./... && go test ./... && go vet ./...`，**通过才关单**并留 `autopilot:verified` 记录（写明核了什么、哪个 sha）；单已关闭就不验证不动；`Part of` 不关 |
| 清孤儿消息 | 消息的收件人已被 revoke 且消息未终结（`GET /api/v1/orphans`） | 取消后它不再是孤儿；原因与消息号留在任务单的留痕评论里 |

**它不做的**：拆解、仲裁、范围决策、绕过闸门。遇到需要判断的，它跳过并在报告里写原因，或熔断并发 BLOCKED，交给 master。

**启用前提（硬编码的下限）**：上游 与 上游 必须已关闭，否则实跑直接拒绝启动（退出码 2）；读不到它们的状态也按未关闭处理。`--also-require-closed` 只能往上加，不能去掉这两项。原因：这两条是文本注入路径，上游 落地后 owner 门是「owner APPROVED 或 standing approval」，机器读到伪造文本就等于伪造合并授权。`--dry-run` 不受此限，只是报告「实跑会拒绝」。

**租约互斥（裁决）**：autopilot 持有 master 租约期间，人类侧的 master 会话 `acquire` 会得到 `lease_held`。这是有意的：autopilot 就是 master 的机械臂，它和一个活着的 master 同时动手，正是「并发的第二个 master」那类事故——两个 master 各自派工、各自合并，账本会对不上。所以不给 autopilot 单独的身份，接受互斥：**autopilot 运行期间人类侧的 master 会话不应动手；要接管就先停 autopilot。**

**关单前先验证（裁决）**：§2.6 要求「合并后验证再关单」（合了≠能用），而 autopilot 恰好在没有人盯着的时候运行，所以不给它写例外，而是让它自己验证：合并后在 `--verify-dir`（一个只给它用的干净克隆；有未提交改动它就拒绝，不替你清理）里 fetch、检出到该 PR 的合并提交，跑 `go build ./... && go test ./... && go vet ./...`，三条命令都清掉了 `YM_CONFIG_DIR` 与 `AGENT_ROLE`（测试会覆盖 agent 的凭据，上游）。通过才关单；不通过就不关、在该 PR 上贴 BLOCKED（带失败输出）、熔断 `verify:pr:N` 并结束本轮。**没有 `--verify-dir` 就不关单**，报告里写明，不存在跳过验证的开关。注意本仓库的 `verified` 事件只有 auditor 能发，autopilot 以 master 身份运行发不了，所以这里的 verified 记录是 PR 上的 `autopilot:verified` 留痕评论。

**熔断（主要安全机制，不是兜底）**：本仓库拿不到分支保护，所以熔断与事后侦测是眼下仅有的刹车。
- 单任务迭代上限（`--max-iterations`，默认 5）：一个任务反复尝试动作却完成不了；
- 连续失败上限（`--max-failures`，默认 3）；
- 单轮时长上限（`--round-timeout`，默认 5 分钟）：超时**只结束本轮**，不熔断、不贴 BLOCKED、不计迭代——恰好踩在线上的慢 gh 调用不该让一个无辜的 PR 永久停转；它和没轮到的那些一起留到下一轮。
失败计数是「连续」的：中间任何一次成功（包括检查正常、只是 PR 还没准备好）都会清零。任何一条触发，该任务立即停止，并在任务单/PR 上贴一条带 `autopilot:blocked` 标记的 **BLOCKED** 评论（只贴一次）。**它不会自行放宽任何判据，也不会自己重试**：熔断状态写在状态文件里，重启不丢；状态文件读不出来时拒绝启动，而不是当作空的。只有操作者用 `ym-autopilot --reset <key>` 才能解除。`--status` 列出当前熔断表。

**读不全时要说出来，不能静默**：读 master 收件箱时只取未处理的消息（不带 `state=all`），按创建顺序用 `after=` 游标翻页直到服务端说读完，所以历史消息再多也不会把新的 `done` 挤出视野；翻到页数上限（50 页）仍有剩余、或 `gh pr list` 的 open（100）/ merged（30）列表碰到上限，报告里都有 warning。**租约**：租约只有 60 秒而一轮可以几分钟，所以孤儿阶段开头先探一次、每次取消前再 renew。**没有有效租约（`stale_epoch` / `lease_required` / `lease_held`，或根本没导出）不是任何一条消息的失败**：不计失败、不计迭代、不熔断、不贴 BLOCKED，整个孤儿阶段本轮跳过，报告里写「没有有效的 master 租约，孤儿消息本轮未处理」。已过期的租约续不回来（`RenewMasterLease` 对过期租约一律 stale），所以**常驻模式必须放在 `ym lease exec` 里启动**，让租约一直被续着；在 shell 里 acquire 一次再启动，第二轮起必然 stale。**留痕标记只认评论的第一行**：评论的正文可能来自 worker（`ym post` 把消息落到 issue 上），标记出现在正文别处不算数，否则 worker 能预埋标记让循环以为 BLOCKED / 取消 / 关单的留痕已经写过。Ctrl-C / SIGTERM 造成的取消既不算失败也不会熔断。

**留痕**：开 PR 靠正文里的标记，其余动作各贴一条 `🤖 autopilot（自动执行，不是人工操作）` 评论，带 `<!-- autopilot:<动作> key=... -->`，便于回溯和去重。

**运行与密钥**：以 master 的 Yardmaster 身份跑（读 master 收件箱用 `peek`，不改变消息状态；取消孤儿消息需要导出的 `YM_LEASE_HOLDER`/`YM_LEASE_EPOCH`），并使用运行者的 `gh` 登录。**`GATE_TOKEN` 只放在这个进程的环境里**，不进 master 与 agent 的环境，减少持有者、集中审计；但这是部署约定，不是机器约束。

```bash
ym-autopilot --dry-run --once     # 先看它会做什么，什么都不改（连状态文件也不写）
ym lease exec -- ym-autopilot --once --verify-dir ~/yardmaster-verify   # 跑一轮；有熔断打开时退出码 3
ym lease exec -- ym-autopilot --verify-dir ~/yardmaster-verify         # 常驻：必须放在 ym lease exec 里，--interval 控制轮询间隔
ym-autopilot --status             # 熔断表
ym-autopilot --reset pr:123       # 操作者决定后解除一个熔断
```

**尚未做（另立）**：`ym-gate audit-merges`（自动扫「已合并但 PR 上没有 `ym-gate: merged #N at <sha>` 留痕」的 PR，视为绕过闸门的事故）。预防拿不到时侦测必须自动，这一项仍应单独做；本单没有做。

**blocked 的送达方式**：autopilot 以 master 身份运行，不能给自己发消息，所以 BLOCKED 以任务单/PR 上的评论 + 退出码 3 + 状态文件为准。如果要它直接推给某个 agent，需要另给它一个独立身份，这是身份与权限的决定，没有在本单替你做。

## 3. 超时与改派

默认值以 `config.example.toml` 为准；下表只列配置键，不复制数值。

| 情况 | 阈值（配置键） | 动作 |
|---|---|---|
| 未读 | `unread_warn` | 服务端发 `reminder` |
| 未确认 | `ack_dispatch` | 转 `overdue`；master 取消并改派 |
| worker 离线 | `online_grace` 内无心跳 | 积压改派给空闲 worker |
| 审计无回应 | `review_request` ack 截止 | 换 auditor |
| 阻塞项无进展 | 固定 30 分钟（流程阈值） | master 改派 blocker 或升级 owner，记 `DECISION:` |
| 修复 2 轮仍 `changes` | — | master 裁决或 `needs-user` |
| 合并冲突 | — | conflict-fix 任务，重新审计 |
| PR 已关闭/合并后仍推 | — | 从最新主线切新分支，新 PR |

改派用 `ym cancel <msg_id> --reason "改派"`，再给新 worker 发 `dispatch`，新消息 `parent_id` 指向旧的。

## 4. 硬规则

1. 没有拆解不派工；没有验收标准不派工。
2. 一个任务一个 worker 一个分支；每条 PR 走两段审计（standard → final），两段由不同 agent 独立完成。
3. master 不写代码、不复审；只拆解、派工、裁决、合并、关闭。
4. 复核必须独立子代理、独立上下文，不得与执行者同体；合并只认 head 上的两段票型（standard → final），不跳过 gate。
5. 关闭前必须 merged + verified（或记录豁免）。
6. 每一步有消息和 issue 评论；口头约定不算。GitHub 写权限：worker 不写；auditor 只贴自己的 REVIEW 票评论；其余评论都由 master 落（见 §2.4）。
7. 生产动作只在 owner 直接确认后执行。
8. 按 `P0 > P1 > P2` 推进；同级先做能解锁下游最多的任务。blocker 未开始或无人负责时先派 blocker；超过 30 分钟无进展就改派或升级 owner。

## 5. master 每轮检查清单

1. 优先级与阻塞链：按 `P0 > P1 > P2` 排序；检查每个 `status:blocked` 的 blocker，30 分钟无进展的改派或升级 owner。
2. `ym supervise --json`：离线、积压、`overdue`。
3. 收件箱：`done`、`blocked`、`ask`、`review`、`verified`。
4. 每个 `in-review`：PR 是否存在、head 是否变；当前 head 上是否已有 standard 的 `approve`、其后有 final 的 `approve`，且两段都没有 `changes`。
5. 每个 `changes`：修复是否派出、轮次是否超过 2。
6. 每个 merged：是否 `verified`、issue 是否关闭。
7. 需求 epic 的任务列表与子 issue 状态是否一致。
8. 看板是否还写着已经过期的状态。
9. `ym-gate audit-master`：票单 sha 与 head、重复票单、派工后队列、孤儿交付、合并留痕、镜像 ref、base 漂移、二进制/服务端陈旧、双 postman、已合并未删分支。它**只报事实**；unknown 不算 ok，发现异常由 master 判断处理。本机 agent 用 `--local-agents` 声明：未声明时 0 个 postman 只是 warn（`pgrep` 只看本机），声明后才报 problem。镜像范围目前是 image-building workflow runs（push/workflow_dispatch，成功 run），不审 ghcr tag/digest 清单。

## 6. 与 COORDINATION.md 的分工

| 文档 | 负责 |
|---|---|
| 本文 | 流程顺序、阶段门禁、超时与改派 |
| `COORDINATION.md` | 角色权限、关键字、标签、审计格式、密钥红线 |
| `ym help worker/auditor/master` | 每个角色的日常命令 |

冲突时：流程看本文，权限与格式看 `COORDINATION.md`。

## 7. 自举：用本流程开发本产品

Yardmaster 是边开发、边用自身流程实施的项目：产品开发本身就是这套工作流的第一个用户。

- 每个改动（包括流程文档）都走：issue → 拆解/派工 → 独立复核（standard → final） → gate/合并 → `verified`。
- 流程规则变更先在自身仓库跑一遍，再推广到其他项目；不靠会话口头约定。
- 修改流程文档同样要开 issue、派工、复核和合并，不能只改会话不回写仓库。
- §1.1 的优先级与阻塞链规则对 master 自身同样生效。
