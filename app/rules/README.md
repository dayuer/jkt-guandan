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
的 `packages/core`（MIT，不含它的 `ai/`），牌堆顺序、牌的写法、ID 的含义按 `testdata/rules`
的约定重写。许可原文见 [LICENSE](LICENSE)。

## 怎么跑

```bash
cd app/rules
npm ci
npm run typecheck
npm test
```

小游戏（`app/minigame`）用相对路径 import，不经过 npm 包解析：

```ts
import { deal, power } from "../../rules/src/index";
```

## 进度（#87 的第 1–8 步）

| 步骤 | 状态 |
|---|---|
| 1. 牌、发牌、牌点大小 | 本 PR |
| 2. 牌型识别和比较（不带逢人配） | |
| 3. 逢人配（枚举全部顶法） | |
| 4. 领出 / 跟牌的合法出法生成 | |
| 5. 出牌状态机（过牌、接风、出完顺序） | |
| 6. 结算、升级、过 A 配置 | |
| 7. 进贡、还贡、抗贡 | |
| 8. 随机对局性质测试 | |

第 2、3 步和服务端的 #216、#217 对齐（同一批牌例、同一套 `rule` 条款号写法）。
