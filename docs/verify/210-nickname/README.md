# #210 改称呼：「我的」页截图

**在微信开发者工具模拟器里真跑了一遍**（真服务端、真渲染），和 #165 验收同一套路：
截的是小游戏自己导出的画布（`toTempFilePathSync` → 本机接收端 `sink.js`），不是无头替身。

- 走查脚本：`app/minigame/src/accept210.ts`（只编进 `--selftest` 包）
- 服务端：本机 `GUANDAN_DEV=1`，`API_BASE=http://127.0.0.1:18791`，接收端 18691
- 一轮 8 步全过：`tap-miss` / `tap-disabled` / `step-throw` / `shot-throw` 一条都没有

| 文件 | 看点 |
|---|---|
| `me-before.png` | 「我的」页：顶部卡片右侧多了一个「改」 |
| `me-sheet.png` | 点「改」→ 弹层（和「绑定 WhatsApp」同一套版式），输入框里是现在的称呼 |
| `me-typed.png` | 点输入框（真机拉起原生键盘）→ 打字 → 保存按钮亮起来 |
| `me-after.png` | 保存后回到「我的」页：顶部是新称呼「老张」，toast「改好了」 |

**打字那一步是真的走键盘回调**：`installSelftest` 把 `wx.onKeyboardInput` 的回调记在
`GameGlobal.__kb` 上，走查脚本像真机那样派发输入事件。第一版直接改内部变量，截图看着一样，
其实**什么都没发生**（没点过输入框时 `app.edit` 还没登记 `onChange`）——现在打字前会先报
一次 `TYPED {"editing":"nick"}`，`editing` 是空的就说明这一步白跑了。

服务端那边也核了：这一步之后 `GET /api/me` 回的 `user.nickname` 是「老张」
（走查日志里的 `SERVER-NICKNAME`），不是只改了本地缓存。

## 怎么跑

```sh
# 1. 服务端（开发模式）+ 接收端
GUANDAN_DEV=1 GUANDAN_DATA=/tmp/impl-210-run/data guandan seed --force
GUANDAN_DEV=1 GUANDAN_DATA=/tmp/impl-210-run/data GUANDAN_ADDR=127.0.0.1:18791 \
  GUANDAN_PUBLIC_URL=http://127.0.0.1:18791 GUANDAN_METRICS_ADDR= guandan &
SINK_OUT=/tmp/impl-210-run/out SINK_PORT=18691 node docs/verify/165-accept/sink.js &

# 2. 打包（改称呼场景）
API_BASE=http://127.0.0.1:18791 SELFTEST_SINK_PORT=18691 SELFTEST_SCENARIO=210 \
  node app/minigame/build.mjs --dev --selftest

# 3. 开发者工具打开 app/minigame，小游戏起来后会自己走完并把截图传回来
cli open --project <repo>/app/minigame
```

## 没覆盖的

- **真机**：键盘是原生输入条，模拟器里没有；「改」这一下会不会和原生键盘打架、12 个字的
  长名字在卡片上会不会换行，要真机看（这批图里名字都是短的）。
- 内容安全检测被拦（422）时的界面：开发模式下的检测是「含固定测试词就 risky」，
  这里没造那一轮；服务端侧有用例（`TestChangeNicknameChecksContent`）。
- 限频（429）的界面同理，服务端侧有用例。
