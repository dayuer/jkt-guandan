#!/bin/sh
# 发往 ssh 的命令里的 $SHA、$WEB_ROOT 故意在本机展开。
# shellcheck disable=SC2029
# 发布到生产：scripts/deploy.sh [git 引用，默认 HEAD]（docs/runbook.md §2，§13.4）
#
# 在本机（或 CI）执行，通过 ssh 操作服务器。生产拓扑见 #12：nginx 管 TLS 和静态前端，
# 容器只听 127.0.0.1:18200。
#   1. 用 git worktree 取出这个提交的干净副本，构建镜像 guandan:<短 SHA>（按服务器的 CPU 架构交叉编译）
#      和前端产物 web/dist（docker build --target web，取完就删构建镜像）；
#   2. 把镜像送到服务器，两种方式（IMAGE_SOURCE）；服务器上已有同名镜像就跳过：
#      - ssh（默认）：docker save | gzip | ssh docker load。镜像约 6 MB，不需要任何凭据，
#        也不用在服务器上构建（构建镜像 350 MB，那台机器磁盘紧张）；
#      - ghcr：本机 docker push 到 ghcr.io/dayuer/jkt-guandan:<短 SHA>，服务器 docker pull。
#        需要本机 docker login ghcr.io（write:packages），并且 package 设为公开或服务器也 login 过，
#        见 runbook §2.4；
#   3. 静态文件打包传到 /var/www/guandan/<短 SHA>/（先解到临时目录再改名，不会出现半个目录）；
#   4. 在服务器上执行 scripts/deploy-remote.sh：换容器 → 等 /readyz → 切 current → 失败就全部回滚。
#
# 环境变量：
#   SSH_HOST      必填，如 root@<服务器>（不写进仓库，见 runbook §2）
#   WEB_ROOT      默认 /var/www/guandan
#   IMAGE_SOURCE  ssh（默认）或 ghcr
#   GHCR_REPO     默认 ghcr.io/dayuer/jkt-guandan
#   其余（PORT、MEMORY、PUBLIC_URL、READY_TIMEOUT、KEEP……）原样传给 deploy-remote.sh
set -eu

REF=${1:-HEAD}
SSH_HOST=${SSH_HOST:?"设置 SSH_HOST，如 SSH_HOST=root@<服务器> scripts/deploy.sh"}
WEB_ROOT=${WEB_ROOT:-/var/www/guandan}
IMAGE_SOURCE=${IMAGE_SOURCE:-ssh}
GHCR_REPO=${GHCR_REPO:-ghcr.io/dayuer/jkt-guandan}
case "$IMAGE_SOURCE" in ssh | ghcr) ;; *) echo "IMAGE_SOURCE 只能是 ssh 或 ghcr" >&2; exit 2 ;; esac

log() { printf '[deploy] %s\n' "$*"; }

cd "$(git rev-parse --show-toplevel)"
FULL=$(git rev-parse --verify "$REF^{commit}")
SHA=$(git rev-parse --short=7 "$FULL")
log "发布 $SHA（$FULL）到 $SSH_HOST"

case "$(ssh "$SSH_HOST" uname -m)" in
  x86_64 | amd64) PLATFORM=linux/amd64 ;;
  aarch64 | arm64) PLATFORM=linux/arm64 ;;
  *) log "不认识服务器的 CPU 架构"; exit 1 ;;
esac

work=$(mktemp -d)
src="$work/src"
cleanup() {
  git worktree remove --force "$src" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
git worktree add --detach "$src" "$FULL" >/dev/null

log "构建镜像 guandan:$SHA（$PLATFORM）"
docker build -q --platform "$PLATFORM" --build-arg BUILD="$FULL" -t "guandan:$SHA" "$src" >/dev/null

log "构建前端产物"
docker build -q --target web -t "guandan-web:$SHA" "$src" >/dev/null
cid=$(docker create "guandan-web:$SHA")
docker cp "$cid:/src/web/dist" "$work/dist"
docker rm "$cid" >/dev/null
docker rmi "guandan-web:$SHA" >/dev/null
[ -f "$work/dist/index.html" ] || { log "前端产物里没有 index.html"; exit 1; }

if ssh "$SSH_HOST" docker image inspect "guandan:$SHA" >/dev/null 2>&1; then
  log "服务器上已有镜像 guandan:$SHA，跳过上传"
elif [ "$IMAGE_SOURCE" = ghcr ]; then
  log "推送 $GHCR_REPO:$SHA，服务器从 GHCR 拉取"
  docker tag "guandan:$SHA" "$GHCR_REPO:$SHA"
  docker push -q "$GHCR_REPO:$SHA" >/dev/null
  # 服务器上统一叫 guandan:<短 SHA>，deploy-remote.sh 和清理旧版本都按这个名字找。
  ssh "$SSH_HOST" "docker pull -q '$GHCR_REPO:$SHA' >/dev/null && docker tag '$GHCR_REPO:$SHA' 'guandan:$SHA' && docker rmi '$GHCR_REPO:$SHA' >/dev/null"
else
  log "上传镜像（docker save | gzip | ssh docker load）"
  docker save "guandan:$SHA" | gzip | ssh "$SSH_HOST" 'gunzip | docker load -q' >/dev/null
fi

log "上传静态文件到 $WEB_ROOT/$SHA/"
# macOS 的 bsdtar 会带上扩展属性（LIBARCHIVE.xattr.com.apple.provenance 等），服务器上的 GNU tar
# 解包时会报一串 warning。COPYFILE_DISABLE 去掉 ._ 文件，--no-xattrs（本机 tar 支持时）去掉扩展属性。
tar_flags=""
if tar --no-xattrs -cf /dev/null -T /dev/null 2>/dev/null; then tar_flags="--no-xattrs"; fi
# shellcheck disable=SC2086 # tar_flags 为空时不该变成一个空参数
COPYFILE_DISABLE=1 tar $tar_flags -C "$work/dist" -czf - . | ssh "$SSH_HOST" "set -eu
  d='$WEB_ROOT/$SHA'
  if [ -d \"\$d\" ]; then cat >/dev/null; echo '[deploy] 静态目录已存在，跳过'; exit 0; fi
  rm -rf \"\$d.tmp\" && mkdir -p \"\$d.tmp\"
  tar -xzf - -C \"\$d.tmp\"
  chmod -R a+rX \"\$d.tmp\"
  mv \"\$d.tmp\" \"\$d\""

log "在服务器上切换"
# 只把 deploy-remote.sh 认识的变量传过去。
env_args="SHA=$SHA WEB_ROOT=$WEB_ROOT"
for v in NAME PORT VOLUME ENV_FILE PUBLIC_URL SITE_URL MEMORY READY_TIMEOUT KEEP; do
  eval "val=\${$v-}"
  # shellcheck disable=SC2154 # val 由上面的 eval 赋值
  [ -n "$val" ] && env_args="$env_args $v='$val'"
done
ssh "$SSH_HOST" "env $env_args sh -s" < scripts/deploy-remote.sh
