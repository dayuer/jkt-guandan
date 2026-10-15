#!/bin/sh
# $common 故意按空格拆成多个参数；快照名只有字母数字，ls 足够。
# shellcheck disable=SC2086,SC2012
# 恢复演练（docs/runbook.md「恢复演练」，§13.3：上线前至少演练一次）。
# 在本机用 Docker 模拟两台机器：
#   旧机器：起服务 → 写入示例数据 → 加密备份到「异地」（一个本机目录，file://）
#   新机器：全新的空数据卷 → guandan restore latest → 起服务 → 数据都在
# 用法：scripts/restore-drill.sh <镜像，如 guandan:local>
#       scripts/restore-drill.sh --print-names   只打印本次运行用的名字和端口（给自测/排障看）
set -eu
# 等容器/HTTP 就绪的小工具（#186）：超时会明确失败并打印日志，不会静默往下走。
. "$(dirname "$0")/docker-wait.sh"

# 每次运行一套自己的名字和端口（#183，和 #181 是同一类问题）。本机经常同时跑好几路
# local-ci/docker（impl 自测、auditor 复审、coord 的 pre-merge-check），原来写死的
# drill-old / drill-new / drill-*-data 和 28481–28483 会让两路互相删改。
#
# **端口按「这个脚本要几个」步进**：这里要 3 个（旧机器、新机器、新机器的 /metrics），
# 所以起点按 3 步进，每次运行独占一段连着的端口。只按 1 步进的话，两路的编号一相邻，
# 对方的第一个口就会落在这一路的区间里 —— #181 复审里实测撞过这个（`port is already
# allocated`），别再犯。
#
# DEPLOYTEST_RUN 只给自测用：固定「本次运行的编号」，好造出两次运行编号相邻的场景。
RUN=${DEPLOYTEST_RUN:-$$}
OLD="drill-old-$RUN"
NEW="drill-new-$RUN"
OLD_VOL="drill-old-$RUN-data"
NEW_VOL="drill-new-$RUN-data"

# 每次运行独占一个 8 个口宽的**窗口**，窗口起点按编号推：[30000 + (RUN % 1250) * 8, +8)。
# 窗口里挑第一组连着的 3 个空闲端口。三条约束，缺一不可：
#
#  1. 起点按编号推，不能交给内核（bind 到 0）—— 内核会把刚释放的端口立刻再发出去，
#     先后两次运行会拿到同一个号。
#  2. 挪的时候**只在窗口里挪，步长就是这一组的宽度**。允许挪出窗口的话，挪一步正好落进
#     下一个编号的窗口，而并发起的两路 PID 就是相邻的 —— 还是要撞（#181 复审实测过这个
#     形状：占住起点那个口时自测就红了）。窗口比这一组宽，挪完还是自己的地盘。
#  3. 一组里的每个口都要先探过是空的：后面两个要过一会儿才用，中间被别的进程抢走就晚了。
#
# 窗口里凑不出来就**报错退出**，不去抢别人的窗口。（test-deploy.sh 用 20000–29999，
# 两个脚本的窗口不会撞。）
WINDOW=8
port_group() { # $1=窗口起点 $2=要几个；在窗口内打印一组，找不到就退出非 0
  python3 - "$1" "$2" "$WINDOW" <<'PY'
import socket, sys
start, k, window = int(sys.argv[1]), int(sys.argv[2]), int(sys.argv[3])
for p in range(start, start + window - k + 1):
    held = []
    for q in range(p, p + k):
        s = socket.socket()
        try:
            s.bind(("127.0.0.1", q))
        except OSError:
            break
        held.append(s)
    for s in held:
        s.close()          # 立刻交还给 docker；窗口极小
    if len(held) == k:
        print(" ".join(str(p + i) for i in range(k)))
        sys.exit(0)
sys.exit("本机 %d–%d 这一段端口凑不出 %d 个连着的空闲端口（有别的进程占着？）；"
         "过一会儿或换台机器再跑" % (start, start + window - 1, k))
PY
}

if [ -n "${PORT_OLD:-}" ]; then
  # 显式指定时保持原来的行为（调试、或外部脚本要固定端口）。并发跑 CI 时**不要**设。
  printf '注意：环境里给了 PORT_OLD=%s，本次运行就用它（并发跑 CI 时不要设，见 #183）\n' "${PORT_OLD}"
  PORT_NEW=${PORT_NEW:-$((PORT_OLD + 1))}
  PORT_METRICS=${PORT_METRICS:-$((PORT_OLD + 2))}
