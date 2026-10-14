# 微信小游戏（二期，#83 选型原型）

原生小游戏 API：Canvas 画界面，自写一层 UI（热区、滚动、弹层、toast），不依赖引擎。数据是本地假数据（`js/data.js`），不连服务端。

用微信开发者工具「导入项目」选**本目录**（不是仓库根目录），AppID 已写在 `project.config.json`。

## 三屏

| 文件 | 对应设计稿 | 验证什么 |
|---|---|---|
| `js/scenes/home.js` | A2 列表 | 纵向惯性滚动 + 回弹、商圈胶囊横滑、日期筛选、右上角胶囊避让 |
| `js/scenes/detail.js` | B1 / B3 / W1 详情 | 牌桌和落座回弹动画；报名在 touchend 里**同步**调 `wx.requestSubscribeMessage`（#85）；称呼弹层随键盘上移；`wx.shareAppMessage` 带 `query=g=<短码>` |
| `js/scenes/create.js` | X1 开局 | 文字输入走 `wx.showKeyboard`（屏幕底部原生输入条），敏感词即时提示；按住说话 / 上滑取消（`wx.getRecorderManager`，只录不传，#122） |

## 已知差异（相对 H5）

- 小游戏没有行内输入框，点字段后在底部原生输入条里打字，画布上同步显示。
- 右上角固定是微信胶囊：详情页的分享图标挪到底部操作栏，开局页的「取消」挪到左上。
- 字体用系统字体；衬线标题在没有宋体的安卓机上会退回无衬线。
