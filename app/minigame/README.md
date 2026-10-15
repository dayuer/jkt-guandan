# 微信小游戏（二期，#83 / #85）

一期 H5 的约局功能全部搬进小游戏：原生 Canvas 画界面，自写一层 UI（热区、滚动、弹层、键盘），不依赖引擎。
接口和 H5 是同一套 JSON API，身份走 #84 的小游戏登录（`wx.login` → `POST /api/wx/login` → `Authorization: Bearer`）。

## 开发

```bash
cd app/minigame
npm ci
npm run dev        # 开发版，连 http://127.0.0.1:8080，改了自动重打
npm run build      # 正式版，连 https://guandan.synon.ai
npm run typecheck
npm test
```

打包产物是本目录的 `game.js`（不进仓库）。用微信开发者工具「导入项目」选**本目录**，AppID 在 `project.config.json`。

开发版要配一个本机服务端：`make run`（`GUANDAN_DEV=1`，没配 `GUANDAN_WX_APPID` 时接受 `dev:<openid>` 登录码，每台设备固定一个）。
开发者工具里要关掉「校验合法域名」（`project.private.config.json` 的 `urlCheck`），否则连不上 `http://127.0.0.1`。
换端口：`API_BASE=http://127.0.0.1:18674 npm run build:dev`。

**在真实运行时里自检**：`node build.mjs --dev --selftest` 打出的包启动后会自动切一遍 Tab，把报错、触摸命中的热区、每一步的画布截图发到 `127.0.0.1:18675`（自己起一个接收端，收 JSON 和 multipart 图片）。正式版不含这段代码。

正式版上线前，仓库所有者要在小游戏后台把 `guandan.synon.ai` 加进 request、socket（wss）、uploadFile、downloadFile 合法域名，并在用户隐私保护指引里声明相册（保存海报）。

## 目录

| 路径 | 内容 |
|---|---|
| `src/main.ts` | 入口：Tab 栏、页面跳转、从转发卡片进来（`query.g`） |
| `src/core/` | 画布运行时（场景栈、触摸、弹层、键盘）和常用控件 |
| `src/lib/` | 接口（`wx.request` + Bearer）、登录、实时（复用 H5 的 RealtimeClient）、埋点、时间格式 |
| `src/scenes/` | 各页：牌局、详情、开局、学牌、牌友、我的、设备、隐私、牌桌计分（`score.ts`，#165），以及共用弹层 |

复用 `app/h5/src` 的纯逻辑：`status.ts`（状态提示；没有本机快照时的换设备兜底在 `src/lib/notice.ts`，#160）、`conflict.ts`（K4/K5）、`share.ts`（话术）、`learn.ts`（课程）、`wa.ts`、`invite.ts`、`realtime.ts`。
H5 的 `format.ts` 依赖 `Intl` 时区，安卓微信不保证支持，打包时换成 `src/lib/fmt.ts`（雅加达固定 UTC+7）。

## 和 H5 的差异

- 文字输入只能用底部的微信原生输入条（`wx.showKeyboard`），画布上同步显示。
- 右上角是微信胶囊：标题区和导航栏都让开它；设计稿里右上角的按钮挪到左上或底部操作栏。
- 分享：「发到微信」用转发卡片（画布现画的 5:4 图，`query` 带牌局短码）；WhatsApp 只能复制话术后自己粘贴。
- 日历：基础库有 `wx.addPhoneCalendar` 就直接加，否则复制链接去浏览器里加。
- 提醒：小游戏收不到 Web Push，走微信订阅消息（#137，`src/lib/subscribe.ts`）。启动时从 `GET /api/config` 拿模板 ID；用户点「报名占座」的那次点按里**同步**调 `wx.requestSubscribeMessage`（没身份时改在称呼层「就用这个名字」那一下），同意了的经 `POST /api/wx/subscribe` 上报。同意了「开局提醒」，报名成功弹层显示「开局前 1 小时会提醒你」；没同意或服务端没配模板，仍给「加到日历」。开发版连本机服务时，服务端配上 `GUANDAN_WX_TMPL_FULL` / `GUANDAN_WX_TMPL_REMIND`（随便填）就会下发模板，发送只记日志。
- 不做：加到主屏幕、跳出微信的引导；claim 链接改成「我在网页上报过名」输入 6 位认领码（`POST /api/wx/link`）。
