#!/bin/sh
# 给浏览器端到端冒烟（make e2e）挑一个本次运行专用的端口（#228）。
#
#   scripts/e2e-port.sh             打印一个空闲端口；窗口里凑不出来就退出非 0
#   scripts/e2e-port.sh --window    只打印本次运行的窗口起点，不探端口（给自测用）
#
# 原来 e2e 固定用 18673：本机好几个会话同时跑 local-ci 时，后起的那一路 Playwright 会红在
# `http://127.0.0.1:18673/healthz is already used`。做法和 #181/#183 给 docker 那两项分端口
# 的一样（见 scripts/test-deploy.sh 头上那段说明），三条约束也一样：
#
#  1. 窗口起点按本次运行的编号推：[40000 + (编号 % 1100) * 8, +8)。不交给内核（bind 到 0），
#     内核会把刚释放的端口立刻再发出去，先后两次运行会拿到同一个号。
#  2. 只在自己的窗口里挑。并发起的两路编号（PID）是相邻的，挪出窗口就会落进对方的窗口。
#  3. 窗口里挑不出空闲端口就报错退出，不去抢别人的窗口。
#
# 端口段：40000–48799。test-deploy.sh 用 20000–29999，restore-drill.sh 用 30000–39999，
# 三段不重叠；上限停在 48799，避开 macOS 从 49152 起的临时端口（出站连接随手占的那一段）。
#
# 编号默认是本进程的 PID；E2E_RUN 只给自测用（固定编号好造出「相邻编号」的场景）。
# 探完立刻放手，交给 Playwright 拉起的服务端去绑 —— 中间那一下极短，和 docker 那两项一样。
set -eu

RUN=${E2E_RUN:-$$}
case "$RUN" in
  ''|*[!0-9]*) echo "e2e-port: 编号必须是非负整数：$RUN" >&2; exit 2 ;;
esac
# 显式给的编号不取模：超出范围直接拒，免得悄悄绕回别人的窗口（#186 同一类问题）。
if [ -n "${E2E_RUN:-}" ] && [ "$RUN" -ge 1100 ]; then
  echo "e2e-port: 编号 $RUN 超出范围（0–1099）" >&2
  exit 2
fi
START=$((40000 + (RUN % 1100) * 8))
WINDOW=8

if [ "${1:-}" = "--window" ]; then
  echo "$START"
  exit 0
fi

python3 - "$START" "$WINDOW" <<'PY'
import socket, sys
start, window = int(sys.argv[1]), int(sys.argv[2])
for p in range(start, start + window):
    s = socket.socket()
    try:
        s.bind(("127.0.0.1", p))
    except OSError:
        continue
    finally:
        s.close()
    print(p)
    sys.exit(0)
sys.exit("e2e-port: 本机 %d–%d 这一段端口都被占着（有别的进程？）；过一会儿再跑，"
         "或者手动指定 E2E_PORT" % (start, start + window - 1))
PY
