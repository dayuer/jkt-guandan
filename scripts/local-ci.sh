#!/usr/bin/env bash
# 本地 CI：按改动范围跑检查，把结论发成 commit status。
#
#   scripts/local-ci.sh [--base <ref>] [--no-post] [--all]
#
#   --base <ref>  比较的基线，默认 origin/main
#   --no-post     只跑不发 status（开 PR 之前自检用）。没有开的 PR 时跳过协议三项。
#   --all         不看改动范围，全部检查项都跑（等于原来 GitHub CI 的全量）
#
# 退出码：全绿 0；有红(代码) 1；只是环境未就绪 2；只是脚本自身出错 3；参数用错 64(EX_USAGE)。
#   （2 与 3 在 PR 上都发 `error`，区别只在描述里怎么说 —— 看见 2 去装依赖，
#     看见 3 去修脚本。）
#
#   ⚠️ 下面 --help 打印的是**行号范围**，改这段头注释时同步 `sed -n '2,Np'` 的 N。
#
# 为什么本地跑而不是 GitHub Actions：所有者 2026-10-15 决定不再使用 GitHub 的 CI（#157）。
# 机制移植自 dayuer/superapp 的 scripts/local-ci.sh；协议见 docs/agents/COORDINATION.md §4。
#
# 三处别改掉：
#  1) 跑之前先把**所有**要跑的 context 发成 pending。中途崩掉时 GitHub 上停在
#     pending；若只发最终态，先绿的那条会让整体显示 success —— 那是假绿。
#  2) 没被改动范围覆盖的检查项**不发 status**。聚合状态只看已发的，
#     所以「protocol 绿 + 无其它 status」= 适用范围内全绿，语义正确。
#  3) 推新代码后新 SHA 上没有任何 status，聚合自动回 pending —— 这是
#     statuses API 自带的强制重跑，协议不需要再约束。
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=local-ci-lib.sh
. "$ROOT/scripts/local-ci-lib.sh"
cd "$ROOT" || exit 2

BASE='origin/main'
POST=1
ALL=0
while [ $# -gt 0 ]; do
  case "$1" in
    --base)     BASE="${2:?--base 需要一个 ref}"; shift 2 ;;
    --no-post)  POST=0; shift ;;
    --all)      ALL=1; shift ;;
    -h|--help)  sed -n '2,13p' "$0"; exit 0 ;;
    *)          printf '未知参数：%s\n' "$1" >&2; exit 64 ;;
  esac
done

# --- 检查与上报的目标：**本地 HEAD**，不是 PR 的 headRefOid -----------------
# `--no-post` 的用途是「推送前自检」，那时本地必然领先远端。取 PR head 得到的是
# **远端已推送的状态** —— 未推的改动不在范围里，会**静默少跑检查项**。
# 有意保留的副作用：不 push 就带上报跑，会往一个远端不存在的 SHA 发 status，
# gh api 会报错、post_status 会把失败打出来 —— 不会静默。
HEAD_SHA=$(git rev-parse HEAD)
export LOCAL_CI_SHA="$HEAD_SHA"

# --- 工作区必须干净，结论才算数 ---------------------------------------------
# status 是发给**某个 SHA** 的，而各检查跑的是**工作区**。带 --post 时工作区不干净直接拒；
# --no-post 只警告（那个用法本来就常在有改动的树上跑）。
DIRTY=$(git status --porcelain)
ACTION=$(dirty_tree_action "$DIRTY" "$POST")
if [ "$ACTION" = refuse ]; then
  printf '工作区不干净，拒绝上报。\n' >&2
  printf 'status 是发给 head SHA 的，而检查跑的是工作区 —— 带着未提交改动上报，\n' >&2
  printf '会给一个「与被测内容不符」的 SHA 挂状态。先提交/清理，或改用 --no-post。\n' >&2
  printf '当前改动：\n%s\n' "$DIRTY" >&2
  exit 64
