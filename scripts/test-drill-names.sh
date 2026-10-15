#!/bin/sh
# 并发隔离自测（#183，写法和 test-deploy-names.sh 一样）：同一台机器上两路 local-ci/docker
# 同时跑时，restore-drill.sh 不能互相删改对方的容器、卷，也不能抢同一个宿主端口。
#
# 做法：在 PATH 前面放一个**假 docker**（只把参数记下来、永远成功），然后跑两遍
# `restore-drill.sh --print-names` —— 两个进程就是「两次同时发生的运行」。--print-names
# 模式照样会走开头的 `cleanup_before` 和退出时 trap 里的 `cleanup`，所以记下来的正好是
# 「这一次运行碰过的全部资源」。然后断言：
#
#   1. 两次运行的编号 / 容器名 / 卷名各不相同，而且名字里带着本次运行的编号；
#   2. **六个端口两两不同**（含交叉比较：这一路的第二个口不能等于另一路的第一个口）；
#   3. 第一次运行碰过的资源里没有任何一个属于第二次，反过来也一样（清理只碰自己那组）。
#
# 第 1、2 条的两次运行**编号是相邻的**（N 和 N+1，靠 DEPLOYTEST_RUN 指定）。并发起的两路
# PID 就是相邻的，而那正是最容易撞的一种 —— 这个脚本要 3 个端口，起点只按 1 步进的话，
# B 的三个口会正好插进 A 的三个口里（#181 复审阻塞 1 是同一个形状）。
#
# 不碰真实 docker、不需要镜像，毫秒级跑完。由 restore-drill.sh 在开跑前调用，
# 也可以单独跑：scripts/test-drill-names.sh
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SELF="$ROOT/scripts/restore-drill.sh"

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

# 跑一次 --print-names。$1 = 这次运行的 docker 调用日志；$2 = 这次运行的编号。
#
# **必须把 PORT_OLD / PORT_NEW / PORT_METRICS 清掉**：本脚本是 restore-drill.sh 调起来的，
# 而它把这三个 export 出去了（docker run 要用）。不清的话两次探测都继承同一组端口，走的
# 是「显式指定端口」那条分支 —— 结果一样，但什么也没验到。清成空串即可：
# restore-drill.sh 那边判的是 `[ -n "${PORT_OLD:-}" ]`。
run_once() {
  DOCKER_LOG="$1" PATH="$work/shim:$PATH" DEPLOYTEST_RUN="$2" \
    PORT_OLD= PORT_NEW= PORT_METRICS= sh "$SELF" --print-names
}

# 从键值对里取一个字段（值里可能有空格）。
field() { # $1=键 $2=全部输出
  printf '%s\n' "$2" | sed -n "s/^$1=//p"
}

# 编号**随机**取，不写死：写死的话，机器上正好有别的进程占着那一段端口，自测就误报
# （#183 复审，和 #181 同一个道理）。挑一个「这个编号和下一个编号的窗口都凑得出端口」的。
try_id() { # $1 = 编号；能跑通返回 0
  PORT_OLD= PORT_NEW= PORT_METRICS= DEPLOYTEST_RUN="$1" PATH="$work/shim:$PATH" \
    sh "$SELF" --print-names >/dev/null 2>&1
}
pick_id() {
  i=0
  while [ "$i" -lt 25 ]; do
    cand=$(( ($$ * 7 + i * 977) % 1250 ))
    if try_id "$cand" && try_id "$((cand + 1))"; then
      printf '%s' "$cand"; return 0
    fi
    i=$((i + 1))
  done
  return 1
}

# 相邻的两个编号：并发起的两路就是这个关系。
A_ID=$(pick_id) || {
  printf '找不到两个相邻编号、窗口都凑得出端口的编号 —— 本机这一段占用异常（见 #183）。\n' >&2
  exit 1
}
B_ID=$((A_ID + 1))

A_OUT=$(run_once "$work/a.log" "$A_ID")
B_OUT=$(run_once "$work/b.log" "$B_ID")

A_OLD=$(field OLD "$A_OUT")
B_OLD=$(field OLD "$B_OUT")
A_NEW=$(field NEW "$A_OUT")
B_NEW=$(field NEW "$B_OUT")
A_OLD_VOL=$(field OLD_VOL "$A_OUT")
B_OLD_VOL=$(field OLD_VOL "$B_OUT")
A_NEW_VOL=$(field NEW_VOL "$A_OUT")
B_NEW_VOL=$(field NEW_VOL "$B_OUT")
A_PORTS=$(field PORTS "$A_OUT")
B_PORTS=$(field PORTS "$B_OUT")

