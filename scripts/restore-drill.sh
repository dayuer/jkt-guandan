#!/bin/sh
# $common 故意按空格拆成多个参数；快照名只有字母数字，ls 足够。
# shellcheck disable=SC2086,SC2012
# 恢复演练（docs/runbook.md「恢复演练」，§13.3：上线前至少演练一次）。
# 在本机用 Docker 模拟两台机器：
#   旧机器：起服务 → 写入示例数据 → 加密备份到「异地」（一个本机目录，file://）
#   新机器：全新的空数据卷 → guandan restore latest → 起服务 → 数据都在
# 用法：scripts/restore-drill.sh <镜像，如 guandan:local>
set -eu
IMAGE=${1:?"用法：scripts/restore-drill.sh <镜像>"}
PORT_OLD=${PORT_OLD:-28481}
PORT_NEW=${PORT_NEW:-28482}
PORT_METRICS=${PORT_METRICS:-28483}

work=$(mktemp -d)
cleanup() {
  docker rm -f drill-old drill-new >/dev/null 2>&1 || true
  docker volume rm drill-old-data drill-new-data >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
cleanup_before() { docker rm -f drill-old drill-new >/dev/null 2>&1 || true; docker volume rm drill-old-data drill-new-data >/dev/null 2>&1 || true; }
cleanup_before

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
run docker run -d --name drill-old $common -v drill-old-data:/data -p 127.0.0.1:$PORT_OLD:8080 "$IMAGE"
for _ in $(seq 1 30); do docker exec drill-old /guandan healthcheck --ready >/dev/null 2>&1 && break; sleep 1; done
run docker exec drill-old /guandan healthcheck --ready
run docker exec drill-old /guandan seed --force
run docker exec drill-old /guandan admin stats --since 1d
run docker exec drill-old /guandan backup now
run docker exec drill-old /guandan backup list
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
run docker rm -f drill-old

echo
echo "== 3. 新机器：全新的空数据卷，从异地恢复最新一份"
step "docker run --rm ... -v drill-new-data:/data -v key.txt:/key.txt:ro $IMAGE restore -i /key.txt latest"
docker run --rm $common -v drill-new-data:/data -v "$work/key.txt:/key.txt:ro" "$IMAGE" restore -i /key.txt latest

echo
echo "== 4. 新机器：启动服务，确认数据都在"
run docker run -d --name drill-new $common -e GUANDAN_METRICS_ADDR=:9090 -v drill-new-data:/data \
  -p 127.0.0.1:$PORT_NEW:8080 -p 127.0.0.1:$PORT_METRICS:9090 "$IMAGE"
for _ in $(seq 1 30); do docker exec drill-new /guandan healthcheck --ready >/dev/null 2>&1 && break; sleep 1; done
run curl -fsS "http://127.0.0.1:$PORT_NEW/readyz"
echo
run docker exec drill-new /guandan admin stats --since 1d
step "curl -sI http://127.0.0.1:$PORT_NEW/healthz | grep -i x-app-build"
curl -fsSI "http://127.0.0.1:$PORT_NEW/healthz" | grep -i x-app-build
step "curl -s http://127.0.0.1:$PORT_METRICS/metrics | grep -E 'build_info|backup_|outbox_pending'"
curl -fsS "http://127.0.0.1:$PORT_METRICS/metrics" | grep -E '^guandan_(build_info|backup_|outbox_pending)'

echo
echo "== 5. 恢复时服务还在跑，必须被拒绝"
step "docker run --rm ... -v drill-new-data:/data $IMAGE restore -i /key.txt latest   # drill-new 正在用这个卷"
if docker run --rm $common -v drill-new-data:/data -v "$work/key.txt:/key.txt:ro" "$IMAGE" restore -i /key.txt latest; then
  echo "失败：服务运行时居然允许恢复"; exit 1
fi

echo
echo "== 演练通过"
