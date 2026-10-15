# #189 局头转交的换设备兜底 验证截图

375×667，无头跑**真小游戏 bundle**（`app/minigame/build.mjs` 打出来的 `game.js`）+ **真服务端**（本机 `guandan seed`，`GUANDAN_ADDR=127.0.0.1:18776`）。
模拟 `wx` 的存储 / 网络 / 触摸，按热区点按，每步截图。脚本见同目录 `steps.js`，逐步断言见 `log.txt`（**24 条 `PASS`，`log.txt` 里可以 `grep -c '^PASS'` 数**）。

本机设备身份是「阿杰」（`MG_OPENID=ajie`）。「换设备」= 把这一台上的 `gd_game_seen:*` / `gd_game_fallback_seen:*` 清掉，身份 token 留着（微信缓存被清 / 换了手机）。

| 文件 | 说明 |
|---|---|
| `375x667-K2-before.png` | 转交之前：4/4 已成局，座位条写「局头 · 老王」，阿杰（我）只是个普通座位，底部也没有局头按钮 → **没有** K2 横幅。这条是对照组 |
| `375x667-K2-after-device-change.png` | 老王退出 → 服务端把这局转交给阿杰；**阿杰换了设备**再打开 → 顶部 K2「你现在是局头。空出的座位已开放报名。」，可以改时间 / 取消（局头操作也回来了） |
| `375x667-promoted-sheet.png` | 同一个人还补过位：换设备后先弹 #160 那条「有人退出，你补上了！」 |
| `375x667-K2-plus-rescheduled.png` | 关掉弹层后：K2 横幅 **和**「局头改过时间或地点，现在是 今天 11:44 · Pacific Place · 鼎泰丰包间」**两条一起出**（#185 复审意见 3）|

## 两条横幅的口径

- **K2（接手当上局头）**：`me.became_host_at` 有值、本机没有快照 → 出。**开局的人本人没有这一项**，所以自己开的局不会给自己弹。看不到上一位局头是谁，`formerHost` 留空，文案退成「你现在是局头。空出的座位已开放报名。」。
- **改时间**：接手的局头也要看 —— 上一位局头改的时间他也没看过。只有「那次改时间的时候我已经是局头」才不说（那才是我自己改的），判断是 `became_host_at <= rescheduled_at`。

## 一处渲染器差异（不是产品问题）

横幅左侧的 👑 / 🕘 在这组截图里是**方框**：`@napi-rs/canvas` 里没有装 `PingFang SC`（`core/ui.ts` 字体栈的第一位），退到 `Hiragino Sans GB`，它没有 emoji 字形、也不做回退。真机上 CoreText 会从字体栈回退到 `Apple Color Emoji`，同一份代码在 #160 的 🕘 上也是这样。文案本身完整、位置正常。

## 怎么跑

```sh
# 1. 起服务端（另一台机器上换成自己的二进制名，别按名字 kill 别人的进程）
GUANDAN_DEV=1 GUANDAN_DATA=/tmp/impl-189-run/data \
  /tmp/impl-189-run/guandan-189mg seed --force
GUANDAN_DEV=1 GUANDAN_DATA=/tmp/impl-189-run/data GUANDAN_ADDR=127.0.0.1:18776 \
  GUANDAN_PUBLIC_URL=http://127.0.0.1:18776 GUANDAN_METRICS_ADDR= /tmp/impl-189-run/guandan-189mg &
for u in "laowang 老王" "lily Lily" "ajie 阿杰" "xiaozhou 小周" "zhang 老张"; do set -- $u
  curl -s -XPOST http://127.0.0.1:18776/api/wx/login -H 'content-type: application/json' \
    -d "{\"code\":\"dev:$1\",\"nickname\":\"$2\"}" >/dev/null; done

# 2. 打包（API_BASE 指到本机服务端），跑 harness
API_BASE=http://127.0.0.1:18776 node app/minigame/build.mjs --dev
MG_OPENID=ajie node e2e.js app/minigame/game.js /tmp/out http://127.0.0.1:18776 steps.js
```

`e2e.js` 是 #160 起的那个无头小程序运行器（不在仓库里）。
