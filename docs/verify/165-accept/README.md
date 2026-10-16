# #165 小游戏牌桌计分：S1–S12 模拟器验收

**在微信开发者工具模拟器里真跑了一遍**（真服务端、真渲染、真实时推送），每一步截图。
不是无头替身：这些图是小游戏自己把**画布**导出来（`toTempFilePathSync`）传回本机接收端的。

- 工具：微信开发者工具（`appid wxc39979bdad718461`，`compileType: game`），机型 375×667
- 服务端：本机 `GUANDAN_DEV=1`（假 wx 登录、假内容检测），`API_BASE=http://127.0.0.1:18780`
- 走查脚本：`app/minigame/src/accept165.ts`（只编进 `--selftest` 包，见下面「怎么跑」）
- 一次跑完 32 步、33 张图，**没有一步落空**（脚本会把「热区没找到 / 被禁用 / 步骤抛异常」
  都报回来；这一轮这三种一条都没有）

## 为什么不用 `miniprogram-automator`

先按官方文档试了：`automator.launch({ projectPath })` **能**把小游戏拉起来，但

```
systemInfo / screenshot / callWxMethod / evaluate / currentPage / pageStack
→ 全部 "timeout waiting for automator response"
testAccounts → 正常返回（走的是 CLI，不经过游戏运行时）
```

包里也没有任何 game 相关代码（导出的全是 `Element` / `CustomElement` / `InputElement` 这些小程序的）。
结论：**它只支持小程序，驱动不了小游戏**。所以驱动只能放在游戏内部做。

## 怎么跑

```sh
# 1. 服务端（开发模式，假 wx 登录）
GUANDAN_DEV=1 GUANDAN_DATA=/tmp/impl-165-run/data guandan-165acc seed --force
GUANDAN_DEV=1 GUANDAN_DATA=/tmp/impl-165-run/data GUANDAN_ADDR=127.0.0.1:18780 \
  GUANDAN_PUBLIC_URL=http://127.0.0.1:18780 GUANDAN_METRICS_ADDR= guandan-165acc &

# 2. 接收端（日志 + 截图），换一个不和别人撞的端口
SINK_PORT=18676 node sink.js          # 见本 issue 评论里贴的 sink.js

# 3. 打包（走查场景 + 自定义接收端口）
API_BASE=http://127.0.0.1:18780 SELFTEST_SINK_PORT=18676 SELFTEST_SCENARIO=165 \
  node app/minigame/build.mjs --dev --selftest

# 4. 开发者工具打开这个项目，小游戏起来后会自己走完并把截图传回来
cli open --project <repo>/app/minigame
```

## 每一步对应哪个画面

| 文件 | 设计稿 | 看点 |
|---|---|---|
| `S1-detail-entry.png` | S1 | 详情页「人齐了，开打就记分」入口条 |
| `S2-start-sheet.png` | S2 | 开始记分弹层：两队成员 + 约定 + 过 A 说明 |
| `S3-score.png` | S3 | 记分页：级牌（打 2）、升级路线、底栏「记一局 · 第 1 局」 |
| `S4-record-1.png` | S4 | 记一局①：谁头游（红方一行、黑方一行，同一行就是搭档） |
| `S5-record-2-head.png` | S5 | 选了头游 → ② 头游的搭档第几 启用 |
| `S6-record-3-preview.png` | S6 | 选完搭档 → 预览「升 N 级 · 下一局打 X」 |
| `S6b-score-at-A.png` | S3/S6 | 红方打到 A：A 牌金边 +「本局打这级」 |
| `S6b-pass-A-preview.png` | **S6 的过 A 分支** | 「本局打红方的 A · 第 1 次」；② 换成「过 A / 过 A / 没过」；预览变**金色**「过 A！红方 赢下这一场」；主按钮变「记下 · 本场结束」 |
| `S7-recorded.png` | S7 | 记下第 1 局 → 回记分页，级数和路线都动了 |
| `S9-records.png` | S9 | 每局记录：局号圆、「打黑 7」签、谁记的、右侧级数变化 |
| `S10-edit.png` | S10 | 改一局：副标题写原来的结果 + 「连带变化」 |
| `S10-edited-recomputed.png` | S10 | 改完 → 记录页重算 |
| `S11-others-recorded.png` | S11 | 别人记了一局 → 顶部深色条（3 秒后自己收起） |
| `S12-conflict.png` | S12 | 两人同时记同一局（409）→ 并排给出，「用 TA 的」/「改成我的」 |
| `S12-take-theirs.png` | S12 | 点了「用 TA 的」之后 |
| `S8-match-over.png` | S8 | 本场结束：赢方 A 牌金边、「红方 过 A」、操作栏「分享战报 / 再来一场」 |
| `S15-report-card.png` | S15 | 战报卡片（`wx.shareAppMessage` 生成的 imageUrl，直接从钩子里截回来） |
| `S13-no-level-score.png` | S13 | 不升级玩法：两张牌换成**胜局数 1 : 0**，下面「双上」次数卡，没有级牌和升级路线 |
| `S13-no-level-record.png` | S13 | 不升级的记一局：只要第一步（选了头游主按钮就能点） |
| `S14-spectator.png` | S14 | 旁观视角：底栏只有「分享」+「只有座位上的四个人能记分」 |

