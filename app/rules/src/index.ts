// 掼蛋规则引擎（客户端 TS 版，#87）。纯逻辑、不依赖界面：选牌时本地判断能不能出、
// 有歧义时列出各种解读、高亮可出的牌都基于它。**裁决以服务端为准**（#204）。
//
// 规则口径按 2022《竞技掼蛋竞赛规则（试行）》（国家体育总局棋牌运动管理中心审定，2022-09-08）。
// 两端（这里和服务端 server/internal/rules）读同一批牌例：仓库根 testdata/rules/，
// 格式见那里的 README.md —— 那是两端的接口说明，改规则先改牌例。
//
// ⚠️ **不要 export `./testing/*`**。那里是读牌例的测试支持代码（还 import 了 testdata/rules
// 下的 JSON）；一旦从这里出去，小游戏打包时会把几十 KB 的牌例打进 game.js。
export * from "./card";
export * from "./power";
export * from "./deal";
export * from "./combo";
export * from "./moves";
export * from "./play";
export * from "./tribute";
// scoring 单独走**命名空间**：它导出的 `apply` / `compute` / `A` / `other` 都是很泛的名字，
// 平铺在 barrel 上迟早和别的模块撞，而且看 `import { A } from "…"` 完全不知道是哪个 A
// （#254 复审提的）。用法：`import { scoring } from "…"`，然后 `scoring.compute(...)`。
// ⚠️ 小游戏那边是从 `../rules/src/scoring` **直接 import 模块**的，不受这里影响。
export * as scoring from "./scoring";
