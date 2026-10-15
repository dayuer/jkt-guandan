#!/bin/sh
# 并发隔离自测（#181）：同一台机器上两路 local-ci/docker 同时跑时，test-deploy.sh 不能
# 互相删改对方的容器、卷、镜像，也不能抢同一个宿主端口。
#
# 做法：在 PATH 前面放一个**假 docker**（只把参数记下来、永远成功），然后跑两遍
# `test-deploy.sh --print-names` —— 两个进程就是「两次同时发生的运行」。--print-names
# 模式照样会走头上那两行 `docker rm -f` 和退出时 trap 里的 cleanup，所以记下来的正好是
# 「这一次运行碰过的全部资源」。然后断言：
#
#   1. 两次运行的编号 / 名字 / 端口各不相同，而且名字里带着本次运行的编号；
#   2. **四个端口两两不同**（含交叉比较：A 的第二个口不能等于 B 的第一个口）；
#   3. 第一次运行碰过的资源里没有任何一个属于第二次，反过来也一样（清理只碰自己那组）。
#
# 第 1、2 条的两次运行**编号是相邻的**（N 和 N+1，靠 DEPLOYTEST_RUN 指定）。这一点是
# 关键：并发起的两路 PID 就是相邻的，而那正是最容易撞的一种 —— 起点差 1 时，B 的 PORT
# 正好是 A 的 PORT_OLD。先后跑两次拿到的 PID 永远不会相邻，光靠那样是发现不了这个的
# （#181 复审的阻塞 1 就是这么漏掉的）。
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

# 跑一次 --print-names。$1 = 这次运行的 docker 调用日志；$2 = 这次运行的编号。
#
# **必须把 PORT / PORT_OLD 清掉**：本脚本通常是 test-deploy.sh 调起来的，而 test-deploy.sh
# 把 PORT export 出去了（deploy-remote.sh 要读）。不清的话两次探测都继承同一个 PORT，
# 走的是「显式指定端口」那条分支，两次当然一样 —— 本地单独跑看不出来，只有从
# test-deploy.sh 里跑才会露。清成空串即可：test-deploy.sh 那边是 `[ -n "${PORT:-}" ]`。
run_once() {
  DOCKER_LOG="$1" PATH="$work/shim:$PATH" PORT= PORT_OLD= DEPLOYTEST_RUN="$2" \
    sh "$SELF" --print-names
}

# 从键值对里取一个字段（值里没有空格）。
field() { # $1=键 $2=全部输出
  printf '%s\n' "$2" | sed -n "s/^$1=//p"
}

# 相邻的两个编号：并发起的两路就是这个关系。
# 编号**随机**取，不写死：写死的话，机器上正好有别的进程占着那一段端口，自测就误报
# （#181 复审）。挑一个「这个编号和下一个编号的窗口都凑得出端口」的编号。
try_id() { # $1 = 编号；能跑通返回 0
  PORT= PORT_OLD= DEPLOYTEST_RUN="$1" PATH="$work/shim:$PATH" sh "$SELF" --print-names >/dev/null 2>&1
}
# 取值上限是 **1248**，不是 1249（#186）：窗口是 `(编号 % 1250) * 8`，取到 1249 的话
# 「下一个编号」1250 会绕回窗口 0，而窗口 1249 和窗口 0 隔着整个区间 —— 「D 在自己的
# 窗口里」那两条断言就会误报（约 1/1250，每次 docker 跑两份自测）。
pick_id() {
  i=0
  while [ "$i" -lt 25 ]; do
    cand=$(( ($$ * 7 + i * 977) % 1249 ))
    if try_id "$cand" && try_id "$((cand + 1))"; then
      printf '%s' "$cand"; return 0
    fi
    i=$((i + 1))
  done
  return 1
}

# DEPLOYTEST_RUN 给了就用它（测边界用），否则随机挑一个。
if [ -n "${DEPLOYTEST_RUN:-}" ]; then A_ID="$DEPLOYTEST_RUN"; else A_ID=$(pick_id); fi
[ -n "${A_ID:-}" ] || {
  printf '找不到两个相邻编号、窗口都凑得出端口的编号 —— 本机这一段占用异常（见 #181）。\n' >&2
  exit 1
}
B_ID=$((A_ID + 1))
if [ "$A_ID" -gt 1248 ]; then
  printf '自测编号超出范围：%s（必须 ≤ 1248，否则 B 会绕回窗口 0，见 #186）\n' "$A_ID" >&2
  exit 1
