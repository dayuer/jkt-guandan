# #211 「我的」页：我的牌局、历史删除、设置

**在微信开发者工具模拟器里真跑了一遍**（真服务端、真渲染），和 #165、#210 验收同一套：
截的是小游戏自己导出的画布（`toTempFilePathSync` → 本机接收端 `sink.js`），不是无头替身。

- 走查脚本：`app/minigame/src/accept211.ts`（只编进 `--selftest` 包，`SELFTEST_SCENARIO=211`）
- 数据：`seed.sh`。「阿杰」有三局要打的、三局历史。开局、报名、记分、取消都走真接口；
  只有「已经开始了」接口做不到，所以开完之后直接改库里的 `starts_at`，并像服务端每 10 分钟一次的
  清理任务那样把开局 6 小时以上的局收成 `done`。
- 一轮 12 步全过：`tap-miss` / `tap-disabled` / `step-throw` / `shot-throw` / `onError` 一条都没有

| 文件 | 看点 |
|---|---|
| `me-top.png` | 称呼卡片下面就是「我的牌局 · 要打的 · 3 局」：**我开的**（差 2 人 · 2/4）、**已报名**（已成局 · 4/4）、**候补第 1 位**（已成局）。每张：哪天几点、片区 · 玩法、地点、座位圆点 |
| `me-history.png` | 往下是「历史」：升级局打完「红 K : 黑 4 · 5 局」、我开的已取消、不升级局「红 1 : 黑 2 · 3 局」。每条右上角「删除」，标题旁写明「删除只是你自己看不到」 |
| `hide-sheet.png` | 点「删除」→ 确认弹层：说清删的是哪一局、只是自己看不到、牌局和同局牌友不受影响、删了不能恢复 |
| `hide-after.png` | 删掉之后 toast「删掉了」，历史少一条。走查日志里 `SERVER-HISTORY` 也核了服务端：只剩两条 |
| `settings.png` | 「设置」：在别的设备上登录、绑定 WhatsApp、我在网页上报过名、登录的设备（行为和原来一样，只是搬家） |
| `detail.png` | 点一张要打的 → 进那一局的详情 |
| `me-empty.png` | 没有局的人：「还没有要打的局。」+「去找局 ›」「开一局 ›」；下面是「设置」「隐私与数据」 |
| `me-guest.png` | 没有身份：「填个称呼」+「设置」「隐私与数据」 |

`settings-guest.png`：没有身份时进「设置」，只有灰掉的「登录的设备」。

## 怎么跑

```sh
# 1. 服务端（开发模式）+ 接收端，端口自己挑，别和别的会话撞
GUANDAN_DEV=1 GUANDAN_DATA=/tmp/impl-211-run/data guandan seed --force
GUANDAN_DEV=1 GUANDAN_DATA=/tmp/impl-211-run/data GUANDAN_ADDR=127.0.0.1:18811 \
  GUANDAN_PUBLIC_URL=http://127.0.0.1:18811 GUANDAN_METRICS_ADDR= guandan &
SINK_OUT=/tmp/impl-211-run/out SINK_PORT=18711 node docs/verify/165-accept/sink.js &

# 2. 造数据
API=http://127.0.0.1:18811 DB=/tmp/impl-211-run/data/guandan.db docs/verify/211-my-games/seed.sh

# 3. 打包并在开发者工具里打开，小游戏起来后会自己走完并把截图传回来
API_BASE=http://127.0.0.1:18811 SELFTEST_SINK_PORT=18711 SELFTEST_SCENARIO=211 \
  node app/minigame/build.mjs --dev --selftest
cli open --project <repo>/app/minigame
```

每跑一轮要从空库重来：走查会删掉一条历史。

## 没覆盖的

- **真机**：只在模拟器里跑过。
- 历史超过 20 条时的「看更早的 ›」：这批数据只有三条历史。翻页由服务端用例
  `TestMyGamesHistoryPaging` 钉住（同一开始时间的局也不重不漏）。
- 网络失败时的提示（「读不到我的牌局」「没删掉」）没有造。
