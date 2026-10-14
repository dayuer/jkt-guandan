#!/bin/sh
# 端到端测试用的服务端（#72）：临时数据目录 + 示例数据 + 开发模式，只听本机。
# 由 web/playwright.config.ts 的 webServer 拉起，测试结束 Playwright 会把它停掉。
set -eu
cd "$(dirname "$0")/.."
PORT="${E2E_PORT:-18673}"
DATA="$(mktemp -d "${TMPDIR:-/tmp}/guandan-e2e.XXXXXX")"
export GUANDAN_DEV=1 GUANDAN_DATA="$DATA"
./bin/guandan seed --force
export GUANDAN_ADDR="127.0.0.1:$PORT" GUANDAN_PUBLIC_URL="http://127.0.0.1:$PORT" GUANDAN_METRICS_ADDR=

# 不能 exec：要留着 shell 才能在退出时删掉临时数据目录。Playwright 停服务时发 SIGTERM，转给服务端。
cleanup() {
  [ -n "${pid:-}" ] && kill "$pid" 2>/dev/null
  [ -n "${pid:-}" ] && wait "$pid" 2>/dev/null
  rm -rf "$DATA"
}
trap cleanup EXIT
trap 'exit 143' INT TERM
./bin/guandan &
pid=$!
wait "$pid"
