#!/bin/sh
# 发布的服务器端部分（docs/runbook.md §2）。由 scripts/deploy.sh 通过 ssh 执行，
# 也可以在服务器上直接跑：SHA=<短 SHA> sh scripts/deploy-remote.sh
#
# 前提（deploy.sh 已经做好）：镜像 guandan:<SHA> 已 docker load，静态文件已放在 $WEB_ROOT/<SHA>/。
# 步骤：
#   0. 预检：镜像在、静态文件在、$WEB_ROOT/current 要么不存在、要么是符号链接（是目录就停）
#   1. 旧容器里做一次发布前备份
#   2. 停旧容器（优雅关闭），改名为 guandan-prev
#   3. 启动新容器，等 /readyz，最多 READY_TIMEOUT 秒
#   4. 原子切换 current 符号链接（rename(2)；nginx 站点关了 open_file_cache，不用 reload）
#   5. 切换后经 nginx 再确认一次（SITE_URL）
#   任何一步失败：删新容器、旧容器原样启动回来、current 指回原来的版本，退出码 1。
#   6. 成功：删 guandan-prev，只保留最近 KEEP 个版本的静态目录和镜像（服务器磁盘紧张）。
#
# 只动这几样东西：容器 $NAME / $NAME-prev、镜像 guandan:<SHA>、$WEB_ROOT 下面的目录。
# 不碰 nginx 配置和别的服务。
set -eu

