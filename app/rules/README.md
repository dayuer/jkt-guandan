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
| `src/play.ts` | —（**没有移植**） | 上游没有出牌状态机；按服务端的 `server/internal/rules/play.go` 写（第八条、第九条），两端共用 `testdata/rules/play.json` |
| `src/tribute.ts` | —（**没有移植**） | 上游没有进贡；按服务端的 `server/internal/rules/tribute.go` 写（第十条），两端共用 `testdata/rules/tribute.json` |
| `src/scoring.ts` | —（**没有移植**，是**从本仓库搬过来的**） | 升级 / 过 A / 本场结束。原来住在 `app/minigame/src/lib/scoring.ts`（和记分弹层的界面/会话混在一起），第 6 步把规则那一半搬到这里；口径按服务端的 `server/internal/scoring`，两端由**共用用例** `testdata/scoring/vectors.json` 锁住 |
| — | `packages/core/src/engine/`、`rules.ts`、`rng.ts` | **没有移植**：出牌、进贡、计分按服务端和本仓库写（上面三行），随机源用 `src/testing/random.ts` 自己的 mulberry32。上游那几处是它自己的引擎外壳，规则口径和竞赛规则对不上（见 `combo.ts` 那行） |

**被小游戏打包时要带上许可**：`app/minigame/build.mjs` 现在是 `minify: true` 加
`legalComments: "none"`，MIT 声明会被去掉。小游戏**从第 6 步起已经 import 本包了**
（`src/scoring.ts` —— 那是自家代码，没有许可问题）；但等它 import 到**移植自上游**的
`card` / `power` / `deal` / `combo` / `moves` 时，MIT 声明就会被打包器去掉。到那时要么把
`legalComments` 改成 `"eof"`，要么在被移植文件的头部加 `/*! ... */`，做法写进那一步的 PR。

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
| `Give` / `Return` 的 `holds`（「手里没有这张牌」）两支都没被守到 —— **上游自己那条用例名不副实** | 我做第 6 步的变异时发现（写在 #249 上） |

三次的形状**是同一个**：通用防线（随机手牌、暴力对拍、共用牌例）都搬了，
**针对性的那一条没搬**。随机对拍对"算错"很灵，对"需要特定配置才走到的分支"是盲的。

**性质测试不能拿实现的输出当期望值。** 第 8 步的随机对局（`src/sim.property.test.ts`）里，
「该轮到谁、这一圈结束没有、这一副结束没有、下一副打几」都是**测试自己按条文推的第二份实现**
（`SimModel` / `refNext`），再和引擎逐步、逐副对；不是「跑一遍引擎、把它的输出当期望」。
区别是实的：把 `scoring` 的升级规则改坏（搭档二游 +3 改成 +2），**独立推的那一份立刻红**；
拿引擎自己的输出当期望的话，这种改法一点声音都没有。Go 那边是同一套路
（`sim_test.go` 的 `simModel` —— auditor-246 的反证就是「把那几处改坏，原来那套 sim 全绿」）。

第 6 步又多出一种形状，比上面三次更阴：

- **上游那条用例名字写的是 A、实际测的是 B。** `tribute_test.go` 里一条叫「**手里没有的牌**
  不能还」，可它传的 `♠A/0` 在上一行刚被贡进收贡方手里 —— 所以走的是 `canReturn` 那一路，
  `Return` 里的 `holds` 一次都没碰到（把它删掉，`go test -run TestTribute` **全绿**）。
  所以搬用例**不能只看名字**：要么核它真的走得到那条分支，要么直接做变异。
- **「拒绝的结果没变、只是理由变了」这一类坏法，只有封闭的原因码守得住。** 拿同一个变异看：
  删 `Return` 的 `holds`，共用牌例会红（那一步本该被拒却被接受）；删 `Give` 的 `holds`，
  **共用牌例也是绿的** —— 因为那一步仍然会被拒，只是原因从 `card-not-in-hand` 变成了
  `not-biggest`。牌例只比「被拒了没有」的话，两种坏法都看不见。

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
| 2. 牌型识别和比较（不含逢人配） | 已合入（#237） |
| 3. 逢人配（枚举全部顶法） | 已合入（#237） |
| 4. 领出 / 跟牌的合法出法生成 | 已合入（#240） |
| 5. 出牌状态机（过牌、接风、出完顺序） | 已合入（#247） |
| 6. 结算、升级、过 A 配置 | 已合入（#254：规则从 minigame 搬进 `src/scoring.ts`） |
| 7. 进贡、还贡、抗贡 | 已合入（#252） |
| 8. 随机对局性质测试 | 本 PR（`src/sim.property.test.ts`：完整对局 + 独立推的第二份实现） |

第 2、3 步和服务端的 #216、#217 对齐（同一批牌例、同一套 `rule` 条款号写法）。

**这两处说明是更正过的，写清楚免得再绕一遍：**

1. **这张表的编号按 #87 正文那份清单**（调研评论第三节）：6 是结算、7 是进贡、8 是随机对局。
   **服务端 #204 的编号不一样** —— 它那边 6 就是进贡（把结算折进了第 6 步的 `Next`、复用 `scoring`）。
   两张表编号不同，所以「第 6 步」在两边指的不是同一件事，说的时候要带上是哪个 issue。
   （之前有一版把这行改成「6 = 进贡」、说那样才和 issue 一致 —— **说反了**，这里改回来。）
2. **结算 / 升级 / 过 A 是在客户端做的**，就是第 6 步。这里原先写过「不在客户端做，服务端算好
   下发」—— **那句是错的**：#87 的范围里明写了升级和过 A 的规则、目标里写了「完整判定……胜负」。
   把「服务端主持练习局」（#87 评论第 3 条）错读成了「客户端不用算结算」。规则现在住在
   `src/scoring.ts`，两端由 `testdata/scoring/vectors.json` 那份共用用例锁住。
   （同一句错话在 `src/tribute.ts` 的注释里也有一份，一并改了。）