elif [ "$ACTION" = warn ]; then
  printf '⚠️ 工作区不干净 —— 下面的结论描述的是**工作区**，不是 %s 这个提交。\n' "$HEAD_SHA" >&2
fi

# --- 取 PR：只为拿 number/title/body（没有 PR 不致命）-----------------------
PR_JSON=''
PR_NUM=''
if command -v gh >/dev/null 2>&1; then
  GH_ERR=$(mktemp)
  if PR_JSON=$(gh pr view --json number,title,body 2>"$GH_ERR"); then
    PR_NUM=$(printf '%s' "$PR_JSON" | jq -r .number)
  elif grep -qi 'no pull requests found\|no open pull requests\|not on any branch' "$GH_ERR"; then
    PR_JSON=''                    # gh 明说没有 PR —— 这才是「真的没有」
  else
    # **瞬时失败 ≠ 没有 PR。** 一律当「没有 PR」的话，带上报时会发出一条假红的
    # `protocol error`。所以要认人话，其余一律报错退出。
    printf '取 PR 失败（可能是网络或瞬时错误，别当成「没有 PR」）：\n' >&2
    sed 's/^/  /' "$GH_ERR" >&2
    rm -f "$GH_ERR"
    exit 2
  fi
  rm -f "$GH_ERR"
fi

# --- 算改动范围 -------------------------------------------------------------
# `-c core.quotePath=false`：默认会把非 ASCII 路径转义加引号，scope_contexts 匹配不上
# 就会**静默漏检**。
if ! CHANGED=$(git -c core.quotePath=false diff --name-only "$BASE...$HEAD_SHA" 2>/dev/null); then
  printf '取不到 %s...%s 的差异；--base 写对了吗？（远端基线要先 git fetch）\n' "$BASE" "$HEAD_SHA" >&2
  exit 64
fi
if [ "$ALL" -eq 1 ]; then
  # shellcheck disable=SC2086  # 有意按空格拆成多行
  CONTEXTS=$(printf '%s\n' $CONTEXT_ORDER)
else
  CONTEXTS=$(printf '%s\n' "$CHANGED" | scope_contexts)
fi

if [ -n "$PR_NUM" ]; then
  printf '基线 %s · head %s · PR #%s\n' "$BASE" "${HEAD_SHA:0:9}" "$PR_NUM"
else
  printf '基线 %s · head %s\n' "$BASE" "${HEAD_SHA:0:9}"
fi
if [ -n "$CONTEXTS" ]; then
  printf '要跑：protocol + %s\n' "$(printf '%s' "$CONTEXTS" | tr '\n' ' ')"
else
  printf '要跑：protocol\n'
fi

# --- 上报 -------------------------------------------------------------------
post_status() { # $1=sha $2=context $3=state $4=描述
  [ "$POST" -eq 1 ] || return 0
  gh api -X POST "repos/{owner}/{repo}/statuses/$1" \
    -f state="$3" -f context="$2" -f description="$4" >/dev/null 2>&1 || {
      printf '上报 status 失败：%s=%s\n' "$2" "$3" >&2; }
}

# 先全部置 pending，防止中途崩掉留下假绿。
# 注意是 '%s\n' 不是 '%s'：`$()` 会吃掉结尾换行，不带这个 \n 时 read 在 EOF
# 返回非 0 → **最后一行被丢掉** → 那个 context 没有 pending。
post_status "$HEAD_SHA" protocol pending '运行中'
printf '%s\n' "$CONTEXTS" | while IFS= read -r c; do
  [ -n "$c" ] && post_status "$HEAD_SHA" "$c" pending '运行中'
done

FAILED=''
ENVNOTREADY=''
SCRIPTBUG=''

# --- protocol 三项 + 脚本卫生 -----------------------------------------------
printf '\n=== protocol ===\n'
if [ -z "$PR_JSON" ]; then
  printf '当前分支没有开的 PR，跳过协议检查（--no-post 时属正常）。\n'
  [ "$POST" -eq 1 ] && post_status "$HEAD_SHA" protocol error '没有找到本分支的 PR'
