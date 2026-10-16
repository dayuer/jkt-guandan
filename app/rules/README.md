# 掼蛋规则引擎（客户端 TS 版，#87）

纯逻辑、不依赖界面。选牌时本地判断能不能出、有歧义时列出各种解读、高亮可出的牌都基于它。
**裁决以服务端为准**（#204，`server/internal/rules`）；这份只用来在客户端给提示、显示和断线恢复。

规则口径按 2022《竞技掼蛋竞赛规则（试行）》（国家体育总局棋牌运动管理中心审定，2022-09-08），
条款号写在每条规则旁边。

## 两端读同一批牌例

对照用例在仓库根 [`testdata/rules/`](../../testdata/rules/)，**格式见那里的 README.md ——
那是两端的接口说明**。这里的 `src/testing/fixtures.ts` 和服务端的 `server/internal/rules/fixtures_test.go`
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
| `src/moves.ts` | —（**没有移植**） | 上游 `packages/core/src/enumerator.ts` 是另一套枚举（先枚举连张窗口再填牌），这里的槽位式枚举和去重键是按服务端的 `server/internal/rules/{moves,moves_gen}.go` 写的 —— 两端要逐条比对，生成方式必须一样 |
| — | `packages/core/src/engine/`、`rules.ts`、`rng.ts` | 第 5 步起再看要不要移植 |

**被小游戏打包时要带上许可**：`app/minigame/build.mjs` 现在是 `minify: true` 加
`legalComments: "none"`，MIT 声明会被去掉。现在小游戏还没 import 本包（第 4 步接界面时才用得上）；
到那时要么把 `legalComments` 改成 `"eof"`，要么在被移植文件的头部加 `/*! ... */`，做法写进那一步的 PR。

## 移植清单（从服务端搬东西时先看这个）

`.go` → `.ts` 不是把实现逐行翻过来就完了，**测试要跟着一起搬，而且要搬两种**：

| 搬什么 | 为什么 |
|---|---|
| 实现 + 它读的牌例 | 两端读同一批 `testdata/rules/*.json` —— 这是"两端一致"的基础 |
| **随机 / 性质测试** | 一致性靠它压得最广：第 2+3 步是 2.5 万手随机牌、0 不一致 |
| **点名列举的边界用例**（Go 里的 `EdgeHands`、`TestRuleSyntax`、`TestMovesEdgeHandsMatchBruteForce` 这类） | 随机压不到「规则里需要**特定配置**才走到的分支」 |

**这条是踩出来的，不是抄来的。** 第 4 步我搬了随机对拍、漏了 Go 的 `EdgeHands`，于是
「跟牌时也要生成同花顺」那条路一直没被压到 —— 直到复审做变异测试（把实现里一行去掉）
才发现，**299 条测试全绿**。

同一类缺口一共出现过三次：

| 缺口 | 谁发现的 |
|---|---|
| 「A 当 1」的三个窗口没被覆盖 | 我做变异测试时发现 |
| 排序漏了张数（4 张和 5 张炸弹会交错） | #238 复审 |
| 跟牌时不生成同花顺 | auditor-240 |

三次的形状**是同一个**：通用防线（随机手牌、暴力对拍、共用牌例）都搬了，
**针对性的那一条没搬**。随机对拍对"算错"很灵，对"需要特定配置才走到的分支"是盲的。

所以：**搬 Go 测试时先问一句 —— 它除了随机对拍，还有没有点名列举的边界用例？**

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
| 2. 牌型识别和比较（不含逢人配） | 本 PR |
| 3. 逢人配（枚举全部顶法） | 本 PR |
| 4. 领出 / 跟牌的合法出法生成 | 本 PR |
| 5. 出牌状态机（过牌、接风、出完顺序） | |
| 6. 结算、升级、过 A 配置 | |
| 7. 进贡、还贡、抗贡 | |
| 8. 随机对局性质测试 | |

第 2、3 步和服务端的 #216、#217 对齐（同一批牌例、同一套 `rule` 条款号写法）。
