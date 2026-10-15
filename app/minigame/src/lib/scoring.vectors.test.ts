// 两端规则一致性（#174）：和 server/internal/scoring/vectors_test.go 读同一份 vectors.json，
// 本地预览用的 compute / previewEdit 必须和服务端 Compute 逐字段一样。改了规则这里会红。
import { describe, expect, it } from "vitest";
import data from "../../../../server/internal/scoring/testdata/vectors.json";
import { compute, previewEdit, type RoundFact, type ScoreSettings } from "./scoring";

interface Vector {
  name: string;
  settings: ScoreSettings;
  rounds: RoundFact[];
  edit?: RoundFact & { round_no: number };
  expect: { rounds: unknown[]; state: unknown };
}

const vectors = (data as { vectors: Vector[] }).vectors;
/** 和 Go 的 JSON 一样：没有的可选字段（omitempty）不出现 */
const plain = (x: unknown) => JSON.parse(JSON.stringify(x));

describe("规则对照用例（vectors.json）", () => {
  it("有用例", () => expect(vectors.length).toBeGreaterThan(10));
  for (const v of vectors) {
    it(v.name, () => {
      const facts = v.rounds.map((r) => ({ head_seat: r.head_seat, partner_place: r.partner_place }));
      if (v.edit) facts[v.edit.round_no - 1] = { head_seat: v.edit.head_seat, partner_place: v.edit.partner_place };
      expect(plain(compute(v.settings, facts))).toStrictEqual(v.expect);
      if (v.edit) {
        // S10 的「连带变化」走 previewEdit：结果要和整段重算一样
        const p = previewEdit(v.settings, v.rounds, v.edit.round_no, v.edit);
        expect(plain(p.round)).toStrictEqual(v.expect.rounds[v.edit.round_no - 1]);
        expect(plain(p.state)).toStrictEqual(v.expect.state);
      }
    });
  }
});
