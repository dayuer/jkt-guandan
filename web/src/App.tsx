import { useEffect, useState } from "preact/hooks";

type Health = "checking" | "ok" | "error";

/**
 * 脚手架页面：确认页面能打开、能连上同一进程的 /healthz。
 * 真正的业务页面（02 首页、03 详情页……）在 #4 及之后实现。
 */
export function App() {
  const [health, setHealth] = useState<Health>("checking");

  useEffect(() => {
    fetch("/healthz")
      .then((r): Health => (r.ok ? "ok" : "error"))
      .catch((): Health => "error")
      .then((next) => setHealth(next));
  }, []);

  const label = { checking: "检查中…", ok: "已连上", error: "连不上" }[health];

  return (
    <main style={{ padding: "calc(48px + var(--safe-top)) 24px 24px" }}>
      <h1 style={{ fontFamily: "var(--serif)", color: "var(--red)", fontSize: 30 }}>随时掼掼蛋</h1>
      <p style={{ color: "var(--muted)", marginTop: 8 }}>雅加达 · 找人凑一桌，就差你一个</p>
      <p style={{ color: "var(--muted-2)", marginTop: 32, fontSize: 13 }} data-testid="health">
        服务：{label}
      </p>
    </main>
  );
}
