import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { ApiError, getMe, signOutCurrentDevice, type UserRef } from "./api";
import { NicknameSheet } from "./components/NicknameSheet";
import { clearIdentityMarker, redeemClaimFromURL } from "./identity";
import { agentDisabled } from "./agent";
import { Agent, type FormPrefill } from "./pages/Agent";
import { CreateGame } from "./pages/CreateGame";
import { Detail } from "./pages/Detail";
import { Home } from "./pages/Home";
import { APP_NAME } from "./title";

type Route = { name: "home" } | { name: "detail"; code: string } | { name: "new" } | { name: "agent" };

function parseRoute(): Route {
  const path = window.location.pathname;
  const match = /^\/g\/([0-9A-Za-z]+)\/?$/.exec(path);
  if (match) return { name: "detail", code: match[1] };
  if (path === "/new") return { name: "new" };
  if (path === "/agent") return { name: "agent" };
  return { name: "home" };
}

type Claim =
  | { kind: "idle" }
  | { kind: "ok"; user: UserRef }
  | { kind: "conflict"; other: string };

/**
 * 应用外壳：路由、身份、认领链接，以及首次填称呼的弹层。
 *
 * 身份状态只有一份，放在这里：`/api/me` 问出来的是唯一事实，页面不各自缓存。
 */
export function App() {
  const [route, setRoute] = useState<Route>(parseRoute);
  const [me, setMe] = useState<UserRef | null>(null);
  const [ready, setReady] = useState(false);
  const [claim, setClaim] = useState<Claim>({ kind: "idle" });
  const [toastText, setToastText] = useState("");
  const [sheetOpen, setSheetOpen] = useState(false);
  // 07 蛋蛋退回 08 表单时带过去的草稿；从别处进表单就是空的。
  const [prefill, setPrefill] = useState<FormPrefill | null>(null);

  // 需要身份时弹层给出结果（resolve）。
  const pending = useRef<((user: UserRef | null) => void) | null>(null);

  const navigate = useCallback((path: string) => {
    window.history.pushState(null, "", path);
    setRoute(parseRoute());
  }, []);

  // 标题（#60）：从分享链接落地时服务端注入的是这一局的标题，SPA 内跳回别的页面要换回应用名。
  // 详情页的标题由 Detail 拿到数据后自己设。
  useEffect(() => {
    if (route.name !== "detail") document.title = APP_NAME;
  }, [route]);

  useEffect(() => {
    const onPop = () => setRoute(parseRoute());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const refreshMe = useCallback(async () => {
    try {
      const body = await getMe();
      setMe(body.user);
    } catch {
      // 401 就是还没有身份，不是错误。
      setMe(null);
    }
  }, []);

  useEffect(() => {
    (async () => {
      // 先处理跳出链接里的 claim，再问一次身份。
      try {
        const result = await redeemClaimFromURL();
        if (result.kind === "ok") setClaim({ kind: "ok", user: result.user });
        else if (result.kind === "conflict") setClaim({ kind: "conflict", other: result.otherNickname });
      } catch {
        // 兑换失败不阻塞页面。
      }
      await refreshMe();
      setReady(true);
    })();
  }, [refreshMe]);

  const requireIdentity = useCallback((): Promise<UserRef | null> => {
    if (me) return Promise.resolve(me);
    setSheetOpen(true);
    return new Promise((resolve) => {
      pending.current = resolve;
    });
  }, [me]);

  const toast = useCallback((text: string) => {
    setToastText(text);
    window.setTimeout(() => setToastText(""), 2600);
  }, []);

  const onSheetDone = (userID: string, nickname: string) => {
    const user = { id: userID, nickname };
    setSheetOpen(false);
    setMe(user);
    pending.current?.(user);
    pending.current = null;
  };

  const onSheetCancel = () => {
    setSheetOpen(false);
    pending.current?.(null);
    pending.current = null;
  };

  // 「不是我」：把当前这台设备退出，回到没有身份的状态（#36）。
  const notMe = async () => {
    try {
      await signOutCurrentDevice();
    } catch (err) {
      if (!(err instanceof ApiError)) toast("退出失败，请再试一次");
    }
    clearIdentityMarker();
    setMe(null);
    setClaim({ kind: "idle" });
    toast("已经退出，下次再填一次称呼就好");
  };

  const switchToOther = async () => {
    const result = await redeemClaimFromURL(true).catch(() => null);
    if (result?.kind === "ok") {
      setClaim({ kind: "ok", user: result.user });
      setMe(result.user);
      toast(`已切换成 ${result.user.nickname}`);
    }
  };

  return (
    <>
      {claim.kind === "ok" && (
        <div class="banner">
          <span>
            ✓ 已认回身份：{claim.user.nickname}
          </span>
          {/* 自动兑换是 login CSRF 的入口，给一个立刻退出的出口（#36）。 */}
          <button class="x" onClick={() => void notMe()}>
            不是我
          </button>
        </div>
      )}

      {claim.kind === "conflict" && (
        <div class="banner red">
          <span>
            这个链接属于 {claim.other}。要把这台设备切换成 {claim.other} 吗？
            {/* 切换不可逆：原身份会从这台设备上退出，之后只能靠认领找回（#36）。 */}
            <small style={{ display: "block", marginTop: 4, color: "var(--ink-3)" }}>
              切换之后，你现在这个身份会从这台设备上退出，以后只能通过认领链接或认领码找回。
            </small>
          </span>
          <button class="x" onClick={() => setClaim({ kind: "idle" })}>
            保持当前
          </button>
          <button class="link" onClick={() => void switchToOther()}>
            切换
          </button>
        </div>
      )}

      {!ready ? (
        <p class="empty-state">正在打开…</p>
      ) : route.name === "detail" ? (
        <Detail
          code={route.code}
          me={me}
          requireIdentity={requireIdentity}
          toast={toast}
          onBack={() => navigate("/")}
        />
      ) : route.name === "new" ? (
        <CreateGame
          me={me}
          requireIdentity={requireIdentity}
          toast={toast}
          onBack={() => navigate("/")}
          onCreated={(code) => navigate(`/g/${code}`)}
          prefill={prefill}
        />
      ) : route.name === "agent" ? (
        <Agent
          me={me}
          requireIdentity={requireIdentity}
          toast={toast}
          onBack={() => navigate("/")}
          onForm={(p) => {
            setPrefill(p);
            // 替换而不是压栈：从表单返回不该又回到一个已经失败的对话。
            window.history.replaceState(null, "", "/new");
            setRoute(parseRoute());
          }}
          onCreated={(code) => navigate(`/g/${code}`)}
        />
      ) : (
        <Home
          me={me}
          toast={toast}
          onOpen={(code) => navigate(`/g/${code}`)}
          onCreate={() => {
            setPrefill(null);
            navigate("/new");
          }}
          // 服务端没配蛋蛋（503 agent_disabled）时，「约局」直接打开表单。
          onAgent={() => {
            setPrefill(null);
            navigate(agentDisabled() ? "/new" : "/agent");
          }}
        />
      )}

      {sheetOpen && <NicknameSheet onDone={onSheetDone} onCancel={onSheetCancel} />}
      {toastText && <div class="toast">{toastText}</div>}
    </>
  );
}
