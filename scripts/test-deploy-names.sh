#!/bin/sh
# 并发隔离自测（#181）：同一台机器上两路 local-ci/docker 同时跑时，test-deploy.sh 不能
# 互相删改对方的容器、卷、镜像，也不能抢同一个宿主端口。
#
# 做法：在 PATH 前面放一个**假 docker**（只把参数记下来、永远成功），然后跑两遍
# `test-deploy.sh --print-names` —— 两个进程就是「两次同时发生的运行」。--print-names
# 模式照样会走头上那两行 `docker rm -f` 和退出时 trap 里的 cleanup，所以记下来的正好是
# 「这一次运行碰过的全部资源」。然后断言：
#
#   1. 两次的 RUN / 名字 / 端口各不相同，而且名字里带着本次运行的 RUN 后缀；
#   2. 第一次运行碰过的资源里没有任何一个属于第二次，反过来也一样（清理只碰自己那组）。
#
# 不碰真实 docker、不需要镜像，毫秒级跑完。由 scripts/test-deploy.sh 在开跑前调用，
# 也可以单独跑：scripts/test-deploy-names.sh
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SELF="$ROOT/scripts/test-deploy.sh"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
: > "$work/a.log"
: > "$work/b.log"

fails=0
ok() { printf '  ok   %s\n' "$1"; }
bad() { printf '  FAIL %s\n       期望: %s\n       实际: %s\n' "$1" "$2" "$3"; fails=$((fails + 1)); }

mkdir -p "$work/shim"
cat > "$work/shim/docker" <<'SHIM'
#!/bin/sh
# 只记录，不做任何事。
printf 'docker %s\n' "$*" >> "$DOCKER_LOG"
exit 0
SHIM
chmod +x "$work/shim/docker"

# 跑一次 --print-names。$1 = 这次运行的 docker 调用日志；stdout 是键值对。
run_once() {
  DOCKER_LOG="$1" PATH="$work/shim:$PATH" sh "$SELF" --print-names
}

# 从键值对里取一个字段（值里没有空格）。
field() { # $1=键 $2=全部输出
  printf '%s\n' "$2" | sed -n "s/^$1=//p"
}

A_OUT=$(run_once "$work/a.log")
B_OUT=$(run_once "$work/b.log")

A_RUN=$(field RUN "$A_OUT")
B_RUN=$(field RUN "$B_OUT")
A_NAME=$(field NAME "$A_OUT")
B_NAME=$(field NAME "$B_OUT")
A_VOL=$(field VOLUME "$A_OUT")
B_VOL=$(field VOLUME "$B_OUT")
A_REPO=$(field REPO "$A_OUT")
B_REPO=$(field REPO "$B_OUT")
A_PORT=$(field PORT "$A_OUT")
B_PORT=$(field PORT "$B_OUT")
A_PORT2=$(field PORT_OLD "$A_OUT")
B_PORT2=$(field PORT_OLD "$B_OUT")

if [ -z "$A_NAME" ] || [ -z "$B_NAME" ]; then
  printf '取不到名字 —— --print-names 的输出不对：\n--- A ---\n%s\n--- B ---\n%s\n' "$A_OUT" "$B_OUT"
  exit 1
fi

echo '--- scripts/test-deploy.sh 的并发隔离（#181）---'

# 1. 两次运行的名字不同，而且带上了本次运行的 RUN 后缀。
same_or_diff() { # $1=标签 $2=A 的值 $3=B 的值
  if [ "$2" = "$3" ]; then bad "两次运行的 $1 不同" '不同' "两次都是 $2"
  else ok "两次运行的 $1 不同（$2 / $3）"; fi
}
same_or_diff NAME "$A_NAME" "$B_NAME"
same_or_diff VOLUME "$A_VOL" "$B_VOL"
same_or_diff REPO "$A_REPO" "$B_REPO"

# 名字里必须带着这一次的 RUN，否则等于又写死了。
case "$A_NAME" in
  *"-${A_RUN}") ok "容器名带本次运行的 RUN 后缀（-${A_RUN}）" ;;
  *) bad "容器名带本次运行的 RUN 后缀（-${A_RUN}）" "以 -${A_RUN} 结尾" "$A_NAME" ;;
esac
case "$B_NAME" in
  *"-${B_RUN}") ok "另一次也是（-${B_RUN}）" ;;
  *) bad "另一次也是（-${B_RUN}）" "以 -${B_RUN} 结尾" "$B_NAME" ;;
esac
[ "$A_NAME" != 'guandan-deploytest' ] && ok '不再是写死的 guandan-deploytest' \
  || bad '不再是写死的 guandan-deploytest' '带后缀' "$A_NAME"

# 2. 端口也是每路自己的，而且同一路里的两个场景端口不撞。
if [ "$A_PORT" = "$B_PORT" ] || [ "$A_PORT2" = "$B_PORT2" ]; then
  bad '两次运行的端口不同' '两路四个端口互不相同' "A=$A_PORT/$A_PORT2 B=$B_PORT/$B_PORT2"
else
  ok "两次运行的端口不同（$A_PORT/$A_PORT2 与 $B_PORT/${B_PORT2}）"
fi
if [ "$A_PORT" = "$A_PORT2" ] || [ "$B_PORT" = "$B_PORT2" ]; then
  bad '同一路里两个场景的端口不同' '各不同' "A=$A_PORT/$A_PORT2 B=$B_PORT/$B_PORT2"
else
  ok '同一路里两个场景的端口不同'
fi

# 3. 清理只碰自己那组：A 的 docker 调用里不能出现 B 的任何名字。
#
# 假 docker 记下来的是**每一次 docker 调用**，包括开头那两行 `rm -f` 和退出时的 cleanup，
# 所以「B 的名字一次都没出现」就等于「A 不会碰到 B 的容器/卷/镜像」。
check_disjoint() { # $1=名字 $2=另一路的名字/卷/镜像仓库 $3=自己的日志
  local who="$1" other="$2" log="$3" o hit=0 seen=''
  for o in $other; do
    # 名字和镜像仓库这次是同一个字符串，去重免得同一条报两遍。
    case " $seen " in *" $o "*) continue ;; esac
    seen="$seen $o"
    if grep -qF -- "$o" "$log"; then
      bad "$who 的 docker 调用里没有对方的资源（${o}）" '一次都不出现' "$(grep -F -- "$o" "$log" | head -3)"
      hit=1
    fi
  done
  if [ "$hit" -eq 0 ]; then ok "$who 的 docker 调用里没有对方的资源"; fi
  return 0   # 不要让 set -e 在这里把后面的断言吃掉
}
check_disjoint '第一路' "$B_NAME $B_VOL $B_REPO" "$work/a.log"
check_disjoint '第二路' "$A_NAME $A_VOL $A_REPO" "$work/b.log"

# 4. 反面：自己的资源确实被碰过。不然上面那条「没出现对方」可能只是因为日志是空的。
if grep -qF -- "$A_NAME" "$work/a.log"; then
  ok '第一路确实碰了自己的容器（日志不是空的）'
else
  bad '第一路确实碰了自己的容器（日志不是空的）' "出现 $A_NAME" "$(head -5 "$work/a.log")"
fi

echo
if [ "$fails" -eq 0 ]; then echo "OK: $0 全过"; exit 0; fi
echo "FAILED: $fails 条"; exit 1
