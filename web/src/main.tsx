import { render } from "preact";
// 设计令牌来自 design/guandan-h5/tokens.css（数值以它为准）。
import "./tokens.css";
import "./styles.css";
import { App } from "./App";
import { registerServiceWorker } from "./push";

render(<App />, document.getElementById("app")!);

// Service Worker 只为了收推送（§5.3）：注册失败不影响页面，所以不 await、不拦渲染。
void registerServiceWorker();
