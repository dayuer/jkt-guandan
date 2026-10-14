import { render } from "preact";
// 设计令牌来自 design/guandan-h5/tokens.css（数值以它为准）。
import "./tokens.css";
import "./styles.css";
import { App } from "./App";

render(<App />, document.getElementById("app")!);
