#!/bin/sh
# 在本机或 CI 上验证 scripts/deploy-remote.sh（服务器端的发布逻辑），不需要 nginx 和 ssh：
# 用一个临时目录当 /var/www/guandan，用容器自己的端口当 SITE_URL。
# 用法：scripts/test-deploy.sh <已经 build 好的镜像，如 guandan:ci>
set -eu
GOOD=${1:?"用法：scripts/test-deploy.sh <镜像>"}
PORT=${PORT:-28480}
REPO=guandan-deploytest

work=$(mktemp -d)
export NAME=guandan-deploytest VOLUME=guandan-deploytest-data IMAGE_REPO=$REPO PORT \
  WEB_ROOT="$work/www" ENV_FILE="$work/none.env" PUBLIC_URL="http://127.0.0.1:$PORT" \
  READY_TIMEOUT=30
cleanup() {
  docker rm -f "$NAME" "$NAME-prev" >/dev/null 2>&1 || true
  docker volume rm "$VOLUME" >/dev/null 2>&1 || true
  for t in good0 good1 good2 good3 broken; do docker rmi "$REPO:$t" >/dev/null 2>&1 || true; done
  rm -rf "$work"
}
trap cleanup EXIT
docker rm -f "$NAME" "$NAME-prev" >/dev/null 2>&1 || true
docker volume rm "$VOLUME" >/dev/null 2>&1 || true

# 三个好版本、一个起不来的版本（备份地址不合法，config.Load 直接退出）。
for t in good1 good2 good3; do docker tag "$GOOD" "$REPO:$t"; done
printf 'FROM %s\nENV GUANDAN_BACKUP_URL=bogus://nowhere\n' "$GOOD" | docker build -q -t "$REPO:broken" - >/dev/null

# 模拟 deploy.sh 上传静态文件：发布前才放上去。
stage() {
  mkdir -p "$WEB_ROOT/$1" && printf '<div id="app">%s</div>\n' "$1" > "$WEB_ROOT/$1/index.html"
  sleep 1 # 目录修改时间要有先后，KEEP 按它排序
}
# deploy <版本> [期望失败时输出里应有的话]
deploy() {
  if [ $# -eq 1 ]; then SHA=$1 sh scripts/deploy-remote.sh; return; fi
  if SHA=$1 sh scripts/deploy-remote.sh > "$work/out" 2>&1; then
    cat "$work/out"; echo "失败：发布 $1 应当失败"; exit 1
  fi
  cat "$work/out"
  grep -q "$2" "$work/out" || { echo "失败：输出里没有「$2」"; exit 1; }
}
current() { readlink "$WEB_ROOT/current"; }
image() { docker container inspect -f '{{.Config.Image}}' "$NAME"; }
expect() { [ "$2" = "$3" ] || { echo "失败：$1 是 $2，期望 $3"; exit 1; }; }

echo "== 1. 首次发布 good1"
stage good1
deploy good1
expect "current" "$(current)" good1
expect "镜像" "$(image)" "$REPO:good1"

echo "== 2. 升级到 good2（KEEP=1：当前版本和上一个版本照留，更早的被清掉）"
stage good0 # 比 good1 还早的一个版本，应被清掉
touch -t 202601010000 "$WEB_ROOT/good0"
stage good2
KEEP=1 deploy good2
expect "current" "$(current)" good2
expect "镜像" "$(image)" "$REPO:good2"
[ ! -e "$WEB_ROOT/good0" ] || { echo "失败：good0 的目录没清掉"; exit 1; }
[ -d "$WEB_ROOT/good1" ] || { echo "失败：上一个版本 good1 不该被清掉"; exit 1; }
docker container inspect "$NAME-prev" >/dev/null 2>&1 && { echo "失败：留下了 $NAME-prev"; exit 1; }

echo "== 3. 发布起不来的 broken：容器回滚，current 不动"
stage broken
deploy broken "已回滚，旧版本恢复服务"
expect "current" "$(current)" good2
expect "镜像" "$(image)" "$REPO:good2"
curl -fsS "http://127.0.0.1:$PORT/readyz"; echo

echo "== 4. current 是真目录：预检就停下，旧容器不受影响"
mv "$WEB_ROOT/current" "$work/current-link"
mkdir "$WEB_ROOT/current"
stage good3
deploy good3 "存在但不是符号链接"
expect "镜像" "$(image)" "$REPO:good2"
rmdir "$WEB_ROOT/current" && mv "$work/current-link" "$WEB_ROOT/current"

echo "== 5. 新版本就绪了，但切换后经「nginx」访问失败：容器和 current 一起回滚"
SITE_URL=http://127.0.0.1:9 deploy good3 "前端已切回 good2"
expect "current" "$(current)" good2
expect "镜像" "$(image)" "$REPO:good2"
curl -fsS "http://127.0.0.1:$PORT/readyz"; echo

echo "== 通过：发布、清理旧版本、三种失败的回滚都符合预期"
