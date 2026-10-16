# app/shared —— 两端共用的那一层

H5（`app/h5`）和小游戏（`app/minigame`）**都** import 这里。它没有 `package.json`、没有构建步骤：
两端各自用相对路径 import（`../../shared/src/…`），打包时跟着各端编进各自的产物。

（#253 之前小游戏是直接 import `app/h5/src` 里的模块的，反向依赖 12 处；现在两边都从这里拿，小游戏
对 `app/h5` 的 import 是 0。）

## 三条规矩

### 1. 不碰 DOM / 浏览器 API；默认值留在各端的**壳**里

窗口地址、本地存储、时间写法这些「各端不一样」的东西，共享层**只声明自己要什么**（`FieldLabels`
的 `startsAt`、`KeyValueStore`、`shareURL(code, origin)` 的 `origin`），默认值由调用方给：

| 需要什么 | H5 的默认值在哪 | 小游戏的默认值在哪 |
|---|---|---|
| 本地存储 | `app/h5/src/{status,learn}.ts` 的 `window.localStorage` | `app/minigame/src/lib/{status,learn}.ts`（`shim.ts` 垫在 `window.localStorage` 上的 wx 存储） |
| 站点地址 | `app/h5/src/{share,invite}.ts` 的 `window.location.origin` | 调用点直接传 `API_BASE` |
| 时间怎么写 | `app/h5/src/format.ts`（`Intl` + Asia/Jakarta） | `app/minigame/src/lib/fmt.ts`（自己按 UTC+7 算，安卓微信不一定有 `Intl`） |

**这条有工具兜着**：`app/shared/tsconfig.json` 的 `lib` **不带 DOM**，`make shared-check` 会跑
`tsc -p app/shared --noEmit`（挂在 `make test` 里，`app/shared/*` 映射到 `local-ci/test`）。
共享层里冒出 `window` / `document` / `localStorage`，`TS2304: Cannot find name 'window'` 当场红。

**还有一条运行期的理由**（比编译期更硬）：小游戏的 vitest 跑在 **node** 环境里，没有 `window`；
共享层里任何一句**顶层求值**的浏览器 API，会让 import 它的测试 `ReferenceError: window is not defined`
—— 在 **import 那一刻**就炸，不是等到调用它才炸。实测过一次：在共享层写一句 `window.location.href`，
`local-ci/test` 和 `local-ci/minigame` 一起红（`src/scenes/me.ts → lib/api.ts → shared/wa.ts` 这条链）。

### 2. 文件之间只许 `import type`

共享层内部**不许**有值 import（`import { x } from "./y"`）和值 re-export（`export * from "./y"`）。
`import type` / `export type` 编译后擦掉，没有运行期耦合，随便用。

为什么拦这个：值是**运行期**的依赖。一端 import 了共享层的某个模块，整条值 import 链都会被拉进
它的产物；链上只要有个模块顶层碰了浏览器 API（见规矩 1），那端的测试/打包就会连带炸。

**工具**：`node scripts/check-shared-imports.mjs`（也在 `make shared-check` 里，同一条 `make test`）。
`tsc` 和 `isolatedModules` 都拦不住值 import，所以由这个脚本按语句扫：

- ✅ `import type { KeyValueStore } from "./store"`、`import { type A, type B } from "./store"`
- ❌ `import { KeyValueStore } from "./store"`、`import { type A, B } from "./store"`、`export * from "./store"`

### 3. 端的差异用「注入」表达，不在这里写 `if (是 H5)`

需要端上才知道的东西（时间怎么显示、链接前缀、往哪存），一律做成参数由调用方给。共享层不知道
自己跑在浏览器还是微信里 —— 这也是前面两条能成立的原因。

## 加一个共享模块的清单

1. 只放**与端无关**的东西：纯函数、类型、常量、数据（比如课本正文 `learn.ts`）。
2. 要读端上的东西？做成参数（`origin` / `when` / `store`），**别读全局**。
3. 在两端各留一个**薄壳**（形状：`export *` 转出 + 把默认值绑好再导出），这样调用点不用动；
   两端壳的路径和写法可参照 `app/h5/src/{status,learn,share,invite}.ts` 与
   `app/minigame/src/lib/{status,learn,share,conflict}.ts`。
4. 跑 `make shared-check`（类型 + 守门）和两端的测试；两端的用例要能因为**改共享层**而红
   （小游戏侧曾经出现过「改共享层文案只有 h5 红」的窟窿）。
5. `local-ci` 的范围：`app/shared/*` 已经映射到 `local-ci/test` + `local-ci/minigame` +
   `local-ci/e2e` + `local-ci/docker`（H5 镜像里要能编进去）。
