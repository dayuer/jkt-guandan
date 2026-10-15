# #160 状态提示的换设备兜底 验证截图

无界面端到端：`app/minigame` 用 `node build.mjs --dev` 打包（`API_BASE=http://127.0.0.1:18775`），
跑在模拟 `wx` 的 Node 里（@napi-rs/canvas 画布，做法同 [docs/verify/165](../165/README.md)、[docs/verify/174](../174/README.md)），
连本机 `GUANDAN_DEV=1` 的真服务端（从本分支构建）。本机设备身份是「阿杰」（`dev:ajie`），
老王（局头）、Lily、小周、老张用接口操作。画面 **375×667**（2 倍图），没有画状态栏和胶囊。

「换设备」的做法：把这一台上的 `gd_game_seen:*` / `gd_game_fallback_seen:*` 清掉，身份 token 留着 ——
等价于换了手机、或者微信把 webview 存储清了。

| 文件 | 画面 | 说明 |
|---|---|---|
| `01-S1-reschedule-fallback.png` | S1 / 改时间横幅 | 阿杰报名坐下、这台设备上打开过一次；**清掉本机快照**之后局头改时间，再拉一次详情 → 「局头改过时间或地点，现在是 今天 10:35 · Pacific Place · 鼎泰丰包间」。原来这条路是：没有快照就什么都不提示，还把新时间记成「已经看过」。 |
| `02-S1-promoted-fallback.png` | S1 / 补位弹层 | 阿杰先排候补，Lily 退出后补上；**清掉本机快照**再打开 → 「有人退出，你补上了！」+ 好的，到时见 / 加到日历 / 去不了了，让给下一位。 |

`harness-log.txt` 是那一次的完整断言输出。断言（15 条，全部 PASS）：

```
PASS A：刚报名、还没人改时间 → 没有横幅
PASS A：局头改时间成功（实际 200）
PASS A：服务端给了 me.joined_at
PASS A：rescheduled_at 晚于我加入的时间
PASS A：**无快照** + 改过时间 → 出改时间横幅
PASS A：关掉之后不再出现
PASS B：阿杰先在候补（实际 waitlist）
PASS B：还没补位，没有 promoted_at
PASS B：阿杰补上了座位（实际 seat）
PASS B：服务端给了 promoted_at
PASS B：joined_at 从排候补算，早于 promoted_at
PASS B：**无快照** + 补过位 → 出「轮到你了」弹层
PASS B：点「好的，到时见」之后弹层不再出现
PASS B：再进来不再出现
```

## 两点说明

- **横幅左边那个方框**是模拟器画布缺 emoji 字体（`🕘`），不是产品问题 —— 真机上正常。同样，
  顶部没有状态栏和胶囊是这套模拟的既定做法（见 165/174）。
- 场景 B 里「排候补」和「补位」必须**隔开一秒以上**：第一版脚本在同一秒里做完，
  `waitlisted_at` 和 `created_at` 相同，`joined_at < promoted_at` 那条断言误报了 FAIL。
  服务端本身是对的（`promoteHead` 把 `waitlist.created_at` 写进 `seats.waitlisted_at`）；
  脚本里加了 1.3 秒再让 Lily 退出，之后稳定通过。
