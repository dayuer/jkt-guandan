# 掼蛋规则引擎（客户端 TS 版，#87）

纯逻辑、不依赖界面。选牌时本地判断能不能出、有歧义时列出各种解读、高亮可出的牌都基于它。
**裁决以服务端为准**（#204，`server/internal/rules`）；这份只用来在客户端给提示、显示和断线恢复。

规则口径按 2022《竞技掼蛋竞赛规则（试行）》（国家体育总局棋牌运动管理中心审定，2022-09-08），
条款号写在每条规则旁边。

## 两端读同一批牌例

对照用例在仓库根 [`testdata/rules/`](../../testdata/rules/)，**格式见那里的 README.md ——
那是两端的接口说明**。这里的 `src/fixtures.ts` 和服务端的 `server/internal/rules/fixtures_test.go`
读同一批文件：

- 改规则先改牌例，两端一起跑；改字段名/加字段是**破坏性改动**（两端都拒绝未知字段）。
- 牌的 ID（0–107）由 `deck.json` 的 `order` 决定，两端构造出来的牌堆必须一模一样，
  否则同一串牌例在两边指的不是同一张牌。

和范围相同的 GitHub 实现对照过：规则要点参考了
[guandan-lab](https://github.com/Mereithhh/guandan-lab) 的 `RULES_SOURCES.md`。

## 许可

`src/` 下的算法移植自 [mimaima699-ux/Guandan-Poker-Platform](https://github.com/mimaima699-ux/Guandan-Poker-Platform)
的 `packages/core`（MIT，不含它的 `ai/`），盯的是上游 `03dfbb6`（2026-09-22）。许可原文见
[LICENSE](LICENSE)。

上游用 8 位整数编码一张牌（`card.ts`），发牌是 Fisher–Yates 加种子 RNG、按每 27 张一摞切开后排序
（`deck.ts`）；这里按 `testdata/rules` 的约定重写了牌的模型和发牌方向，所以逐文件对应是：

| 这里 | 上游 | 改动 |
|---|---|---|
| `src/card.ts` | `packages/core/src/card.ts`、`rank.ts` 的点数部分 | 牌改成 `{id,rank,suit,deck}` + `cardKey`/`parseKey`；牌堆顺序按 `testdata/rules/deck.json` 锁定 |
| `src/power.ts` | `packages/core/src/card.ts` 的 `orderKey`、`isWildCard` | 数值和 `orderKey` 不同（这里级牌 = 15、A = 14、原值兜底），只用来比大小 |
| `src/deal.ts` | `packages/core/src/deck.ts` | 随机源改成注入一个**排列**（`shuffle[i]` 是第 i 张发出的牌的 ID），不是种子 RNG；不排序、不按摞切 |
| `src/combo.ts` | —（**没有移植**） | 上游 `packages/core/src/recognizer.ts` 的连张窗口是「含级牌就整手非法」，和竞赛规则对不上：级牌在顺子类里按牌面原位置插带（第五条 1），A 可以当最小的 1（第四条 一、2）。所以这边是按服务端的 `server/internal/rules/combo.go` 写的 |
| — | `packages/core/src/enumerator.ts`、`engine/`、`rules.ts`、`rng.ts` | 第 4 步起再看要不要移植 |

**被小游戏打包时要带上许可**：`app/minigame/build.mjs` 现在是 `minify: true` 加
`legalComments: "none"`，MIT 声明会被去掉。现在小游戏还没 import 本包（第 4 步接界面时才用得上）；
到那时要么把 `legalComments` 改成 `"eof"`，要么在被移植文件的头部加 `/*! ... */`，做法写进那一步的 PR。

## 怎么跑

```bash
cd app/rules
npm ci
npm run typecheck   # 两个 tsc：全量 + 只查 src/ 的 ES2017 那一遍
npm test
```

`tsconfig.src.json` 是给**要被小游戏打包的那部分**（`src/` 里的非测试文件）单独设的一道闸：
`lib: ES2017`。esbuild 的 `target: es2017` 只降语法、不给运行时 API 打补丁，所以 `.flat()`、
`Object.fromEntries` 这类 ES2018+ 的 API 能让类型检查先拦下来。

小游戏（`app/minigame`）用相对路径 import，不经过 npm 包解析：

```ts
import { deal, power } from "../../rules/src/index";
```

⚠️ `src/testing/` 是读牌例的测试支持代码，**不要从 `index.ts` 导出** —— 一旦导出，小游戏打包
会把几十 KB 的牌例 JSON 一起打进 `game.js`。

## 进度（#87 的第 1–8 步）

| 步骤 | 状态 |
|---|---|
| 1. 牌、发牌、牌点大小 | 已合入（#229） |
| 2. 牌型识别和比较（不带逢人配） | 本 PR |
| 3. 逢人配（枚举全部顶法） | |
| 4. 领出 / 跟牌的合法出法生成 | |
| 5. 出牌状态机（过牌、接风、出完顺序） | |
| 6. 结算、升级、过 A 配置 | |
| 7. 进贡、还贡、抗贡 | |
| 8. 随机对局性质测试 | |

第 2、3 步和服务端的 #216、#217 对齐（同一批牌例、同一套 `rule` 条款号写法）。
