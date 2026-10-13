#!/bin/sh
# 场景 7 在子 shell 里换了 NAME、VOLUME，是故意只在那里生效的。
# shellcheck disable=SC2030,SC2031
# 在本机或 CI 上验证 scripts/deploy-remote.sh（服务器端的发布逻辑），不需要 nginx 和 ssh：
# 用一个临时目录当 /var/www/guandan，用容器自己的端口当 SITE_URL。
# 用法：scripts/test-deploy.sh <已经 build 好的镜像，如 guandan:ci>
# 在仓库根目录执行：场景 6 要用当前提交的源码另外构建一个带额外迁移的镜像。
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
  docker rm -f "$NAME-old" "$NAME-old-prev" >/dev/null 2>&1 || true
  docker volume rm "$VOLUME-old" >/dev/null 2>&1 || true
  for t in good0 good1 good2 good3 broken unusable migrated-base migrated prod; do docker rmi "$REPO:$t" >/dev/null 2>&1 || true; done
  rm -rf "$work"
}
trap cleanup EXIT
docker rm -f "$NAME" "$NAME-prev" >/dev/null 2>&1 || true
docker volume rm "$VOLUME" >/dev/null 2>&1 || true

# 三个好版本，加两个坏版本。
for t in good1 good2 good3; do docker tag "$GOOD" "$REPO:$t"; done
# broken：配置没问题，但监听地址非法，启动到最后一步才退出。
printf 'FROM %s\nENV GUANDAN_ADDR=:99999\n' "$GOOD" | docker build -q -t "$REPO:broken" - >/dev/null
# unusable：配置都过不了（备份地址不合法），连发布前冷备份都做不了。
printf 'FROM %s\nENV GUANDAN_BACKUP_URL=bogus://nowhere\n' "$GOOD" | docker build -q -t "$REPO:unusable" - >/dev/null

# 注意：给 deploy 临时加环境变量要用子 shell，「VAR=x deploy …」在 POSIX sh 里会留在当前 shell。
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
(KEEP=1; export KEEP; deploy good2) || exit 1
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

echo "== 3b. 新镜像连发布前冷备份都做不了：中止发布，旧容器原样恢复（#34 复审第 1 条）"
stage unusable
deploy unusable "发布前冷备份失败，中止发布"
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
(SITE_URL=http://127.0.0.1:9; export SITE_URL; deploy good3 "前端已切回 good2") || exit 1
expect "current" "$(current)" good2
expect "镜像" "$(image)" "$REPO:good2"
curl -fsS "http://127.0.0.1:$PORT/readyz"; echo

echo "== 6. 新版本先跑完一条兼容迁移、然后才失败：旧版本照常起来（coord 对 #34 的裁定）"
# 用当前提交的源码加一条兼容迁移构建新版本，再让它监听一个非法端口：迁移跑完后才退出。
src="$work/src"
mkdir -p "$src"
git archive HEAD | tar -x -C "$src"
printf -- '-- 测试用的兼容迁移\nCREATE TABLE deploytest_compat (x INTEGER);\n' > "$src/migrations/0999_deploytest_compat.sql"
docker build -q -t "$REPO:migrated-base" "$src" >/dev/null
printf 'FROM %s\nENV GUANDAN_ADDR=:99999\n' "$REPO:migrated-base" | docker build -q -t "$REPO:migrated" - >/dev/null
stage migrated
deploy migrated "已回滚，旧版本恢复服务"
expect "current" "$(current)" good2
expect "镜像" "$(image)" "$REPO:good2"
docker logs "$NAME" 2>&1 | grep -q "不认识的兼容迁移" || { echo "失败：旧版本日志里没有「不认识的兼容迁移」，新版本的迁移可能没跑"; exit 1; }
docker logs "$NAME" 2>&1 | grep "不认识的兼容迁移" | tail -1
curl -fsS "http://127.0.0.1:$PORT/readyz"; echo

echo "== 7. 从线上版本（#34 之前：遇到任何不认识的迁移都拒绝启动）升级，新版本迁移后失败（#34 复审第 1 条）"
# 线上跑的是 0e45b18（#35）。用它的源码构建「旧版本」，在一个新数据卷上先发布它。
PROD_REF=${PROD_REF:-0e45b187c0f10caba5bcce15dff095f06d8bcc7a}
git cat-file -e "$PROD_REF^{commit}" 2>/dev/null || git fetch -q --depth=1 origin "$PROD_REF"
prod="$work/prod"
mkdir -p "$prod"
git archive "$PROD_REF" | tar -x -C "$prod"
docker build -q -t "$REPO:prod" "$prod" >/dev/null
stage prod
(
  # 子 shell：换一套容器名、数据卷和端口，不影响上面的场景。
  # 注意 ( … ) || 里 set -e 不生效，每一步都要自己判失败。
  NAME="$NAME-old" VOLUME="$VOLUME-old" PORT=$((PORT + 1))
  PUBLIC_URL="http://127.0.0.1:$PORT" SITE_URL="http://127.0.0.1:$PORT"
  export NAME VOLUME PORT PUBLIC_URL SITE_URL
  # 0e45b18 的镜像没有 backup 子命令：默认拒绝发布；空数据卷上首次发布它要显式允许不备份。
  deploy prod "新旧镜像都没有 backup 子命令" || exit 1
  (ALLOW_NO_BACKUP=1; export ALLOW_NO_BACKUP; deploy prod) || exit 1
  # 新版本（带 0003 和一条额外迁移，迁移完才退出）：旧版本不认识这些迁移，回滚时起不来，
  # 必须用发布前的冷备份恢复。
  deploy migrated "已从发布前快照恢复，旧版本恢复服务" || exit 1
  expect "镜像" "$(docker container inspect -f '{{.Config.Image}}' "$NAME")" "$REPO:prod" || exit 1
  docker exec "$NAME" /guandan healthcheck >/dev/null || { echo "失败：恢复后的旧版本不健康"; exit 1; }
  curl -fsS "http://127.0.0.1:$PORT/readyz" || exit 1
  echo
) || exit 1

echo "== 通过：发布、清理旧版本、六种失败都符合预期"