if [ -z "$A_OLD" ] || [ -z "$B_OLD" ] || [ -z "$A_PORTS" ] || [ -z "$B_PORTS" ]; then
  printf '取不到名字或端口 —— --print-names 的输出不对：\n--- A ---\n%s\n--- B ---\n%s\n' "$A_OUT" "$B_OUT"
  exit 1
fi

echo "--- scripts/restore-drill.sh 的并发隔离（#183，编号 $A_ID / ${B_ID}）---"

# 1. 两次运行的名字不同，而且带上了本次运行的编号。
same_or_diff() { # $1=标签 $2=A 的值 $3=B 的值
  if [ "$2" = "$3" ]; then bad "两次运行的 $1 不同" '不同' "两次都是 $2"
  else ok "两次运行的 $1 不同（$2 / $3）"; fi
}
same_or_diff OLD "$A_OLD" "$B_OLD"
same_or_diff NEW "$A_NEW" "$B_NEW"
same_or_diff OLD_VOL "$A_OLD_VOL" "$B_OLD_VOL"
same_or_diff NEW_VOL "$A_NEW_VOL" "$B_NEW_VOL"

case "$A_OLD" in
  *"-${A_ID}") ok "容器名带本次运行的编号（-${A_ID}）" ;;
  *) bad "容器名带本次运行的编号（-${A_ID}）" "以 -${A_ID} 结尾" "$A_OLD" ;;
esac
case "$B_OLD" in
  *"-${B_ID}") ok "另一次也是（-${B_ID}）" ;;
  *) bad "另一次也是（-${B_ID}）" "以 -${B_ID} 结尾" "$B_OLD" ;;
esac
case "$A_OLD" in
  drill-old|drill-old-data) bad '不再是写死的 drill-old' '带后缀' "$A_OLD" ;;
  *) ok '不再是写死的 drill-old' ;;
esac

# 2. 六个端口**两两**不同 —— 含交叉比较（这一路的第二个口 vs 另一路的第一个口）。
#
# 这一条才是拦「起点按 1 步进」的：那样 A 占 (p, p+1, p+2)、B 占 (p+1, p+2, p+3)，
# 两路中间两个口完全重合。
DUPES=$(printf '%s\n' $A_PORTS $B_PORTS | sort | uniq -d | tr '\n' ' ')
if [ -n "$DUPES" ]; then
  bad '六个端口两两不同（含交叉）' '没有重复' "A=$A_PORTS B=${B_PORTS}，重复：$DUPES"
else
  ok "六个端口两两不同（A=$A_PORTS B=${B_PORTS}）"
fi

