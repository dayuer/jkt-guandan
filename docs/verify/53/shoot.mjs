// 用 CDP 精确设定视口再截图（headless Chrome 的 --window-size 对布局视口不可靠）。
import { writeFileSync } from "node:fs";

const CDP = "http://127.0.0.1:9333";
const shots = JSON.parse(process.argv[2]);

async function newTarget(url) {
  const r = await fetch(`${CDP}/json/new?${encodeURIComponent(url)}`, { method: "PUT" });
  return r.json();
}
async function closeTarget(id) {
  await fetch(`${CDP}/json/close/${id}`);
}

function rpc(ws, method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = Math.floor(Math.random() * 1e9);
    const onMsg = (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.id !== id) return;
      ws.removeEventListener("message", onMsg);
      msg.error ? reject(new Error(method + ": " + msg.error.message)) : resolve(msg.result);
    };
    ws.addEventListener("message", onMsg);
    ws.send(JSON.stringify({ id, method, params }));
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

for (const shot of shots) {
  const target = await newTarget("about:blank");
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((res, rej) => {
    ws.addEventListener("open", res);
    ws.addEventListener("error", rej);
  });
  await rpc(ws, "Page.enable");
  await rpc(ws, "Emulation.setDeviceMetricsOverride", {
    width: shot.width,
    height: shot.height,
    deviceScaleFactor: 1,
    mobile: true,
  });
  await rpc(ws, "Page.navigate", { url: shot.url });
  await sleep(shot.wait ?? 1200);
  if (shot.click) {
    await rpc(ws, "Runtime.evaluate", { expression: `document.querySelector(${JSON.stringify(shot.click)})?.click()` });
    await sleep(900);
  }
  if (shot.scrollToBottom) {
    await rpc(ws, "Runtime.evaluate", {
      expression: "window.scrollTo(0, document.documentElement.scrollHeight)",
    });
    await sleep(400);
  }
  const { data } = await rpc(ws, "Page.captureScreenshot", { format: "png" });
  writeFileSync(shot.file, Buffer.from(data, "base64"));
  const info = await rpc(ws, "Runtime.evaluate", {
    expression: `JSON.stringify({
      docH: document.documentElement.scrollHeight,
      winH: window.innerHeight,
      scrollY: Math.round(window.scrollY),
      barTop: Math.round(document.querySelector('.tabbar,.actionbar')?.getBoundingClientRect().top ?? -1),
      barBottom: Math.round(document.querySelector('.tabbar,.actionbar')?.getBoundingClientRect().bottom ?? -1),
      lastCard: (() => { const c=[...document.querySelectorAll('.gamecard,.info,.disclaimer')].pop(); return c ? Math.round(c.getBoundingClientRect().bottom) : -1; })()
    })`,
  });
  console.log(shot.file, info.result.value);
  ws.close();
  await closeTarget(target.id);
}
