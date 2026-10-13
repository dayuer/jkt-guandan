# iOS 主屏幕 cookie 测试（#8）

验证一个假设：**iOS 在 Safari 里「添加到主屏幕」时，会把 Safari 的 cookie 复制给主屏幕 PWA**。结论决定 14a 屏走「自动认出」还是「输入认领码」（见 [docs/h5-pwa-plan.md](../../docs/h5-pwa-plan.md) §4 第 3 条）。

测试页是纯静态页面：没有 Service Worker、没有服务端、不发任何外部请求。

## 页面做了什么

| 环境 | 页面行为 |
|---|---|
| Safari 打开 | 生成一个 4 位**认领码**，连同时间戳写进 3 个 cookie（持久 Lax、会话 Lax、持久 Strict）和 localStorage。刷新不会换码；要换码点「重新生成认领码」 |
| 主屏幕 PWA 打开 | 顶部写明是否处于 standalone 模式，并给出结论：✅ 读到 Safari 的 cookie，并显示同一个认领码；或者 ❌ 没读到。下面的表格逐项列出能读到哪些数据（绿色：和 Safari 写入的一致；红色：读不到） |

「复制结果」按钮会把整页结果复制成文本，可以直接粘贴到 #8。

## 部署（一次性，二选一）

页面使用相对路径，部署在任意子路径下都能用。必须是 **HTTPS**。

**A. Netlify Drop（推荐，约 1 分钟，不改仓库设置）**
1. 把本目录 `tools/ios-cookie-test/` 下载到电脑（或 `git checkout audit/8-ios-cookie`）。
2. 浏览器打开 <https://app.netlify.com/drop>，把整个 `ios-cookie-test` 文件夹拖进去。
3. 得到一个 `https://<随机名>.netlify.app/` 地址，用 iPhone 打开它。

**B. GitHub Pages**
1. 仓库 Settings → Pages → Source 选「Deploy from a branch」，分支选 `audit/8-ios-cookie`，目录选 `/ (root)`。
2. 一两分钟后访问 `https://dayuer.github.io/jkt-guandan/tools/ios-cookie-test/`。
3. 测完把 Pages 关掉（Source 改回 None）。私有仓库需要付费套餐才能开 Pages，这种情况用 A。

## 真机操作步骤（每台约 2 分钟）

iOS 17 和 iOS 18 **各跑一遍**（有 iOS 26 的设备也请顺手跑一遍）。开始前确认：
- Safari 设置里**没有**打开「阻止所有 Cookie」，也不是无痕浏览。
- 这台手机上没有旧的「Cookie测试」图标；有的话先删掉。

1. **Safari 打开测试页。** 顶部显示蓝色「浏览器模式（Safari）」和一个 4 位认领码。**截图。**
2. **添加到主屏幕。** 点底部「分享」→「添加到主屏幕」→「添加」。iOS 26 及以后，确认「作为 Web App 打开」是打开的。
3. **从桌面图标打开。** 点新出现的「Cookie测试」图标。顶部应显示红色「主屏幕 PWA 模式（standalone）」，并且下面写着「这是本 PWA 第一次打开」。**截图**，再点「复制结果」，粘贴到 #8。
4. **可选：快照还是共享。** 回到 Safari 的测试页，点「写入「添加后」cookie」。然后在多任务界面上滑划掉 PWA，再从图标重新打开。看表格里「cookie「添加后」写入」一行：
   - 有值：PWA 和 Safari 一直共用 cookie；
   - 「（无）」：只在添加那一刻复制了一份。
5. **清理。** 删掉桌面图标；在 Safari 测试页点「清除本环境的测试数据」。

如果第 3 步顶部显示的是蓝色「浏览器模式」，说明打开的不是主屏幕 PWA，这次结果无效，请重做第 2–3 步。

能录屏更好：第 1–3 步录一段，直接上传到 #8。

## 结果记录表

把下表复制到 #8 填写。第 3 步的「复制结果」文本也一起贴上。

| 项 | iOS 17.__ | iOS 18.__ | iOS 26.__（可选） |
|---|---|---|---|
| 机型 | | | |
| 第 1 步 Safari 认领码 | | | |
| 第 3 步顶部是「PWA 模式」 | 是 / 否 | 是 / 否 | 是 / 否 |
| 第 3 步显示「第一次打开」 | 是 / 否 | 是 / 否 | 是 / 否 |
| 第 3 步认领码与第 1 步一致 | 是 / 否 | 是 / 否 | 是 / 否 |
| cookie 持久 Lax | 有 / 无 | 有 / 无 | 有 / 无 |
| cookie 会话（无过期） | 有 / 无 | 有 / 无 | 有 / 无 |
| cookie 持久 Strict | 有 / 无 | 有 / 无 | 有 / 无 |
| localStorage | 有 / 无 | 有 / 无 | 有 / 无 |
| 第 4 步「添加后」cookie（可选） | 有 / 无 / 没做 | 有 / 无 / 没做 | 有 / 无 / 没做 |

## 怎么判读

| 结果 | 14a 屏 |
|---|---|
| iOS 17 和 18 的「cookie 持久 Lax」都**有**，且认领码一致 | 走**自动认出**：PWA 首开读 Safari 写入的 `claim_token` cookie 自动认领；4 位认领码保留为兜底入口 |
| 任何一个版本**无** | 走**输入认领码**为主：Safari 引导页显示 4 位码，PWA 首开让用户输入；cookie 读得到时作为加速路径 |

补充判读：
- 「会话 cookie」无、「持久 cookie」有：服务端写 `claim_token` cookie 时必须带 `Max-Age`。
- 第 4 步「添加后」cookie 为无（快照）：`claim_token` 必须在用户点「添加到主屏幕」**之前**写好，13 屏一打开就写，不能等用户点按钮后再写。
- localStorage 预期是「无」（一般认为它不随添加复制）。无论结果如何，身份交接都不依赖它。

## 已知局限

- 静态页面只能测 JS 可读的 cookie。如果实现里的 `claim_token` 用 `HttpOnly` cookie，它和这里的 cookie 在同一个 cookie 存储里，预期行为相同，但本页没有直接验证。
- 只测首开那一刻。PWA 长期不用后存储会不会被清理，是另一个问题，不在 #8 范围内。
- GitHub Pages 方案下，`dayuer.github.io` 是所有仓库共用的域名；cookie 名都带 `gdtest_` 前缀，不会和别的页面冲突。
