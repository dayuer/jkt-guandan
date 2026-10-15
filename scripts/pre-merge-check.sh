#!/usr/bin/env bash
# coord 的「合并前必跑」（COORDINATION.md §4 合并条件 5）：把待合 PR 依次合进一条临时分支，
# 在那棵树上跑本地 CI，全绿才按顺序合。
#
#   scripts/pre-merge-check.sh [PR号…]
#
#   不带参数 = **整个合并队列**（所有 base 为 main 的开放 PR）
#   带参数   = 只验这几条
#
# 为什么要有它：本地 CI 没有任何自动触发，全靠各会话自己跑；而「同一批 PR 各自都绿、
# **合起来红**」—— git 不撞行、但语义上打架 —— **只有把待合 PR 依次合起来跑才能发现**。
#
# 合的是各 PR **已推送的 head SHA**（fetch `refs/pull/<N>/head`），不是本地分支 ——
# 与 §4 的 `gh pr merge --match-head-commit` 对齐：合的就是要合的那个提交。
#
# **默认全队列**，不许「只验我打算先合的那几条」—— 想只验几条就明写 PR 号。
#
# 退出码：0 全绿可合；1 有红或合不进来；2 环境未就绪；3 脚本自身出错。
# 移植自 dayuer/superapp 的 scripts/pre-merge-check.sh（#157）。
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=local-ci-lib.sh
. "$ROOT/scripts/local-ci-lib.sh"
cd "$ROOT" || exit 2

BASE_BRANCH='main'
BASE="origin/$BASE_BRANCH"
if [ $# -gt 0 ]; then
  PRS="$*"
  MODE='指定'
else
  PRS=''
  MODE='全队列'
fi

git fetch origin --prune -q || { printf '取不到 origin —— 网络？\n' >&2; exit 2; }

# --- 确定队列 ---------------------------------------------------------------
if [ "$MODE" = '全队列' ]; then
  PRS=$(gh pr list --state open --base "$BASE_BRANCH" --limit 200 --json number,isDraft \
          --jq '.[] | select(.isDraft | not) | .number' | sort -n | tr '\n' ' ')
  printf '合并队列（base %s 的非草稿开放 PR）：%s\n' "$BASE_BRANCH" "${PRS:-（空）}"
  if [ -z "$PRS" ]; then printf '队列是空的，没什么可验。\n'; exit 0; fi
else
  printf '只验指定的：%s\n' "$PRS"
fi

# --- 逐条核对（只读，不合并）------------------------------------------------
# 合并条件 1（approve 的 sha 必须等于当前 head）与 2（head 上的 status）一并报出来 ——
# 顺带暴露「作者忘了跑」的 PR（0 条 status）。
printf '\n=== 逐条核对 ===\n'
printf '  %-7s %-10s %-8s %-16s %s\n' 'PR' 'head' 'approve' 'status' '标题'
for n in $PRS; do
  info=$(gh pr view "$n" --json headRefOid,title,comments 2>/dev/null) || { printf '  #%-6s 取不到\n' "$n"; continue; }
  head=$(printf '%s' "$info" | jq -r .headRefOid)
  title=$(printf '%s' "$info" | jq -r .title | cut -c1-40)
  comments=$(printf '%s' "$info" | jq -r '[.comments[].body] | join("\n")')
  verdict=$(approve_vs_head "$(last_approve_sha "$comments")" "$head")
  st=$(gh api "repos/{owner}/{repo}/commits/$head/status" --jq '"\(.state) (\(.statuses|length))"' 2>/dev/null || printf '取不到')
  printf '  #%-6s %-10s %-8s %-16s %s\n' "$n" "${head:0:9}" "$verdict" "$st" "$title"
done
printf '\n  approve 那一列：ok=对得上 / stale=复审后又推了（§4 要求重审）/ none=没人 approve\n'
printf '  status 那一列：括号里是 status 条数，**0 就是没人跑过本地 CI**\n'
printf '  （作者自审的 approve 这里不识别，看板 board.sh 会标出来）\n'

# --- 合并到临时分支并跑 -----------------------------------------------------
STAMP=$(date +%Y%m%d-%H%M%S)
WT="$(mktemp -d)/pre-merge-$STAMP"
BR="tmp/pre-merge-$STAMP-$$"

cleanup() {
  # 无论成败、无论被 Ctrl-C 还是正常退出，临时分支与 worktree 都不留。
  git -C "$ROOT" worktree remove "$WT" --force >/dev/null 2>&1
  git -C "$ROOT" branch -D "$BR" >/dev/null 2>&1
}
trap cleanup EXIT INT TERM

git worktree add "$WT" -b "$BR" "$BASE" >/dev/null 2>&1 || {
  printf '\n建临时分支失败（%s）。\n' "$BR" >&2; exit 2; }

printf '\n=== 依次合进临时分支 %s ===\n' "$BR"
cd "$WT" || exit 2
merge_ok=1
for n in $PRS; do
  if ! git fetch origin "refs/pull/$n/head" -q 2>/dev/null; then
    printf '  ✗ #%s 取不到它的 head（refs/pull/%s/head）\n' "$n" "$n"
    merge_ok=0; continue
  fi
  if git -c user.name=pre-merge -c user.email=pre-merge@local merge --no-edit FETCH_HEAD >/dev/null 2>&1; then
    printf '  ✓ #%s 合进来了（%s）\n' "$n" "$(git rev-parse --short=9 HEAD)"
  else
    printf '  ✗ #%s **合并冲突** —— 这一批不能一起合。冲突文件：\n' "$n"
    git diff --name-only --diff-filter=U 2>/dev/null | sed 's/^/       /'
    git merge --abort >/dev/null 2>&1
    merge_ok=0
  fi
done

if [ "$merge_ok" -eq 0 ]; then
  printf '\n有 PR 合不进来 —— 先解决它，别往下跑（跑出来的结论对不上这一批）。\n'
  exit 1
fi

# --- 在这棵合起来的树上跑 ---------------------------------------------------
printf '\n=== 在这棵合起来的树上跑本地 CI ===\n'
printf '（--base %s --no-post：临时分支的 SHA 不是任何 PR 的 head，给它发 status 没意义）\n\n' "$BASE"
bash scripts/local-ci.sh --base "$BASE" --no-post
rc=$?

printf '\n=== 结论 ===\n'
case "$rc" in
  0) printf '全绿 —— 这一批**可以**按顺序合。\n' ;;
  1) printf '有红 —— 这一批**不能合**。各自绿、合起来红就是长这样。\n' ;;
  2) printf '环境未就绪 —— **不是代码问题**。按上面的提示把依赖/工具链弄好再跑；这一批的结论目前**未知**。\n' ;;
  3) printf '脚本自身出错 —— 看上面的输出，修脚本，不是这批 PR 的问题。\n' ;;
  *) printf 'CI 报了个没见过的退出码 %s。\n' "$rc" ;;
esac
exit "$rc"
