#!/bin/sh
# 等 docker 容器 / HTTP 就绪的小工具（#186）。
#
#   . "$(dirname "$0")/docker-wait.sh"
#
# 只定义函数，source 进来不产生任何副作用。
#
# 为什么要有它：`restore-drill.sh`、`test-deploy.sh`、`container-smoke.sh` 里原来都是
#
#   for _ in $(seq 1 30); do docker exec "$C" ... && break; sleep 1; done
#
# —— **30 次都没就绪也不报错**，接着往下走。往下多半是 host 侧 curl `/readyz`，于是报一个
# `curl: (7) Failed to connect to 127.0.0.1 port 39449`，看不出到底是「服务没起来」还是
# 「端口映射有问题」。这台机器负载常常很高，这种偶发误报会越来越多。现在超时就明确失败，
# 并把容器的日志和状态打出来。

# 等容器内的 /guandan healthcheck --ready 通过。
#   $1 = 容器名  [$2 = 最多等几秒，默认 30]
# 超时：打印最后 40 行日志和容器状态，返回 1（调用方应当 exit 1）。
wait_container_ready() {
  local c="${1:?wait_container_ready 需要容器名}" n="${2:-30}" i=1
  while [ "$i" -le "$n" ]; do
    docker exec "$c" /guandan healthcheck --ready >/dev/null 2>&1 && return 0
    sleep 1
    i=$((i + 1))
  done
  printf '%s 等了 %s 秒还没就绪 —— 日志和状态：\n' "$c" "$n" >&2
  docker logs --tail 40 "$c" 2>&1 | sed 's/^/  /' >&2
  docker ps -a --filter "name=^${c}$" --format '  {{.Names}}  {{.Status}}  {{.Ports}}' >&2
  return 1
}

# host 侧等一个 HTTP 地址通（带重试，不要只 curl 一次）。
#   $1 = URL  [$2 = 最多等几秒，默认 30]
# 成功：把最后一次的响应体打到 stdout（和直接 curl 一样）；超时：打印最后一次的错误，返回 1。
wait_http_ok() {
  local url="${1:?wait_http_ok 需要 URL}" n="${2:-30}" i=1 body
  while [ "$i" -le "$n" ]; do
    if body=$(curl -fsS --max-time 5 "$url" 2>/dev/null); then
      printf '%s\n' "$body"
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done
  printf '等了 %s 秒 %s 还是连不上（最后一次的错误）：\n' "$n" "$url" >&2
  curl -fsS --max-time 5 "$url" >&2 || true
  return 1
}
