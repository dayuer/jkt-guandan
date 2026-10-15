#!/usr/bin/env bash
# 本地 CI 的纯逻辑：改动范围 → 检查项、协议三项文本校验。
#
# 本文件只定义函数，不产生任何副作用（不联网、不发 status、不 cd），
# 以便 scripts/test-local-ci.sh 直接 source 后逐条断言。
# 编排与上报在 scripts/local-ci.sh，合并前批跑在 scripts/pre-merge-check.sh。
# 说明见 docs/agents/COORDINATION.md §4「本地 CI」。
#
# 移植自 dayuer/superapp 的 scripts/local-ci-lib.sh（2026-10-15，#157）：
# 编排、上报、协议三项与 approve 校验照搬；检查项换成本仓原 .github/workflows/ci.yml 的步骤。

# ---------------------------------------------------------------------------
# 范围映射
# ---------------------------------------------------------------------------

# ⚠️ 本机 /bin/bash 是 3.2.57（macOS 自带，`env bash` 也指向它）。
#
# 坑①：**任何 `case … esac` 都别写进 `$( … )`**。带偏括号计数的是 `case` 模式
# 收尾那个 `)`（`foo)`），报出来的 token 随上下文变。所以判定抽成 _path_to_context，
# 再在子壳里调用它 —— 抽成函数后调用是安全的（superapp 实测）。
#
# 坑②：bash 3.2 缺一批 4.x 特性：`mapfile`/`readarray`、`${x,,}`、`declare -A`、`&>>`。

