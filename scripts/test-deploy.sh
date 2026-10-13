#!/bin/sh
# 在本机或 CI 上验证 scripts/deploy.sh：先发布一个好镜像，再发布一个起不来的镜像，确认自动回滚。
# 用法：scripts/test-deploy.sh <已经 build 好的镜像，如 guandan:ci>
set -eu
GOOD=${1:?"用法：scripts/test-deploy.sh <镜像>"}
REPO=${GOOD%:*}
PORT=${PORT:-28480}
export NAME=guandan-deploytest VOLUME=guandan-deploytest-data PULL=0 IMAGE_REPO="$REPO" \
  PORTS="-p 127.0.0.1:$PORT:8080" READY_TIMEOUT=30

work=$(mktemp -d)
cleanup() {
  docker rm -f "$NAME" "$NAME-prev" >/dev/null 2>&1 || true
  docker volume rm "$VOLUME" >/dev/null 2>&1 || true
  docker rmi "$REPO:deploytest-broken" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
cleanup_before() { docker rm -f "$NAME" "$NAME-prev" >/dev/null 2>&1 || true; docker volume rm "$VOLUME" >/dev/null 2>&1 || true; }
cleanup_before

export ENV_FILE="$work/env"
printf 'GUANDAN_PUBLIC_URL=http://127.0.0.1:%s\n' "$PORT" > "$ENV_FILE"

# 起不来的镜像：备份地址不合法，config.Load 会直接退出。
printf 'FROM %s\nENV GUANDAN_BACKUP_URL=bogus://nowhere\n' "$GOOD" | docker build -q -t "$REPO:deploytest-broken" - >/dev/null

echo "== 1. 首次发布 ${GOOD}"
scripts/deploy.sh "${GOOD#*:}"
curl -fsS "http://127.0.0.1:$PORT/readyz"; echo

echo "== 2. 再次发布同一个镜像（有旧版本，走停旧启新）"
scripts/deploy.sh "${GOOD#*:}"

echo "== 3. 发布起不来的镜像，期望自动回滚"
if scripts/deploy.sh deploytest-broken; then
  echo "失败：坏镜像居然发布成功了"; exit 1
fi
image=$(docker container inspect -f '{{.Config.Image}}' "$NAME")
[ "$image" = "$GOOD" ] || { echo "失败：回滚后的镜像是 $image"; exit 1; }
curl -fsS "http://127.0.0.1:$PORT/readyz"; echo
docker container inspect "$NAME-prev" >/dev/null 2>&1 && { echo "失败：留下了 $NAME-prev"; exit 1; }
echo "== 通过：坏版本已回滚，$GOOD 在服务"