# 3. 清理只碰自己那组：A 的 docker 调用里不能出现 B 的任何名字。
check_disjoint() { # $1=名字 $2=另一路的容器/卷 $3=自己的日志
  local who="$1" other="$2" log="$3" o hit=0 seen=''
  for o in $other; do
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
check_disjoint '第一路' "$B_OLD $B_NEW $B_OLD_VOL $B_NEW_VOL" "$work/a.log"
check_disjoint '第二路' "$A_OLD $A_NEW $A_OLD_VOL $A_NEW_VOL" "$work/b.log"

# 4. 反面：自己的资源确实被碰过。不然上面那条「没出现对方」可能只是因为日志是空的。
if grep -qF -- "$A_OLD" "$work/a.log"; then
  ok '第一路确实碰了自己的容器（日志不是空的）'
else
  bad '第一路确实碰了自己的容器（日志不是空的）' "出现 $A_OLD" "$(head -5 "$work/a.log")"
fi

# 5. 窗口里第一个口被**外部进程**占着时：这一路要在自己的窗口里往后挪，而且**不能挪进
#    下一个编号的窗口**。做法：用 Python 先绑住 C 窗口的第一个口，再跑 C 和 C+1。
#
#    stdout/stderr 必须接到 /dev/null：这个后台进程会继承调用方的管道，而套在外面的
#    `out=$(scripts/test-drill-names.sh)` 会一直等管道关闭 —— 不重定向的话测试会卡到
#    那个 python 自己超时为止。
hold_port() { # $1=起始端口 $2=占几个 $3=哨兵文件（放开就建它）
  python3 -c '
import os, socket, sys, time
start, n = int(sys.argv[1]), int(sys.argv[2])
# 重试几秒再放弃：上一个占着口的小进程刚收到哨兵，未必已经把口放开；
# 不重试的话这里 bind 失败、什么也没占住，下面的断言就会莫名其妙地过。
for _ in range(50):
    held = []
    for q in range(start, start + n):
        s = socket.socket()
        try:
            s.bind(("127.0.0.1", q))
        except OSError:
            for x in held:
                x.close()
            held = []
            break
        held.append(s)
    if len(held) == n:
        break
    time.sleep(0.1)
for _ in range(600):        # 最多守 60 秒：脚本异常退出也不会一直挂着
    if os.path.exists(sys.argv[3]):
        break
    time.sleep(0.1)
' "$1" "$2" "$3" >/dev/null 2>&1 &
  sleep 1
}

C_ID=$A_ID
C_BASE=$((30000 + (C_ID % 1250) * 8))
hold_port "$C_BASE" 1 "$work/hold1.stop"
C_OUT=$(run_once "$work/c.log" "$C_ID")
D_OUT=$(run_once "$work/d.log" "$((C_ID + 1))")
: > "$work/hold1.stop"   # 放开

C_PORTS=$(field PORTS "$C_OUT")
D_PORTS=$(field PORTS "$D_OUT")

if [ -z "$C_PORTS" ] || [ -z "$D_PORTS" ]; then
  bad '窗口第一个口被占住时仍然能起（C 要往窗口里挪）' '两个都拿到端口' \
      "C=${C_PORTS:-<空>} D=${D_PORTS:-<空>}；C 窗口 ${C_BASE}–$((C_BASE + 7))"
else
  ok "窗口第一个口被占住时仍然能起（C 挪到了 $(printf '%s' "$C_PORTS" | cut -d' ' -f1)）"
  C_DUPES=$(printf '%s\n' $C_PORTS $D_PORTS | sort | uniq -d | tr '\n' ' ')
  if [ -n "$C_DUPES" ]; then
    bad '挤过之后六个端口仍然两两不同' '没有重复' \
        "C=$C_PORTS D=${D_PORTS}，重复：$C_DUPES"
  else
    ok "挤过之后六个端口仍然两两不同（C=$C_PORTS D=${D_PORTS}）"
  fi
  C_FIRST=$(printf '%s' "$C_PORTS" | cut -d' ' -f1)
  if [ "$C_FIRST" -gt "$C_BASE" ]; then
    ok "C 在自己的窗口里往后挪了（${C_BASE} 被占，用 ${C_FIRST}）"
  else
    bad 'C 在自己的窗口里往后挪' "> $C_BASE" "$C_FIRST"
  fi
  D_FIRST=$(printf '%s' "$D_PORTS" | cut -d' ' -f1)
  if [ "$D_FIRST" -ge "$((C_BASE + 8))" ]; then
    ok "D 没有被挤进 C 的窗口（D 从 $D_FIRST 起）"
  else
    bad 'D 没有被挤进 C 的窗口' "≥ $((C_BASE + 8))" "$D_FIRST"
  fi
fi

# 6. 窗口**整个**被占住时：这一路必须**报错退出**，绝不能挪进下一个编号的窗口去抢 ——
#    抢了的话相邻编号的两路还是会撞。占住整个窗口就区分得出来：没有边界的话，
#    C 会滑到 $((C_BASE + 8)) 起，那正是 D 的窗口。
hold_port "$C_BASE" 8 "$work/hold2.stop"
if C2_OUT=$(run_once "$work/c2.log" "$C_ID" 2>&1); then
  bad 'C 的窗口被占满时应当报错退出（不抢下一个编号的窗口）' '退出码非 0' \
      "居然成功了：$(printf '%s' "$C2_OUT" | tr '\n' ' ')"
else
  case "$C2_OUT" in
    *凑不出*) ok 'C 的窗口被占满时报错退出，不抢下一个编号的窗口' ;;
    *) bad 'C 的窗口被占满时报错退出（信息里点明是窗口凑不出）' '含「凑不出」' \
           "$(printf '%s' "$C2_OUT" | tr '\n' ' ')" ;;
  esac
fi
: > "$work/hold2.stop"

D2_OUT=$(run_once "$work/d2.log" "$((C_ID + 1))")
D2_PORTS=$(field PORTS "$D2_OUT")
D2_FIRST=$(printf '%s' "$D2_PORTS" | cut -d' ' -f1)
if [ -n "$D2_FIRST" ] && [ "$D2_FIRST" -ge "$((C_BASE + 8))" ]; then
  ok "C 报错之后 D 照旧待在自己的窗口里（D 从 ${D2_FIRST} 起）"
else
  bad 'C 报错之后 D 照旧待在自己的窗口里' "≥ $((C_BASE + 8))" "${D2_FIRST:-<空>}"
fi

echo
if [ "$fails" -eq 0 ]; then echo "OK: $0 全过"; exit 0; fi
echo "FAILED: $fails 条"; exit 1
