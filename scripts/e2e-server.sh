#!/bin/sh
# 端到端测试用的服务端（#72）：临时数据目录 + 示例数据 + 开发模式，只听本机。
# 由 web/playwright.config.ts 的 webServer 拉起，测试结束 Playwright 会把它停掉。
set -eu
cd "$(dirname "$0")/.."
PORT="${E2E_PORT:-18673}"
DATA="$(mktemp -d "${TMPDIR:-/tmp}/guandan-e2e.XXXXXX")"

# 服务端放后台、前台 wait，收到信号时先停服务再把临时目录删掉（#77 复审）。
# 这里不能用 exec：exec 之后这个 shell 就没了，trap 不会执行，$DATA 会一直留在 $TMPDIR 里。
server_pid=
cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$DATA"
}
trap cleanup EXIT INT TERM

export GUANDAN_DEV=1 GUANDAN_DATA="$DATA"
./bin/guandan seed --force
export GUANDAN_ADDR="127.0.0.1:$PORT" GUANDAN_PUBLIC_URL="http://127.0.0.1:$PORT" GUANDAN_METRICS_ADDR=
./bin/guandan &
server_pid=$!
wait "$server_pid"
