#!/bin/sh
# 发布：scripts/deploy.sh <镜像 tag，一般是提交 SHA>（docs/server-design.md §13.4，docs/runbook.md）
#
# 依次：拉镜像 → 给旧版本做一次备份 → 停旧容器（触发优雅关闭）→ 启新容器 → 等 /readyz。
# READY_TIMEOUT 秒内没就绪就回滚：删掉新容器，把旧容器原样启动回来。
#
# 可以用环境变量覆盖：
#   IMAGE_REPO     镜像仓库，默认 ghcr.io/dayuer/jkt-guandan
#   NAME           容器名，默认 guandan
#   ENV_FILE       docker --env-file，默认 /etc/guandan/guandan.env（GUANDAN_DOMAIN 等写在这里）
#   VOLUME         数据卷，默认 guandan-data
#   PORTS          端口映射，默认 "-p 80:8080 -p 443:8443"
#   EXTRA_ARGS     额外的 docker run 参数
#   READY_TIMEOUT  等就绪的秒数，默认 60
#   PULL           设为 0 不拉镜像（本机测试用）
set -eu

TAG=${1:?"用法：scripts/deploy.sh <镜像 tag>"}
IMAGE_REPO=${IMAGE_REPO:-ghcr.io/dayuer/jkt-guandan}
NAME=${NAME:-guandan}
ENV_FILE=${ENV_FILE:-/etc/guandan/guandan.env}
VOLUME=${VOLUME:-guandan-data}
PORTS=${PORTS:-"-p 80:8080 -p 443:8443"}
EXTRA_ARGS=${EXTRA_ARGS:-}
READY_TIMEOUT=${READY_TIMEOUT:-60}
PULL=${PULL:-1}

IMAGE="$IMAGE_REPO:$TAG"
PREV="$NAME-prev"

log() { printf '[deploy] %s\n' "$*"; }

exists() { docker container inspect "$1" >/dev/null 2>&1; }
running() { [ "$(docker container inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = true ]; }

start() {
  # shellcheck disable=SC2086 # PORTS 和 EXTRA_ARGS 需要按空格拆开
  docker run -d --name "$NAME" --restart unless-stopped \
    --read-only --cap-drop ALL --stop-timeout 30 \
    --log-opt max-size=10m --log-opt max-file=5 \
    $PORTS -v "$VOLUME:/data" --env-file "$ENV_FILE" $EXTRA_ARGS \
    "$1" >/dev/null
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
  log "$READY_TIMEOUT 秒内 /readyz 没有返回 200"
  docker exec "$NAME" /guandan healthcheck --ready 2>&1 | sed 's/^/[deploy]   /' || true
  return 1
}

[ -r "$ENV_FILE" ] || { log "读不到 $ENV_FILE"; exit 2; }
if [ "$PULL" = 1 ]; then
  log "拉取 $IMAGE"
  docker pull -q "$IMAGE" >/dev/null
fi
docker image inspect "$IMAGE" >/dev/null

if exists "$PREV"; then
  log "清理上次遗留的 $PREV"
  docker rm -f "$PREV" >/dev/null
fi

HAD_PREV=0
if exists "$NAME"; then
  HAD_PREV=1
  log "旧版本：$(docker container inspect -f '{{.Config.Image}}' "$NAME")"
  if running "$NAME"; then
    # 给回滚留一个最新的恢复点：新版本如果跑了旧版本不认识的迁移，旧版本会拒绝启动（见 runbook）。
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

log "启动 $IMAGE"
if start "$IMAGE" && wait_ready; then
  [ "$HAD_PREV" = 1 ] && docker rm "$PREV" >/dev/null
  log "发布成功：$IMAGE 已就绪"
  exit 0
fi

log "新版本没有就绪，最近的日志："
docker logs --tail 30 "$NAME" 2>&1 | sed 's/^/[deploy]   /' || true
docker rm -f "$NAME" >/dev/null 2>&1 || true

if [ "$HAD_PREV" = 0 ]; then
  log "发布失败，没有旧版本可以回滚"
  exit 1
fi
log "回滚到旧版本"
docker rename "$PREV" "$NAME"
docker start "$NAME" >/dev/null
if wait_ready; then
  log "已回滚，旧版本恢复服务"
else
  log "回滚后旧版本也没有就绪。如果日志里是「不认识的迁移」，按 docs/runbook.md「回滚失败」一节用发布前的快照恢复"
fi
exit 1