else
  GROUP=$(port_group $((30000 + (RUN % 1250) * 8)) 3)
  PORT_OLD=${GROUP%% *}
  rest=${GROUP#* }
  PORT_NEW=${rest%% *}
  PORT_METRICS=${rest#* }
fi

work=$(mktemp -d)
cleanup() {
  docker rm -f "$OLD" "$NEW" >/dev/null 2>&1 || true
  docker volume rm "$OLD_VOL" "$NEW_VOL" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
cleanup_before() { docker rm -f "$OLD" "$NEW" >/dev/null 2>&1 || true; docker volume rm "$OLD_VOL" "$NEW_VOL" >/dev/null 2>&1 || true; }
cleanup_before

# 打印本次的名字和端口就退出 —— cleanup_before 已经跑过，退出时 trap 里的 cleanup 也会
# 照常执行：配合 test-drill-names.sh 的假 docker，正好能看出「这一次运行碰了哪些资源」。
if [ "${1:-}" = '--print-names' ]; then
  printf 'RUN=%s\nOLD=%s\nNEW=%s\nOLD_VOL=%s\nNEW_VOL=%s\nPORTS=%s %s %s\n' \
    "$RUN" "$OLD" "$NEW" "$OLD_VOL" "$NEW_VOL" "$PORT_OLD" "$PORT_NEW" "$PORT_METRICS"
  exit 0
fi

IMAGE=${1:?"用法：scripts/restore-drill.sh <镜像> | --print-names"}

# 并发隔离自测（#183）：毫秒级，放在前面 —— 名字或端口一旦退回去写死，先在这里就红，
# 不用等后面几分钟的演练跑完。
"$(dirname "$0")"/test-drill-names.sh || { echo "失败：并发隔离自测没过"; exit 1; }

step() { printf '\n$ %s\n' "$*"; }
run() { step "$*"; "$@"; }

# 「异地」存储：一个本机目录，容器里的 nonroot 用户（uid 65532）要能写。
mkdir -p "$work/remote" && chmod 0777 "$work/remote"

echo "== 0. 生成备份密钥（公钥放服务器，私钥离线保存）"
step "docker run --rm $IMAGE backup keygen"
docker run --rm "$IMAGE" backup keygen > "$work/keygen.txt"
sed 's/AGE-SECRET-KEY-.*/AGE-SECRET-KEY-…（私钥，已省略）/' "$work/keygen.txt"
grep '^AGE-SECRET-KEY-' "$work/keygen.txt" > "$work/key.txt"
chmod 0644 "$work/key.txt" # 演练用；生产上私钥不放服务器，恢复时临时挂进去
RECIPIENT=$(sed -n 's/^# \(age1.*\)$/\1/p' "$work/keygen.txt")

cat > "$work/env" <<ENV
GUANDAN_PUBLIC_URL=http://127.0.0.1:$PORT_OLD
GUANDAN_BACKUP_URL=file:///backup-remote
GUANDAN_BACKUP_KEY=$RECIPIENT
ENV

common="--read-only --cap-drop ALL --env-file $work/env -v $work/remote:/backup-remote"

echo
echo "== 1. 旧机器：启动服务，写入示例数据，做一次加密备份"
run docker run -d --name "$OLD" $common -v "$OLD_VOL:/data" -p 127.0.0.1:$PORT_OLD:8080 "$IMAGE"
wait_container_ready "$OLD" 30 || { echo "失败：$OLD 等了 30 秒还没就绪（日志见上）"; exit 1; }
run docker exec "$OLD" /guandan healthcheck --ready
run docker exec "$OLD" /guandan seed --force
run docker exec "$OLD" /guandan admin stats --since 1d
run docker exec "$OLD" /guandan backup now
run docker exec "$OLD" /guandan backup list
step "ls -l <异地目录>"
ls -l "$work/remote" | sed 1d
step "head -c 64 <最新的异地对象>   # age 密文头，后面全是密文"
latest=$(ls "$work/remote" | tail -1)
# Linux 上对象属于容器里的 nonroot 用户（0600），宿主机用户可能读不了，读不了就跳过。
if head -c 64 "$work/remote/$latest" > "$work/peek" 2>/dev/null; then
  od -c "$work/peek" | head -4
else
  echo "（对象属于容器用户，宿主机读不了，跳过）"
fi

echo
echo "== 2. 旧机器报废"
run docker rm -f "$OLD"

echo
echo "== 3. 新机器：全新的空数据卷，从异地恢复最新一份"
step "docker run --rm ... -v $NEW_VOL:/data -v key.txt:/key.txt:ro $IMAGE restore -i /key.txt latest"
docker run --rm $common -v "$NEW_VOL:/data" -v "$work/key.txt:/key.txt:ro" "$IMAGE" restore -i /key.txt latest

echo
echo "== 4. 新机器：启动服务，确认数据都在"
run docker run -d --name "$NEW" $common -e GUANDAN_METRICS_ADDR=:9090 -v "$NEW_VOL:/data" \
  -p 127.0.0.1:$PORT_NEW:8080 -p 127.0.0.1:$PORT_METRICS:9090 "$IMAGE"
wait_container_ready "$NEW" 30 || { echo "失败：$NEW 等了 30 秒还没就绪（日志见上）"; exit 1; }
# host 侧也带重试（#186）：原来只 curl 一次，容器刚起来时会报一个看不出原因的 connect failed。
run wait_http_ok "http://127.0.0.1:$PORT_NEW/readyz" 30 || { echo "失败：host 侧连不上 $PORT_NEW 的 /readyz"; exit 1; }
echo
run docker exec "$NEW" /guandan admin stats --since 1d
step "curl -sI http://127.0.0.1:$PORT_NEW/healthz | grep -i x-app-build"
curl -fsSI "http://127.0.0.1:$PORT_NEW/healthz" | grep -i x-app-build
step "curl -s http://127.0.0.1:$PORT_METRICS/metrics | grep -E 'build_info|backup_|outbox_pending'"
curl -fsS "http://127.0.0.1:$PORT_METRICS/metrics" | grep -E '^guandan_(build_info|backup_|outbox_pending)'

echo
echo "== 5. 恢复时服务还在跑，必须被拒绝"
step "docker run --rm ... -v $NEW_VOL:/data $IMAGE restore -i /key.txt latest   # $NEW 正在用这个卷"
if docker run --rm $common -v "$NEW_VOL:/data" -v "$work/key.txt:/key.txt:ro" "$IMAGE" restore -i /key.txt latest; then
  echo "失败：服务运行时居然允许恢复"; exit 1
fi

echo
echo "== 演练通过"
