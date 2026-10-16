/**
 * 几个**不碰时间**的文案函数（#253：从 `app/h5/src/format.ts` 里挑出来的纯部分）。
 *
 * `format.ts` 里其余的都要 `Intl`（时区），那部分留在各端 —— H5 在浏览器里能跑，小游戏在
 * 安卓微信上不一定有。这里只有纯字符串映射，两端一样。
 */

/** 「经典掼蛋」/「不升级」/「带新手」 */
export function kindLabel(kind: string): string {
  return { classic: "经典掼蛋", "no-level": "不升级", teaching: "带新手" }[kind] ?? kind;
}

/** 「差 2 人」/「已满」 */
export function missingLabel(missing: number): string {
  return missing > 0 ? `差 ${missing} 人` : "已满";
}