: "${SHA:?需要 SHA（镜像 tag 和静态目录名）}"
NAME=${NAME:-guandan}
IMAGE_REPO=${IMAGE_REPO:-guandan}
PORT=${PORT:-18200}                     # 宿主机的 8080、8443 被别的服务占着，只能用这个
WEB_ROOT=${WEB_ROOT:-/var/www/guandan}
VOLUME=${VOLUME:-guandan-data}
ENV_FILE=${ENV_FILE:-/etc/guandan/env}
# 容器看到的对端是 docker 网桥网关（coord 在服务器上确认过），只信任它传来的 X-Real-IP（#35）。
TRUSTED_PROXIES=${TRUSTED_PROXIES-172.17.0.1/32}
PUBLIC_URL=${PUBLIC_URL:-https://guandan.synon.ai}
SITE_URL=${SITE_URL:-$PUBLIC_URL}       # 切换后经 nginx 检查的地址；设为空跳过
MEMORY=${MEMORY:-256m}
READY_TIMEOUT=${READY_TIMEOUT:-60}
KEEP=${KEEP:-5}

IMAGE="$IMAGE_REPO:$SHA"
PREV="$NAME-prev"
CURRENT="$WEB_ROOT/current"

log() { printf '[deploy] %s\n' "$*"; }
die() { log "$*"; exit 1; }
exists() { docker container inspect "$1" >/dev/null 2>&1; }
running() { [ "$(docker container inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = true ]; }

start() {
  img=$1
  # 备份地址、密钥等写在 $ENV_FILE 里（runbook §1.1）；没有这个文件也能起，只是不做异地备份。
  if [ -r "$ENV_FILE" ]; then set -- --env-file "$ENV_FILE"; else set --; fi
  docker run -d --name "$NAME" --restart unless-stopped \
    --read-only --cap-drop ALL --security-opt no-new-privileges \
    --memory "$MEMORY" --stop-timeout 30 \
    --log-opt max-size=10m --log-opt max-file=5 \
    -p "127.0.0.1:$PORT:8080" -v "$VOLUME:/data" \
    -e GUANDAN_PUBLIC_URL="$PUBLIC_URL" -e GUANDAN_TRUSTED_PROXIES="$TRUSTED_PROXIES" "$@" \
    "$img" >/dev/null
}

wait_ready() {
  i=0
  while [ "$i" -lt "$READY_TIMEOUT" ]; do
    if docker exec "$NAME" /guandan healthcheck --ready >/dev/null 2>&1; then
      return 0
    fi
    if ! running "$NAME"; then
      log "容器 $NAME 已退出"
      return 1
    fi
    # --restart unless-stopped 会把崩溃的进程一直拉起来，看重启次数才知道它其实起不来。
    if [ "$(docker container inspect -f '{{.RestartCount}}' "$NAME")" -gt 0 ]; then
      log "容器 $NAME 启动后崩溃重启过"
      return 1
    fi
    sleep 1
    i=$((i + 1))
  done
  log "$READY_TIMEOUT 秒内 /readyz 没有返回 200："
  docker exec "$NAME" /guandan healthcheck --ready 2>&1 | sed 's/^/[deploy]   /' || true
  return 1
}

# 原子地把 current 指向 $1：先建临时链接，再 rename(2) 覆盖。rename 不跟随符号链接，
# 不会出现「链接建到目录里面」的问题（ln -sfn 碰到 current 是真目录时就会这样）。
switch_current() {
  tmp="$WEB_ROOT/.current.$$"
  ln -s "$1" "$tmp"
  perl -e 'rename($ARGV[0], $ARGV[1]) or die "rename: $!\n"' "$tmp" "$CURRENT"
}

site_ok() {
  [ -z "$SITE_URL" ] && return 0
  curl -fsS --max-time 10 "$SITE_URL/readyz" >/dev/null &&
    curl -fsS --max-time 10 "$SITE_URL/" | grep -q 'id="app"'
}

# ---- 0. 预检：在动任何东西之前 ----
docker image inspect "$IMAGE" >/dev/null 2>&1 || die "镜像 $IMAGE 不存在，先 docker load"
[ -f "$WEB_ROOT/$SHA/index.html" ] || die "静态文件 $WEB_ROOT/$SHA/index.html 不存在"
PREV_CURRENT=""
if [ -L "$CURRENT" ]; then
  PREV_CURRENT=$(readlink "$CURRENT")
elif [ -e "$CURRENT" ]; then
  die "$CURRENT 存在但不是符号链接。请人工确认后改成指向某个版本目录的符号链接，再重新发布"
fi
log "镜像 $IMAGE，静态文件 $WEB_ROOT/$SHA，当前前端 ${PREV_CURRENT:-（无）}"

if exists "$PREV"; then
  log "清理上次遗留的 $PREV"
  docker rm -f "$PREV" >/dev/null
fi

# ---- 1、2. 备份、停旧 ----
HAD_PREV=0
if exists "$NAME"; then
  HAD_PREV=1
  log "旧版本：$(docker container inspect -f '{{.Config.Image}}' "$NAME")"
  if running "$NAME"; then
    # 给回滚留一个最新的恢复点：新版本如果跑了 breaking 迁移，旧版本会拒绝启动（runbook §2.2）。
    if docker exec "$NAME" /guandan backup now >/dev/null 2>&1; then
      log "发布前备份完成"
    else
      log "警告：发布前备份失败，继续发布"
    fi
  fi
  log "停止旧容器（优雅关闭，最多 30 秒）"
  docker stop -t 30 "$NAME" >/dev/null
  docker rename "$NAME" "$PREV"
fi

rollback() {
  log "最近的日志："
  docker logs --tail 30 "$NAME" 2>&1 | sed 's/^/[deploy]   /' || true
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  if [ -n "$PREV_CURRENT" ] && [ "$(readlink "$CURRENT" 2>/dev/null)" != "$PREV_CURRENT" ]; then
    switch_current "$PREV_CURRENT"
    log "前端已切回 $PREV_CURRENT"
  fi
  if [ "$HAD_PREV" = 0 ]; then
    log "发布失败，没有旧版本可以回滚"
    exit 1
  fi
  log "回滚到旧容器"
  docker rename "$PREV" "$NAME"
  docker start "$NAME" >/dev/null
  if wait_ready; then
    log "已回滚，旧版本恢复服务"
  else
    log "回滚后旧版本也没有就绪。日志里如果是「不兼容迁移」，按 docs/runbook.md §2.2 用发布前的快照恢复"
  fi
  exit 1
}

# ---- 3. 启新、等就绪 ----
log "启动 $IMAGE（127.0.0.1:$PORT → :8080，内存上限 $MEMORY）"
if ! start "$IMAGE" || ! wait_ready; then
  log "新版本没有就绪"
  rollback
fi
log "/readyz 通过"

# ---- 4、5. 切前端、经 nginx 确认 ----
switch_current "$SHA"
log "前端已切到 $SHA"
if ! site_ok; then
  log "经 nginx 访问 $SITE_URL 失败"
  rollback
fi

# ---- 6. 收尾 ----
[ "$HAD_PREV" = 1 ] && docker rm "$PREV" >/dev/null
# 只保留最近 KEEP 个版本（按目录修改时间）。
kept=0
# 版本目录名都是十六进制的短 SHA，没有空格，按 ls -t 遍历是安全的。
# shellcheck disable=SC2045
for d in $(ls -1t "$WEB_ROOT"); do
  case "$d" in current | .*) continue ;; esac
  [ -d "$WEB_ROOT/$d" ] || continue
  kept=$((kept + 1))
  # 当前版本和上一个版本（手动回滚的目标）无论如何都留着。
  if [ "$kept" -le "$KEEP" ] || [ "$d" = "$SHA" ] || [ "$d" = "$PREV_CURRENT" ]; then continue; fi
  rm -rf "${WEB_ROOT:?}/$d"
  if docker image inspect "$IMAGE_REPO:$d" >/dev/null 2>&1; then
    docker rmi "$IMAGE_REPO:$d" >/dev/null 2>&1 || true
  fi
  log "清理旧版本 $d"
done
log "发布成功：$IMAGE 已就绪，前端 $SHA"