S12 的「两个人记的**一样** → 静默合并 + toast「和 Lily 记的一样」」这一轮**没单独截**：
走查里造的那次冲突两边记的正好不同（要触发弹框），同结果那条分支没造。
代码路径是 `decideConflict` 的 `"same"` 分支（`lib/scoring.ts`），有单测覆盖，但没有界面截图。

## 视觉逐张对照（另一个 agent 独立做的）

对着设计稿第七节和参考图逐张核过：**S1、S2、S4–S7、S9、S10（三张）、S11、S12（三张）、S8、
S15 卡片全部通过**，没有一张是空页/过渡帧。几何也量过：S4 的虚线占位和 S6 实填预览的上下边界
完全重合（预览行高正好 60、主按钮不动，「选完不跳动」成立）。

对照里提出来、我核过之后**不认为是缺陷**的三条，记在这里免得下次又有人提：

1. **S15 卡片写「红方双上 5 次」**（设计稿参考图是不带队名的「双上 N 次」）—— 这是**故意的**：
   #165 正文里 #164 复审意见 b 明确要求「战报卡片的双上次数写明是哪一队，例如「红方双上 4 次」」。
   实现（`scenes/score.ts:849`）符合要求，是**设计稿那句参考文案过时了**。
2. **S8 和卡片上「用时 0:00 / 不到 1 分」** —— 不是 clamp 出错：`matchDuration` 是
   `Math.max(0, 最后一局的时间 - 起算点)`（`lib/scoring.ts:412`）。走查是**用 API 在一分钟内
   连写 9 局**、而且开局时间设在 20 分钟后，所以本来就是 0。正常比赛不会这样。
3. **S12 的冲突框是上下两行**（设计稿写「并排给出」）—— 要在一个 375 宽的框里并排两份
   「谁头游 + 搭档第几 + 级数变化」，上下两行读起来更清楚；按钮的左（soft）/右（primary）
   关系没变。判为可接受的实现选择，**不改**。如果复审要求严格照稿，说一声。

## 已知的两件事

1. **S2 的「打 A 几次不过回 2」马上要没了** —— #205 已经把这条约定整个去掉（仓库所有者决定
   「3 次回 2」也不留，没过 A 就一直打 A）。所以 `S2-start-sheet.png` 里的那两个 chip
   （`start-limit:3` / `start-limit:0`）是**改之前**的样子，改完要重跑这一张。
2. **真机没做**。真机那几项（翻牌流畅度 / 减少动态效果、主画布导出战报、四机同记、安卓 600 字重、
   弱网重复提交）要 `cli preview` 上传预览二维码，属于对外动作，**等仓库所有者同意再做**。

## 走查脚本踩过的六个坑

现象全都一样（「后面每个热区都 MISS」），原因各不相同，所以都在代码注释里写了现象：

1. 点完要自己 `app.redraw()`：真实触摸走 app 的 `onTouchEnd`（那里会重绘），直接调 `onTap()`
   绕过了那一步 —— 状态变了但画面还是上一帧。
2. 同一个 step 里连点两下不行：第二下看到的 `app.hits` 还是上一帧的，必然是禁用态。
3. 换身份要连内存里那份 token 一起清（`setToken("")`）：api.ts 的 token 是模块级变量。
4. 变量别遮蔽：诊断用的 `let code` 把这一局的 short_code 遮掉了。
5. 每轮新建 dev 账号会撞 IP 频率限制（`rate_limited`），改成固定账号 + 每轮退座
   （一个人同时最多占 5 个位置）。
6. `waitHit` 要等「能用」而不只是「存在」：弹层里没选头游时，搭档那三个选项**也在 hits 里**，
   只是 `onTap=null`。