# 一条路径 → 零到多个 context（每行一个）。与原 ci.yml 的对应关系：
#
#   local-ci/test     go vet + 前端构建 + go test -race + 前端测试（= make test）
#   local-ci/vuln     govulncheck
#   local-ci/h5-audit npm audit --omit=dev（只在依赖清单变了时跑：通报每天在变，
#                     与改动无关的红不该挂在别人的 PR 上）
#   local-ci/minigame 小游戏 typecheck + test + build。小游戏复用 app/h5/src 的纯逻辑，
#                     所以 app/h5/src 改了也要跑（原 ci.yml 里那条注释说的就是这个）
#   local-ci/nginx    nginx 模板检查
#   local-ci/docker   docker build + 容器冒烟 + 发布回滚测试 + 恢复演练
#   local-ci/e2e      浏览器端到端冒烟（= make e2e）
#   local-ci/meta     这套 CI 自己的自测 —— 否则它永远不会被它服务的那个 CI 跑到
#
# **有意不覆盖**：docs/、design/、tools/（ios-cookie-test 是真机手工验证页）、
# .claude/、.github/ 模板、README 等 —— 只跑 protocol。这是边界，不是遗漏。
_path_to_context() {
  case "$1" in
    go.mod|go.sum)
      echo 'local-ci/test'; echo 'local-ci/vuln'; echo 'local-ci/docker'; echo 'local-ci/e2e' ;;
    server/*)
      echo 'local-ci/test'; echo 'local-ci/vuln'; echo 'local-ci/docker'; echo 'local-ci/e2e' ;;
    # ⚠️ 下面三条要排在 `app/h5/*` 前面：case 取**第一个**命中的分支。
    app/h5/package.json|app/h5/package-lock.json)
      echo 'local-ci/test'; echo 'local-ci/h5-audit'; echo 'local-ci/e2e' ;;
    app/h5/src/*)
      echo 'local-ci/test'; echo 'local-ci/minigame'; echo 'local-ci/e2e' ;;
    app/h5/*)
      echo 'local-ci/test'; echo 'local-ci/e2e' ;;
    app/minigame/*)
      echo 'local-ci/minigame' ;;
    .nvmrc)
      echo 'local-ci/test'; echo 'local-ci/minigame'; echo 'local-ci/e2e' ;;
    Makefile)
      echo 'local-ci/test'; echo 'local-ci/e2e' ;;
    deploy/nginx/*|scripts/check-nginx.sh)
      echo 'local-ci/nginx' ;;
    Dockerfile|.dockerignore|deploy/*)
      echo 'local-ci/docker' ;;
    scripts/deploy.sh|scripts/deploy-remote.sh|scripts/test-deploy.sh|scripts/restore-drill.sh|scripts/container-smoke.sh)
      echo 'local-ci/docker' ;;
    scripts/e2e-server.sh)
      echo 'local-ci/e2e' ;;
    scripts/local-ci.sh|scripts/local-ci-lib.sh|scripts/test-local-ci.sh|scripts/pre-merge-check.sh)
      echo 'local-ci/meta' ;;
  esac
}

# 从 stdin 读改动路径（每行一个），输出要跑的 context（去重，按 CONTEXT_ORDER 排）。
# 没有任何命中时输出空串（不是空行）。
#
# 对调用方的两个前提（lib 管不了上游怎么生成这份列表）：
#  ① **路径不得被引号包裹**。git 默认 `core.quotePath=true`，会把非 ASCII 路径
#     写成带引号的转义形式 → 匹配不上 → **静默漏检**。调用侧用
#     `git -c core.quotePath=false diff --name-only …` 生成。这是个中文仓库。
#  ② 路径须是仓库根相对形态（`./x`、裸目录名 `server` 都不命中）。
#
# 末行不带换行也能读到（`|| [ -n "$p" ]` 兜住 read 在 EOF 的返回值）。
scope_contexts() {
  local hits c out=''
  hits=$(
    while IFS= read -r p || [ -n "$p" ]; do
      [ -n "$p" ] || continue
      _path_to_context "$p"
    done | sort -u
  )
  # 按固定顺序输出：便宜的先跑、先红先知道；docker / e2e 最慢放最后。
  for c in $CONTEXT_ORDER; do
    if printf '%s\n' "$hits" | grep -qxF "$c"; then
      out="$out$c
"
    fi
  done
  printf '%s' "${out%
}"
}

CONTEXT_ORDER='local-ci/meta local-ci/nginx local-ci/test local-ci/h5-audit local-ci/minigame local-ci/vuln local-ci/docker local-ci/e2e'

# context → 跑什么（一行，供打印、status 描述与自测断言）
# 未知 context 返回 1。注意 **run_context 的未知返回码有意不同（3）**。
# 自测里有一条断言把 1 钉死了（精确等于 1 —— 只判"非 0"会把 127 当成通过）。
context_command() {
  case "${1:-}" in
    local-ci/test)     echo 'make test  # go vet + 前端构建 + go test -race + 前端测试' ;;
    local-ci/vuln)     echo 'make vuln  # govulncheck' ;;
    local-ci/h5-audit) echo 'cd app/h5 && npm audit --omit=dev' ;;
    local-ci/minigame) echo 'cd app/minigame && npm ci && npm run typecheck && npm test && npm run build' ;;
    local-ci/nginx)    echo 'scripts/check-nginx.sh deploy/nginx/guandan.synon.ai.conf' ;;
    local-ci/docker)   echo 'docker build + scripts/container-smoke.sh + scripts/test-deploy.sh + scripts/restore-drill.sh' ;;
    local-ci/e2e)      echo 'make e2e  # Playwright + 无头 Chromium' ;;
    local-ci/meta)     echo 'bash scripts/test-local-ci.sh' ;;
    *) return 1 ;;
  esac
}

# ---------------------------------------------------------------------------
# 环境前置检查：「环境没就绪」与「代码红」必须分开
# ---------------------------------------------------------------------------
# 编排器把返回 2 发成 `error`、其余非 0 发成 `failure`。不先拦一下的话，没装 docker /
# 没装浏览器会被记成 `failure`，和「代码坏了」在 PR 上长得一模一样，人会去翻代码。

# 这个 context 要哪些命令行工具（空格分隔）。
context_tools() {
  case "${1:-}" in
    local-ci/test)     echo 'make go npm' ;;
    local-ci/vuln)     echo 'make go' ;;
    local-ci/h5-audit) echo 'npm' ;;
    local-ci/minigame) echo 'npm' ;;
    local-ci/nginx)    echo 'awk' ;;
    local-ci/docker)   echo 'docker curl python3' ;;
    local-ci/e2e)      echo 'make go npm npx' ;;
    *) : ;;
  esac
}

# 这个 context 会在哪些目录里 `npm ci`（相对仓库根）。回显空 = 不碰 node 依赖。
node_dirs_for_context() {
  case "${1:-}" in
    local-ci/test|local-ci/e2e|local-ci/h5-audit) echo app/h5 ;;
    local-ci/minigame) echo app/minigame ;;
    *) : ;;
  esac
}

# 依赖目录的处置。参数：$1 = node_modules 是不是软链（1/0）
#   软链 → unreachable；否则 ok（没装也行：make / minigame 那一支自己会 `npm ci`）
#
# **软链整个 node_modules 是错的**（superapp #425 实测）：`npm ci` 会先删 node_modules，
# 软链时删的是**另一棵树**的依赖；不删的话跑出来的结论也与当前提交无关。
node_modules_action() {
  [ "${1:-0}" -eq 1 ] && { echo unreachable; return 0; }
  echo ok
}

require_no_symlinked_node_modules() { # $1 = 相对仓库根的目录
  [ -n "${1:-}" ] || return 0
  local nm="$1/node_modules" islink=0
  [ -L "$nm" ] && islink=1
  case "$(node_modules_action "$islink")" in
    ok) return 0 ;;
    unreachable)
      printf '%s/node_modules 是**软链** —— npm ci 会删掉另一棵树的依赖，结论也与当前提交无关。\n' "$1" >&2
      printf '  **不是代码问题。** 删掉这个软链（rm %s，只删链接本身）再跑，脚本会自己 npm ci。\n' "$nm" >&2
      return 2 ;;
  esac
}

# 工具缺失 → 返回 2 并说清楚装什么。$1 = 空格分隔的工具名
require_tools() {
  local t missing=''
  for t in ${1:-}; do
    command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
  done
  [ -z "$missing" ] && return 0
  printf '缺少命令：%s —— 装好再跑。**这不是代码问题。**\n' "$missing" >&2
  return 2
}

# docker 命令在、但守护进程没起来，也是环境问题。
require_docker_daemon() {
  docker info >/dev/null 2>&1 && return 0
  printf 'docker 守护进程连不上（Docker Desktop / colima 没开？）。**这不是代码问题。**\n' >&2
  return 2
}

# Playwright 的 Chromium 装在用户缓存里，不在 node_modules 里：npm ci 装不上它。
# 没装时 Playwright 会报一长串「Executable doesn't exist」，看着像测试挂了。
playwright_browsers_dir() {
  if [ -n "${PLAYWRIGHT_BROWSERS_PATH:-}" ]; then printf '%s' "$PLAYWRIGHT_BROWSERS_PATH"
  elif [ "$(uname)" = Darwin ]; then printf '%s' "$HOME/Library/Caches/ms-playwright"
  else printf '%s' "$HOME/.cache/ms-playwright"
  fi
}

require_playwright_chromium() { # $1 = 浏览器目录（可注入，便于测试）
  local dir="${1:-$(playwright_browsers_dir)}" d
  for d in "$dir"/chromium*; do
    [ -d "$d" ] && return 0
  done
  printf 'Playwright 的 Chromium 没装（找的是 %s/chromium*）。**这不是代码问题。**\n' "$dir" >&2
  printf '  先装一次：cd app/h5 && npx playwright install chromium\n' >&2
  return 2
}

# 一个 context 跑之前的全部前置检查。0 = 就绪，2 = 环境未就绪。
context_ready() {
  local ctx="${1:-}" d
  require_tools "$(context_tools "$ctx")" || return 2
  for d in $(node_dirs_for_context "$ctx"); do
    require_no_symlinked_node_modules "$d" || return 2
  done
  case "$ctx" in
    local-ci/docker) require_docker_daemon || return 2 ;;
    local-ci/e2e)    require_playwright_chromium || return 2 ;;
  esac
  return 0
}

# ---------------------------------------------------------------------------
# 真正执行；cwd 必须是仓库根。返回 0 = 绿，1 = 代码红，2 = 环境未就绪，3 = 脚本自身出错。
# ---------------------------------------------------------------------------
# 为什么不自己锚定 cwd：`context_command` 打印的命令就是给人看的「跑了什么」，
# 在这里偷偷 `cd` 会让打印与实际执行产生偏差。出厂调用方 local-ci.sh 开头就 `cd "$ROOT"`。
#
# ⚠️ **不能被 `set -e` 的调用方调用**：下面有 `cmd; rc=$?` 形状，在 `-e` 下会直接中止。
#
# 各支一律 `|| return 1`：把子命令的退出码**归一成 1**。不这么做的话，哪天某个工具
# 用 2 表示测试失败，代码坏了就会被发成 `error`（环境未就绪）。**只有显式的前置检查才返回 2。**
#
# LOCAL_CI_SHA：被测提交，docker 那支要拿它当构建号（容器冒烟要核 X-App-Build 头）。
run_context() {
  local ctx="${1:-}"
  case "$ctx" in
    local-ci/test|local-ci/vuln|local-ci/h5-audit|local-ci/minigame|local-ci/nginx|local-ci/docker|local-ci/e2e|local-ci/meta)
      context_ready "$ctx" || return 2 ;;
  esac
  case "$ctx" in
    local-ci/test)     make test || return 1 ;;
    local-ci/vuln)     make vuln || return 1 ;;
    local-ci/h5-audit)
      # audit 只读 package-lock.json，不需要先装依赖。
      ( cd app/h5 && npm audit --omit=dev ) || return 1 ;;
    local-ci/minigame)
      ( cd app/minigame && npm ci && npm run typecheck && npm test && npm run build ) || return 1 ;;
    local-ci/nginx)    scripts/check-nginx.sh deploy/nginx/guandan.synon.ai.conf || return 1 ;;
    local-ci/docker)
      local sha="${LOCAL_CI_SHA:-$(git rev-parse HEAD)}" image rc
      # 镜像名带上进程号：同一台机器上几个会话同时跑，别互相覆盖同一个 tag。
      image="guandan:local-ci-$$"
      if ! docker build --build-arg "BUILD=$sha" -t "$image" .; then
        docker rmi "$image" >/dev/null 2>&1
        return 1
      fi
      scripts/container-smoke.sh "$image" "$sha" \
        && scripts/test-deploy.sh "$image" \
        && scripts/restore-drill.sh "$image"
      rc=$?
      docker rmi "$image" >/dev/null 2>&1
      [ "$rc" -eq 0 ] || return 1
      ;;
    local-ci/e2e)      make e2e || return 1 ;;
    local-ci/meta)     bash scripts/test-local-ci.sh || return 1 ;;
    *)
      # 与 context_command 漂移的告警。两份 case 必须同步 —— 漂移的后果是
      # 「一条绿状态，正文广告着从未执行过的命令」。
      # ⚠️ 用 `$ctx` 不用裸 `$1`：零参时裸 `$1` 在 `set -u` 下会炸成 127。
      if context_command "$ctx" >/dev/null 2>&1; then
        printf 'run_context 不认识已知 context（与 context_command 漂移）：%s\n' "$ctx" >&2
        printf '  这是**脚本自身的 bug**，不是环境问题 —— 两份 case 必须同步。\n' >&2
      else
        printf '未知 context：%s\n' "$ctx" >&2
        printf '  这是**脚本自身的 bug**（传进来的名称不在 CONTEXT_ORDER 里）。\n' >&2
      fi
      # 返回 **3**，不是 2：2 已经被「环境未就绪」占用了。
      return 3 ;;
  esac
}

# 工作区不干净时「能不能上报」的判定。抽成纯函数是为了**能测**。
# 参数：$1 = `git status --porcelain` 的输出（空 = 干净），$2 = 是否要上报（1/0）
# 回显：ok（干净）| warn（脏但不报，只警告）| refuse（脏且要报，必须拒）
#
# 它防的是最坏的一种假绿：提交了必红的代码 → 把工作区改回好的、**但不提交** →
# HEAD 还是那个坏提交 → 跑出来全绿 → **一个必红的 SHA 上挂了 success**。
dirty_tree_action() {
  [ -z "$1" ] && { echo ok; return 0; }
  if [ "${2:-0}" -eq 1 ]; then echo refuse; else echo warn; fi
}

# ---------------------------------------------------------------------------
# 协议三项（迁移自 .github/workflows/protocol.yml）
# 第 ③ 项**去掉了「仅 impl/* 与 audit/*」的前置条件**：COORDINATION.md §4 的
# 「PR 描述写清关联的 issue」本来就不限分支前缀。
# ---------------------------------------------------------------------------

# 扫「不得含署名」之前先剥掉**围栏代码块**。
#
# 为什么必须剥：§4 要求 PR 描述里**贴出**本地测试输出，而贴出来的内容可能逐字含
# 这些模式 —— 一个给「署名检查」本身写测试的 PR，会同时满足不了 §4 和 ②。
# 判据：围栏块 = 引用，围栏外 = 作者自己写的。**只用于 ②**：①③ 是「必须含」检查。
_strip_code_fences() {
  sed '/^[[:space:]]*```/,/^[[:space:]]*```/d'
}

# AI 助手署名的**形态**（trailer、"generated with/by" 尾注、带尖括号的机器人邮箱），
# 不是裸名字 —— 提到仓库里的 CLAUDE.md 这类文件名不该被拦（#28）。
# **邮箱那一支必须带尖括号**：trailer 渲染出来就是 `<邮箱>`，而散文里提到裸邮箱是引用。
# 最后一支带 `\b`（照原 protocol.yml）：superapp 那版没有，`generated with details`
# 里的 ai 会被拦。作者 / 提交者身份另外按裸名字查（见 protocol_failures）。
PROTO_ATTR_RE='co-authored-by:.*(claude|anthropic)|<noreply@anthropic\.com>|generated (with|by) .*\b(claude|anthropic|ai|assistant)\b'

# 参数：$1=标题 $2=描述 $3=提交信息 $4=作者与提交者身份
# 通过：无输出、返回 0。失败：每条问题打到 stdout、返回 1（不提前返回，一次列全）。
#
# ⚠️ **$3 必须是含正文的完整提交信息**（`git log --format='%h %B'`）。只给主题行会漏掉
# 正文里的署名尾注 —— 而那正是最常见的违规形态。
protocol_failures() {
  local title="$1" body="$2" commits="$3" authors="$4"
  local fail=0 first

  # ① 描述第一行（跳过空行与 HTML 注释）必须是角色标签
  first=$(printf '%s\n' "$body" | sed -e 's/\r$//' \
            | grep -v -e '^[[:space:]]*$' -e '^[[:space:]]*<!--' | head -n 1)
  if ! printf '%s' "$first" | grep -Eq '^\[(coord|impl|auditor|design)\]'; then
    printf 'PR 描述第一行须为角色标签 [coord]/[impl]/[auditor]/[design]，当前是: %s\n' "${first:-<空>}"
    fail=1
  fi

  # ② 不得有 AI 助手署名：标题、描述（先剥围栏块）、每一条提交信息、作者/提交者身份
  local scan_body
  scan_body=$(printf '%s\n' "$body" | _strip_code_fences)
  if printf '%s\n%s\n' "$title" "$scan_body" | grep -Eiq "$PROTO_ATTR_RE"; then
    printf 'PR 标题或描述含 AI 助手署名（见 COORDINATION.md §1）\n'
    fail=1
  fi
  if printf '%s' "$commits" | grep -Eiq "$PROTO_ATTR_RE"; then
    printf '以下提交的信息含 AI 助手署名：\n%s\n' "$commits"
    fail=1
  fi
  if printf '%s' "$authors" | grep -Eiq 'claude|anthropic'; then
    printf '作者或提交者含 AI 助手身份：%s\n' "$authors"
    fail=1
  fi

  # ③ 关联 issue —— 对所有 PR 生效。冒号可有可无（GitHub 支持 `Fixes: #10`）。
  if ! printf '%s' "$body" | grep -Eiq '(fixes|closes|resolves|refs):?[[:space:]]*#[0-9]+'; then
    printf 'PR 描述必须关联 issue（Fixes #N / Fixes: #N / Refs #N 均可）\n'
    fail=1
  fi

  [ "$fail" -eq 0 ]
}

# ---------------------------------------------------------------------------
# §4 合并条件 1：最近一次 `REVIEW: approve` 的 sha 必须等于当前 head
# ---------------------------------------------------------------------------
# scripts/pre-merge-check.sh 要用。判据错一个字符，就会把「复审后又推了」的 PR 当成已复审过。

# 从一段（按时间正序拼起来的）评论文本里取**最后一次** `REVIEW: approve sha=…` 的 sha。
last_approve_sha() {
  printf '%s\n' "${1:-}" \
    | grep -oE 'REVIEW: approve sha=[0-9a-f]{7,40}' \
    | tail -1 \
    | sed 's/.*sha=//'
}

# approve 与 head 的关系：ok 对得上（按前缀比）/ stale 复审后又推了 / none 没有 approve / nohead 没拿到 head
approve_vs_head() { # $1 = approve 里写的 sha，$2 = head
  local a="${1:-}" h="${2:-}"
  [ -n "$a" ] || { echo none; return 0; }
  [ -n "$h" ] || { echo nohead; return 0; }
  case "$h" in "$a"*) echo ok ;; *) echo stale ;; esac
}
