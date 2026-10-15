#!/bin/sh
# 容器冒烟测试：按 docs/server-design.md §8 的部署参数跑镜像 —— 只读根文件系统、去掉全部
# capability；非开发模式必须告诉它本站地址（config.Load 会检查）。
# 原来是 .github/workflows/ci.yml 里的一步，本地 CI（#157）搬成脚本。
#
# 用法：scripts/container-smoke.sh <镜像> [构建号]
#   给了构建号就核 /healthz 的 X-App-Build 头（§13.4）。
#
# 同一台机器上可能有几个会话同时在跑：容器名、数据卷带进程号，宿主端口让 docker 随机分配，
# 互不撞车；退出时只删自己建的那两个。
set -eu
IMAGE=${1:?"用法：scripts/container-smoke.sh <镜像> [构建号]"}
BUILD=${2:-}
NAME="guandan-smoke-$$"
VOLUME="guandan-smoke-data-$$"

cleanup() {
  echo "--- 容器日志"
  docker logs "$NAME" 2>&1 | tail -40 || true
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker volume rm "$VOLUME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# 端口要先知道才能填 GUANDAN_PUBLIC_URL，所以不能交给 docker 随机分配：
# 让系统挑一个空闲端口（python3 是 macOS 与 CI 镜像都自带的）。
PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
URL="http://127.0.0.1:$PORT"

docker run -d --name "$NAME" --read-only --cap-drop ALL \
  --health-interval 2s --health-start-period 5s \
  -e GUANDAN_PUBLIC_URL="$URL" \
  -p "127.0.0.1:$PORT:8080" -v "$VOLUME:/data" "$IMAGE" >/dev/null

for _ in $(seq 1 30); do
  if curl -fsS "$URL/healthz" >/dev/null 2>&1; then break; fi
  sleep 1
done
echo "--- /healthz"
curl -fsS "$URL/healthz"
echo
echo "--- /readyz"
curl -fsS "$URL/readyz"
echo
echo "--- /"
curl -fsS "$URL/" | grep -q 'id="app"'
echo "内嵌页面已提供"
echo "--- 安全响应头"
curl -fsSI "$URL/" | grep -i 'content-security-policy'
if [ -n "$BUILD" ]; then
  echo "--- 构建号（§13.4）"
  curl -fsSI "$URL/healthz" | grep -i "x-app-build: $BUILD"
fi
echo "--- Docker HEALTHCHECK（guandan healthcheck 子命令）"
health=''
for _ in $(seq 1 30); do
  health=$(docker inspect -f '{{.State.Health.Status}}' "$NAME")
  if [ "$health" = healthy ]; then break; fi
  sleep 1
done
echo "health=$health"
test "$health" = healthy
echo "容器冒烟通过"