fi

A_OUT=$(run_once "$work/a.log" "$A_ID")
B_OUT=$(run_once "$work/b.log" "$B_ID")

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

echo "--- scripts/test-deploy.sh 的并发隔离（#181，编号 $A_ID / ${B_ID}）---"

# 1. 两次运行的名字不同，而且带上了本次运行的编号。
same_or_diff() { # $1=标签 $2=A 的值 $3=B 的值
  if [ "$2" = "$3" ]; then bad "两次运行的 $1 不同" '不同' "两次都是 $2"
  else ok "两次运行的 $1 不同（$2 / $3）"; fi
}
same_or_diff NAME "$A_NAME" "$B_NAME"
same_or_diff VOLUME "$A_VOL" "$B_VOL"
same_or_diff REPO "$A_REPO" "$B_REPO"

case "$A_NAME" in
  *"-${A_ID}") ok "容器名带本次运行的编号（-${A_ID}）" ;;
  *) bad "容器名带本次运行的编号（-${A_ID}）" "以 -${A_ID} 结尾" "$A_NAME" ;;
esac
case "$B_NAME" in
  *"-${B_ID}") ok "另一次也是（-${B_ID}）" ;;
  *) bad "另一次也是（-${B_ID}）" "以 -${B_ID} 结尾" "$B_NAME" ;;
esac
[ "$A_NAME" != 'guandan-deploytest' ] && ok '不再是写死的 guandan-deploytest' \
  || bad '不再是写死的 guandan-deploytest' '带后缀' "$A_NAME"

# 2. 四个端口**两两**不同 —— 包括交叉的 A_PORT2 / B_PORT。
#
# 这一条才是拦「起点按 1 步进」的：那样 A 占 (p, p+1)、B 占 (p+1, p+2)，
# A 的第二个口就是 B 的第一个口，而 A 要到几分钟后的场景 7 才去绑它。
DUPES=$(printf '%s\n' "$A_PORT" "$A_PORT2" "$B_PORT" "$B_PORT2" | sort | uniq -d | tr '\n' ' ')
if [ -n "$DUPES" ]; then
  bad '四个端口两两不同（含交叉）' '没有重复' \
      "A=$A_PORT/$A_PORT2 B=$B_PORT/${B_PORT2}，重复：$DUPES"
else
  ok "四个端口两两不同（A=$A_PORT/$A_PORT2 B=$B_PORT/${B_PORT2}）"
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

# 5. 窗口里第一个口被**外部进程**占着时：这一路要在自己的窗口里往后挪，而且**不能挪进
#    下一个编号的窗口** —— 挪进去的话，相邻编号的两路还是会撞（auditor 实测：占住 28485
#    时自测就红了）。做法：拿 Python 先绑住 C 窗口的第一个口，再跑 C 和 C+1。
hold_port() { # $1=端口；放开用 release_port。用哨兵文件收，不用 kill —— kill 会在
              # 脚本输出里留一行 "Terminated"，白白让人以为是挂了。
  python3 -c '
import os, socket, sys, time
s = socket.socket()
s.bind(("127.0.0.1", int(sys.argv[1])))
s.listen(1)
for _ in range(600):        # 最多守 60 秒：脚本异常退出时也不会一直挂着
    if os.path.exists(sys.argv[2]):
        break
    time.sleep(0.1)
' "$1" "$work/hold.stop" >/dev/null 2>&1 &
  # 上面必须把 stdout/stderr 接到 /dev/null：这个后台进程会继承调用方的管道，
  # 而套在外面的 `out=$(scripts/test-deploy-names.sh)` 会一直等管道关闭 —— 不重定向的话
  # 测试会卡到那个 python 自己超时为止（实测卡满 60 秒）。
  sleep 1   # 等它真的绑上
}
release_port() { : > "$work/hold.stop"; }

# 把一整段端口占住。$1=起始端口 $2=几个 $3=哨兵文件
hold_range() {
  python3 -c '
import os, socket, sys, time
start, n = int(sys.argv[1]), int(sys.argv[2])
# 重试几秒再放弃：上一个占着口的小进程刚收到哨兵，未必已经把口放开。
# 不重试的话这里 bind 失败、什么也没占住，下面的断言就会莫名其妙地过（实测 3 次里红 2 次）。
held = []
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
for _ in range(600):        # 最多守 60 秒
    if os.path.exists(sys.argv[3]):
        break
    time.sleep(0.1)
' "$1" "$2" "$3" >/dev/null 2>&1 &
  sleep 1
}

