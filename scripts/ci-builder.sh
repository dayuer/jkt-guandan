#!/bin/sh
# 本项目专用 buildx builder（#279）：容器化的 buildkitd，缓存上限见 scripts/ci-buildkitd.toml。
#
# 为什么不用默认 builder：它的构建缓存和这台机器上**别的项目共用**，只能全局 prune
# （`docker builder prune` 会把别人的也清掉），于是 local-ci/docker 的缓存只涨不清。
# 专用 builder 的缓存落在它自己的卷里，GC 策略只管它。
#
# 用法：CI_BUILDER="$(scripts/ci-builder.sh)"   # 打印 builder 名（guandan-ci），没有就建
#       docker buildx build --builder "$CI_BUILDER" --load -t … .
#
# 已存在就直接复用。⚠️ buildx 只在 `create` 时读 ci-buildkitd.toml —— 改了 GC 策略
# **不会**作用于已有的 builder，要先 `docker buildx rm guandan-ci`（顺带清掉缓存），
# 再跑一次本脚本重建。
set -eu
NAME=guandan-ci
if ! docker buildx inspect "$NAME" >/dev/null 2>&1; then
  # docker-container driver = 独立 buildkitd 容器，缓存不落在宿主 daemon 的公共缓存里。
  # 建失败**不直接报错**：两个会话同时跑 local-ci 时会同时发现「不存在」、同时 create，
  # 后到的那个会报 already exists。再 inspect 一次，在就说明是对方建好了（#181 的并发教训）。
  docker buildx create --name "$NAME" --driver docker-container \
    --config "$(dirname "$0")/ci-buildkitd.toml" >/dev/null 2>&1 || true
  if ! docker buildx inspect "$NAME" >/dev/null 2>&1; then
    printf '建不起 buildx builder（%s）：docker buildx 可用吗？\n' "$NAME" >&2
    exit 1
  fi
fi
printf '%s\n' "$NAME"