else
  TITLE=$(printf '%s' "$PR_JSON" | jq -r .title)
  BODY=$(printf '%s' "$PR_JSON" | jq -r .body)
  # 范围用 merge-base 而不是 `$BASE..$HEAD`：脚本不 fetch，本地基线一陈旧就可能把
  # 别人的提交算进这个 PR。
  MB=$(git merge-base "$BASE" "$HEAD_SHA" 2>/dev/null) || MB="$BASE"
  # **`%h %B` 不是 `%h %s`**：署名尾注在提交正文里，只取主题行会漏掉。
  COMMITS=$(git log --format='%h %B' "$MB..$HEAD_SHA" 2>/dev/null || true)
  AUTHORS=$(git log --format='%an <%ae>%n%cn <%ce>' "$MB..$HEAD_SHA" 2>/dev/null || true)
  # 第 ⑤ 个参数是第 ④ 项（脚本卫生）要扫的目录。传的是**工作区**的 scripts/，不是某个提交的。
  if OUT=$(protocol_failures "$TITLE" "$BODY" "$COMMITS" "$AUTHORS" "$ROOT/scripts"); then
    printf '通过。\n'
    post_status "$HEAD_SHA" protocol success '角色标签 / 无 AI 署名 / 关联 issue / scripts 无 $VAR 贴多字节字符'
  else
    [ -n "$OUT" ] || OUT='协议检查没有任何输出 —— 可能是脚本自身出错，看上面的报错'
    printf '%s\n' "$OUT"
    post_status "$HEAD_SHA" protocol failure '见终端输出'
    FAILED="$FAILED protocol"
  fi
fi

# --- 各检查项 ---------------------------------------------------------------
# run_context 的返回码：0 = 绿，2 = 环境未就绪，3 = 脚本自身出错，其余 = 代码红。
# 环境问题发 `error`（一样挡合并 —— 环境没配好时我们根本不知道代码好不好），
# 但描述写清楚，让人一眼看出「去装依赖」而不是去翻代码。
for ctx in $CONTEXTS; do
  printf '\n=== %s ===\n' "$ctx"
  printf '$ %s\n' "$(context_command "$ctx")"
  run_context "$ctx"; rc=$?
  if [ "$rc" -eq 0 ]; then
    printf '%s 通过。\n' "$ctx"
    post_status "$HEAD_SHA" "$ctx" success "$(context_command "$ctx" | cut -c1-135)"
  elif [ "$rc" -eq 2 ]; then
    printf '%s 环境未就绪（不是代码问题，看上面的说明）。\n' "$ctx"
    post_status "$HEAD_SHA" "$ctx" error "环境未就绪：$(context_command "$ctx" | cut -c1-100)"
    ENVNOTREADY="$ENVNOTREADY $ctx"
  elif [ "$rc" -eq 3 ]; then
    printf '%s 脚本自身出错（不是环境、也不是代码）。\n' "$ctx"
    post_status "$HEAD_SHA" "$ctx" error '脚本自身出错：未知 context 或与 context_command 漂移'
    SCRIPTBUG="$SCRIPTBUG $ctx"
  else
    printf '%s 失败。\n' "$ctx"
    post_status "$HEAD_SHA" "$ctx" failure '见终端输出'
    FAILED="$FAILED $ctx"
  fi
done

# --- 汇总 -------------------------------------------------------------------
printf '\n'
if [ -z "$FAILED$ENVNOTREADY$SCRIPTBUG" ]; then
  printf '全绿。\n'
  exit 0
fi
[ -n "$FAILED" ]      && printf '红（代码）：%s\n' "$FAILED"
[ -n "$ENVNOTREADY" ] && printf '环境未就绪：%s\n' "$ENVNOTREADY"
[ -n "$SCRIPTBUG" ]   && printf '脚本自身出错：%s\n' "$SCRIPTBUG"
[ -n "$FAILED" ] && exit 1
[ -n "$ENVNOTREADY" ] && exit 2
exit 3
