/**
 * 这一层（#253）住在 app/shared，所以**不 import 任何一端的 api / format**：
 *
 *   - 需要的字段在这里声明（`ConflictGame` / `GamePatchLike`），两端传整份 api 类型进来照样成立；
 *   - 需要渲染成文字的地方由调用方注入（`FieldLabels`）—— 时间的写法两端不一样，见 share.ts 的说明。
 */
export interface ConflictGame {
  game: { starts_at: number; venue: string; area: string; kind: string; note?: string };
}

/** 编辑表单能改的那几个字段（和编辑框一致）。 */
export interface GamePatchLike {
  starts_at?: number;
  venue?: string;
  area?: string;
  kind?: string;
  note?: string;
}

/** 把值渲染成给用户看的文字，由各端按自己的写法给。 */
export interface FieldLabels {
  /** 「今天 19:30」 */
  startsAt(unixSeconds: number): string;
  /** 「经典掼蛋」/「不升级」/「带新手」 */
  kind(kind: string): string;
}

/**
 * 局头改牌局时的版本冲突（§5.1、§11.3，原型 K4 / K5 / K6）。
 *
 * 409 的成因有两种，处理方式完全不同：
 *
 *   - **K5**（最常见）：编辑期间有人报名或退出。座位一变版本号就 +1，但局头改的是时间或
 *     地点，两边没有碰到同一个字段。这时不该打断用户——带着 409 返回的新版本号自动重提一次
 *     就好，然后提示「已保存 · 期间有人报名」。
 *   - **K4**：局头改的字段在服务端也被改过（比如别人也改了时间）。这时要摆出对比，
 *     让局头自己选用哪一边。
 *
 * 判断依据是「打开编辑框时看到的值」和「409 里的最新值」：只要补丁涉及的字段在这两者之间
 * 没有变过，就说明冲突与本次修改无关。
 */

/** 会出现在编辑表单里的字段。 */
const FIELD_LABELS: Record<string, string> = {
  starts_at: "时间",
  venue: "地点",
  area: "商圈",
  kind: "玩法",
  note: "备注",
};

function sameValue(field: string, a: ConflictGame, b: ConflictGame): boolean {
  switch (field) {
    case "starts_at":
      return a.game.starts_at === b.game.starts_at;
    case "venue":
      return a.game.venue === b.game.venue;
    case "area":
      return a.game.area === b.game.area;
    case "kind":
      return a.game.kind === b.game.kind;
    case "note":
      return (a.game.note ?? "") === (b.game.note ?? "");
    default:
      return true;
  }
}

function displayValue(field: string, d: ConflictGame, labels: FieldLabels): string {
  switch (field) {
    case "starts_at":
      return labels.startsAt(d.game.starts_at);
    case "venue":
      return d.game.venue;
    case "area":
      return d.game.area;
    case "kind":
      return labels.kind(d.game.kind);
    case "note":
      return d.game.note?.trim() || "（空）";
    default:
      return "";
  }
}

/** 补丁里涉及的字段名。 */
export function patchedFields(patch: GamePatchLike): string[] {
  return (["starts_at", "venue", "area", "kind", "note"] as const).filter(
    (f) => patch[f] !== undefined,
  );
}

/**
 * 补丁涉及的字段里，哪些在服务端也被别人改过。空数组表示没有冲突（走 K5）。
 */
export function conflictingFields(patch: GamePatchLike, baseline: ConflictGame, latest: ConflictGame): string[] {
  return patchedFields(patch).filter((field) => !sameValue(field, baseline, latest));
}

export interface FieldDiff {
  field: string;
  label: string;
  /** 局头这次想改成的值。 */
  mine: string;
  /** 服务端现在的值。 */
  theirs: string;
}

/**
 * K4 的对比表：把冲突字段的「我要改的」和「对方现在的」并排列出来。
 */
export function describeConflict(
  patch: GamePatchLike,
  baseline: ConflictGame,
  latest: ConflictGame,
  labels: FieldLabels,
): FieldDiff[] {
  return conflictingFields(patch, baseline, latest).map((field) => ({
    field,
    label: FIELD_LABELS[field] ?? field,
    mine: patchValueLabel(patch, field, labels),
    theirs: displayValue(field, latest, labels),
  }));
}

/** 把补丁里的值渲染成能给用户看的文字。 */
function patchValueLabel(patch: GamePatchLike, field: string, labels: FieldLabels): string {
  switch (field) {
    case "starts_at": {
      const unix = patch.starts_at;
      if (unix === undefined) return "";
      return labels.startsAt(unix);
    }
    case "venue":
      return patch.venue ?? "";
    case "area":
      return patch.area ?? "";
    case "kind":
      return labels.kind(patch.kind ?? "");
    case "note":
      return patch.note?.trim() || "（空）";
    default:
      return "";
  }
}
