import { useEffect, useState } from "preact/hooks";
import { redeemClaimFromURL, type User } from "./identity";

type Health = "checking" | "ok" | "error";

type Claim =
  | { kind: "idle" }
  | { kind: "ok"; user: User }
  | { kind: "conflict"; other: string }
  | { kind: "dismissed" };

/**
 * 脚手架页面：确认页面能打开、能连上同一进程的 /healthz，
 * 并且在 URL 带着 claim token 时走一遍兑换（或者冲突确认）。
 * 真正的业务页面（02 首页、03 详情页……）在 #4 及之后实现。
 */
export function App() {
  const [health, setHealth] = useState<Health>("checking");
  const [claim, setClaim] = useState<Claim>({ kind: "idle" });

  useEffect(() => {
    fetch("/healthz")
      .then((r): Health => (r.ok ? "ok" : "error"))
      .catch((): Health => "error")
      .then((next) => setHealth(next));
  }, []);

  useEffect(() => {
    // 跳出微信 / WhatsApp 的链接会带 ?claim=…。
    // confirm 为 false 的这次请求在服务端是只读的：这台设备已经有别人身份时，
    // 它只会返回 409，不消耗 token、不改动任何东西。
    redeemClaimFromURL()
      .then((result) => {
        if (result.kind === "ok") setClaim({ kind: "ok", user: result.user });
        else if (result.kind === "conflict") setClaim({ kind: "conflict", other: result.otherNickname });
      })
      .catch(() => setClaim({ kind: "idle" }));
  }, []);

  const switchToOther = () => {
    redeemClaimFromURL(true)
      .then((result) => {
        if (result.kind === "ok") setClaim({ kind: "ok", user: result.user });
      })
      .catch(() => setClaim({ kind: "idle" }));
  };

  const label = { checking: "检查中…", ok: "已连上", error: "连不上" }[health];

  return (
    <main style={{ padding: "calc(48px + var(--safe-top)) 24px 24px" }}>
      <h1 style={{ fontFamily: "var(--serif)", color: "var(--red)", fontSize: 30 }}>随时掼掼蛋</h1>
      <p style={{ color: "var(--muted)", marginTop: 8 }}>雅加达 · 找人凑一桌，就差你一个</p>

      {claim.kind === "ok" && (
        <p style={{ color: "var(--green)", marginTop: 24 }} data-testid="claimed">
          ✓ 已认回身份：{claim.user.nickname}
        </p>
      )}

      {claim.kind === "conflict" && (
        <section
          data-testid="claim-confirm"
          style={{
            marginTop: 24,
            padding: 16,
            background: "var(--card)",
            border: "1px solid var(--line)",
            borderRadius: 12,
          }}
        >
          <p style={{ color: "var(--ink)" }}>
            这个链接属于 {claim.other}。要把这台设备切换成 {claim.other} 吗？
          </p>
          {/* 默认选项是保持当前身份：切换是不可逆的，得由用户明确选。 */}
          <button
            style={{ marginTop: 12, marginRight: 12, color: "var(--muted)" }}
            onClick={() => setClaim({ kind: "dismissed" })}
          >
            保持当前身份
          </button>
          <button
            style={{ marginTop: 12, color: "var(--red)" }}
            onClick={() => switchToOther()}
          >
            切换成 {claim.other}
          </button>
        </section>
      )}

      <p style={{ color: "var(--muted-2)", marginTop: 32, fontSize: 13 }} data-testid="health">
        服务：{label}
      </p>
    </main>
  );
}