C_ID=$A_ID
C_BASE=$((20000 + (C_ID % 1250) * 8))
hold_port "$C_BASE"
C_OUT=$(run_once "$work/c.log" "$C_ID")
D_OUT=$(run_once "$work/d.log" "$((C_ID + 1))")
release_port

C_PORT=$(field PORT "$C_OUT")
C_PORT2=$(field PORT_OLD "$C_OUT")
D_PORT=$(field PORT "$D_OUT")
D_PORT2=$(field PORT_OLD "$D_OUT")

if [ -z "$C_PORT" ] || [ -z "$D_PORT" ]; then
  bad '窗口第一个口被占住时仍然能起（C 要往窗口里挪）' '两个都拿到端口' \
      "C=${C_PORT:-<空>} D=${D_PORT:-<空>}；C 窗口 ${C_BASE}–$((C_BASE + 7))"
else
  ok "窗口第一个口被占住时仍然能起（C 挪到了 ${C_PORT}）"
  if [ "$C_PORT" -gt "$C_BASE" ]; then
    ok "C 在自己的窗口里往后挪了（${C_BASE} 被占，用 ${C_PORT}）"
  else
    bad 'C 在自己的窗口里往后挪' "> $C_BASE" "$C_PORT"
  fi
  if [ "$D_PORT" -ge "$((C_BASE + 8))" ]; then
    ok "D 没有被挤进 C 的窗口（D=$D_PORT ≥ $((C_BASE + 8))）"
  else
    bad 'D 没有被挤进 C 的窗口' "≥ $((C_BASE + 8))" "$D_PORT"
  fi
  C_DUPES=$(printf '%s\n' "$C_PORT" "$C_PORT2" "$D_PORT" "$D_PORT2" | sort | uniq -d | tr '\n' ' ')
  if [ -n "$C_DUPES" ]; then
    bad '挤过之后四个端口仍然两两不同' '没有重复' \
        "C=$C_PORT/$C_PORT2 D=$D_PORT/${D_PORT2}，重复：$C_DUPES"
  else
    ok "挤过之后四个端口仍然两两不同（C=$C_PORT/$C_PORT2 D=$D_PORT/${D_PORT2}）"
  fi
fi

# 6. 窗口**整个**被占住时：这一路必须**报错退出**，绝不能挪进下一个编号的窗口去抢 ——
#    抢了的话相邻编号的两路还是会撞。
#
#    这一条才是真正钉住「窗口」这个设计的：只占住起点那个口时，往后挪一格还在窗口里，
#    去掉窗口边界也一样能过（试过）。把整个窗口占满就区分得出来了：没有边界的话，
#    C 会滑到 $((C_BASE + 8)) 起，那正是 D 的窗口。
hold_range "$C_BASE" 8 "$work/hold2.stop"
if C2_OUT=$(run_once "$work/c2.log" "$C_ID" 2>&1); then
  bad 'C 的窗口被占满时应当报错退出（不抢下一个编号的窗口）' '退出码非 0' \
      "居然成功了：$(printf '%s' "$C2_OUT" | tr '\n' ' ')"
else
  case "$C2_OUT" in
    *凑不出*) ok 'C 的窗口被占满时报错退出，不抢下一个编号的窗口' ;;
    *) bad 'C 的窗口被占满时报错退出（信息里点明是窗口凑不出）' "含「凑不出」" \
           "$(printf '%s' "$C2_OUT" | tr '\n' ' ')" ;;
  esac
fi
release_range_holder() { : > "$work/hold2.stop"; }
release_range_holder

D2_OUT=$(run_once "$work/d2.log" "$((C_ID + 1))")
D2_PORT=$(field PORT "$D2_OUT")
if [ -n "$D2_PORT" ] && [ "$D2_PORT" -ge "$((C_BASE + 8))" ]; then
  ok "C 报错之后 D 照旧待在自己的窗口里（D=${D2_PORT}）"
else
  bad 'C 报错之后 D 照旧待在自己的窗口里' "≥ $((C_BASE + 8))" "${D2_PORT:-<空>}"
fi

echo
if [ "$fails" -eq 0 ]; then echo "OK: $0 全过"; exit 0; fi
echo "FAILED: $fails 条"; exit 1
