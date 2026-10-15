#!/usr/bin/env bash
# scripts/local-ci-lib.sh 的自测。只读、不联网、不改仓库。
#   bash scripts/test-local-ci.sh     -> 全过打印 OK 并 exit 0；有断言失败列出并 exit 1；
#                                        lib 缺失（source 守卫拦下）exit 2
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# source 守卫：lib 缺失时若直接往下跑，期望值为空串的断言会照打 ok（假绿）。
# 注意**不要**改成 `set -e` —— 那会让 `out=$(…); rc=$?` 形状直接炸掉。
. "$ROOT/scripts/local-ci-lib.sh" || { printf 'FATAL: 无法 source local-ci-lib.sh\n' >&2; exit 2; }

fails=0
ok() { printf '  ok   %s\n' "$1"; }
# ⚠️ 本文件会拼中文进断言消息。**参数展开后面紧跟多字节字符时必须加花括号** ——
# 写 `"${rc}，"` 而不是 `"$rc，"`：bash 3.2 在 UTF-8 locale 下会把那个全角逗号
# 当成变量名的一部分，报 `rc，: unbound variable`。
bad() { printf '  FAIL %s\n     期望: %s\n     实际: %s\n' "$1" "$2" "$3"; fails=$((fails+1)); }

eq() { # $1=名字 $2=期望 $3=实际
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "$2" "$3"; fi
}

lines() { printf '%s\n' "$@"; }   # 拼多行期望值（$() 会吃掉结尾换行，与 scope_contexts 的输出对齐）

echo '--- scope_contexts ---'

eq '文档、设计稿、tools 不命中任何检查项（只跑 protocol）' \
  '' \
  "$(printf 'docs/server-design.md\ndesign/guandan-h5/README.md\ntools/ios-cookie-test/index.html\nREADME.md\nAGENTS.md\n' | scope_contexts)"

eq '后端改动：test + vuln + docker + e2e' \
  "$(lines local-ci/test local-ci/vuln local-ci/docker local-ci/e2e)" \
  "$(printf 'server/internal/httpapi/x.go\n' | scope_contexts)"

eq 'go.mod 与后端同等对待' \
  "$(lines local-ci/test local-ci/vuln local-ci/docker local-ci/e2e)" \
  "$(printf 'go.sum\n' | scope_contexts)"

# 计分规则的对照用例两端共用（#174）：服务端规则或 vectors.json 改了，小游戏的 vitest 也要跑。
eq '计分规则改动也要跑小游戏' \
  "$(lines local-ci/test local-ci/minigame local-ci/vuln local-ci/docker local-ci/e2e)" \
  "$(printf 'server/internal/scoring/testdata/vectors.json\nserver/internal/scoring/scoring.go\n' | scope_contexts)"

# 小游戏复用 app/h5/src 的纯逻辑（原 ci.yml 的注释）：H5 改了接口类型，小游戏要先报出来。
eq 'app/h5/src 改动也要跑小游戏' \
  "$(lines local-ci/test local-ci/minigame local-ci/e2e)" \
  "$(printf 'app/h5/src/api.ts\n' | scope_contexts)"

eq 'app/h5 的非 src 改动不跑小游戏' \
  "$(lines local-ci/test local-ci/e2e)" \
  "$(printf 'app/h5/e2e/smoke.e2e.ts\napp/h5/vite.config.ts\n' | scope_contexts)"

# 映射顺序守卫：case 取第一个命中的分支，package.json 那条必须排在 app/h5/* 之前，
# 写反了依赖改动就**永远不跑 npm audit**，而且没有任何报错。
eq 'H5 依赖清单改动才跑 npm audit' \
  "$(lines local-ci/test local-ci/h5-audit local-ci/e2e)" \
  "$(printf 'app/h5/package-lock.json\n' | scope_contexts)"

eq '小游戏自己的改动只跑小游戏' \
  'local-ci/minigame' \
  "$(printf 'app/minigame/src/main.ts\napp/minigame/package.json\n' | scope_contexts)"

eq 'nginx 模板只跑 nginx 检查' \
  'local-ci/nginx' \
  "$(printf 'deploy/nginx/guandan.synon.ai.conf\n' | scope_contexts)"

eq 'Dockerfile 与部署脚本跑 docker' \
  'local-ci/docker' \
  "$(printf 'Dockerfile\nscripts/deploy-remote.sh\nscripts/container-smoke.sh\n' | scope_contexts)"

eq 'scripts/agents 不命中（协作脚本，不在 CI 范围）' \
  '' \
  "$(printf 'scripts/agents/board.sh\n' | scope_contexts)"

eq '本地 CI 自己的脚本命中 local-ci/meta（这套 CI 要覆盖自己）' \
  'local-ci/meta' \
  "$(printf 'scripts/local-ci.sh\nscripts/test-local-ci.sh\n' | scope_contexts)"

eq '多命中：去重，并按 CONTEXT_ORDER 排（便宜的先跑）' \
  "$(lines local-ci/meta local-ci/nginx local-ci/test local-ci/h5-audit local-ci/minigame local-ci/vuln local-ci/docker local-ci/e2e)" \
  "$(printf 'server/a.go\napp/h5/package.json\napp/minigame/b.ts\ndeploy/nginx/x.conf\nscripts/local-ci-lib.sh\nserver/c.go\n' | scope_contexts)"

eq '别的前缀不误伤' \
  '' \
  "$(printf 'app/h5x/a.ts\nserverless/b.go\nxscripts/local-ci.sh\n' | scope_contexts)"

# 故意不带结尾换行：裸 `while read` 在 EOF 拿到不完整行会返回非 0 而丢掉它。
eq '末行不带换行的输入也要读到' \
  'local-ci/minigame' \
  "$(printf 'app/minigame/a.ts' | scope_contexts)"

# CONTEXT_ORDER 必须列全：漏一个的话 scope_contexts 会把它**静默丢掉**。
missing=''
for c in local-ci/test local-ci/vuln local-ci/h5-audit local-ci/minigame local-ci/nginx local-ci/docker local-ci/e2e local-ci/meta; do
  case " $CONTEXT_ORDER " in *" $c "*) : ;; *) missing="$missing $c" ;; esac
done
eq 'CONTEXT_ORDER 列全了每个 context' '' "$missing"

echo '--- context_command ---'

for c in $CONTEXT_ORDER; do
  if [ -n "$(context_command "$c")" ]; then ok "context_command 认识 $c"
  else bad "context_command 认识 $c" '非空' '空'; fi
done

eq 'test 就是 make test' 'make test  # go vet + 前端构建 + go test -race + 前端测试' "$(context_command local-ci/test)"
eq 'minigame 与原 ci.yml 一致' \
  'cd app/minigame && npm ci && npm run typecheck && npm test && npm run build' \
  "$(context_command local-ci/minigame)"
eq 'meta 跑 CI 自己的自测' 'bash scripts/test-local-ci.sh' "$(context_command local-ci/meta)"

# 断言**精确的**返回码 1：函数不存在时 shell 给的是 127，只判"非 0"会当成通过。
context_command local-ci/不存在 >/dev/null 2>&1; rc=$?
if [ "$rc" -eq 1 ]; then ok '未知 context 返回 1'
else bad '未知 context 返回 1' '1' "$rc"; fi

run_context local-ci/不存在 >/dev/null 2>&1; rc=$?
if [ "$rc" -eq 3 ]; then ok 'run_context 未知 context 返回 3（脚本自身出错，不是环境）'
else bad 'run_context 未知 context 返回 3（脚本自身出错，不是环境）' '3' "$rc"; fi

# 零参也要返回 3，而不是被 `set -u` 打死（127）。本文件头部就是 `set -uo pipefail`。
run_context >/dev/null 2>&1; rc=$?
if [ "$rc" -eq 3 ]; then ok 'run_context 零参也返回 3（不被 set -u 打死）'
else bad 'run_context 零参也返回 3（不被 set -u 打死）' '3' "$rc"; fi

echo '--- 工作区与上报 ---'

eq '工作区干净 → ok' 'ok' "$(dirty_tree_action '' 1)"
eq '脏 + 要上报 → refuse' 'refuse' "$(dirty_tree_action ' M scripts/local-ci.sh' 1)"
eq '脏 + 不上报 → warn（推送前自检常这样）' 'warn' "$(dirty_tree_action ' M scripts/local-ci.sh' 0)"

echo '--- 环境前置检查：环境问题返回 2，不是 1 ---'

eq 'test 要的 node 目录' 'app/h5' "$(node_dirs_for_context local-ci/test)"
eq 'e2e 要的 node 目录' 'app/h5' "$(node_dirs_for_context local-ci/e2e)"
eq 'minigame 要的 node 目录' 'app/minigame' "$(node_dirs_for_context local-ci/minigame)"
eq 'nginx 不是 node 侧' '' "$(node_dirs_for_context local-ci/nginx)"

eq 'node_modules 是软链 → unreachable' 'unreachable' "$(node_modules_action 1)"
eq 'node_modules 不是软链（装没装都行，会 npm ci）→ ok' 'ok' "$(node_modules_action 0)"

require_tools 'sh' >/dev/null 2>&1; rc=$?
eq '工具都在 → 0' '0' "$rc"
require_tools 'sh 一个不存在的命令-xyz' >/dev/null 2>&1; rc=$?
eq '缺工具 → 2（环境未就绪）' '2' "$rc"

TMPP=$(mktemp -d)
require_playwright_chromium "$TMPP" >/dev/null 2>&1; rc=$?
eq '没有 Playwright Chromium → 2' '2' "$rc"
mkdir -p "$TMPP/chromium-1248"
require_playwright_chromium "$TMPP" >/dev/null 2>&1; rc=$?
eq '有 Playwright Chromium → 0' '0' "$rc"
rm -rf "$TMPP"

TMPL=$(mktemp -d)
mkdir -p "$TMPL/real" "$TMPL/app"
ln -s "$TMPL/real" "$TMPL/app/node_modules"
require_no_symlinked_node_modules "$TMPL/app" >/dev/null 2>&1; rc=$?
eq 'node_modules 软链 → 2（npm ci 会删掉别的树的依赖）' '2' "$rc"
rm -rf "$TMPL"

echo '--- protocol_failures ---'

pass_case() { # $1=名字 $2=title $3=body $4=commits $5=authors
  local out rc
  out=$(protocol_failures "$2" "$3" "$4" "$5"); rc=$?
  if [ "$rc" -eq 0 ] && [ -z "$out" ]; then ok "$1"
  else bad "$1" '退出码 0 且无输出' "退出码 ${rc}，输出: $out"; fi
}

fail_case() { # $1=名字 $2=title $3=body $4=commits $5=authors $6=输出应含
  local out rc
  out=$(protocol_failures "$2" "$3" "$4" "$5"); rc=$?
  if [ "$rc" -eq 1 ] && printf '%s' "$out" | grep -qF "$6"; then ok "$1"
  else bad "$1" "退出码 1 且输出含「$6」" "退出码 ${rc}，输出: $out"; fi
}

GOOD_BODY='[impl]

Refs #115

改点东西。'

pass_case '合规 PR' '标题' "$GOOD_BODY" 'abc 改点东西' 'dayuer <dayuer@gmail.com>'

pass_case '描述第一行前有 HTML 注释也能取到角色标签' \
  '标题' "<!-- agent: impl-本机 -->
[auditor]

Fixes #7" 'abc x' 'dayuer <a@b.c>'

pass_case '[design] 也是合法角色标签' '标题' '[design]

Refs #1' 'abc x' 'dayuer <a@b.c>'

pass_case '提到 CLAUDE.md 这个文件名不算署名（#28）' \
  'docs: 改 CLAUDE.md' '[coord]

Refs #1

见 CLAUDE.md。' 'abc 改 CLAUDE.md' 'dayuer <a@b.c>'

fail_case '描述第一行不是角色标签' \
  '标题' '改点东西

Refs #1' 'abc x' 'dayuer <a@b.c>' '角色标签'

fail_case '[biz] 不是本仓的角色' '标题' '[biz]

Refs #1' 'abc x' 'dayuer <a@b.c>' '角色标签'

fail_case '缺 issue 关联（coord/ design/ 分支也查 —— 相对原 protocol.yml 的变更点）' \
  '标题' '[coord]

改点东西，没写 Refs。' 'abc x' 'dayuer <a@b.c>' '必须关联 issue'

pass_case 'Fixes: #N 带冒号也算关联' '标题' '[impl]

Fixes: #115' 'abc x' 'dayuer <a@b.c>'

out=$(protocol_failures 'docs: 改点东西 (Co-Authored-By: Claude <noreply@anthropic.com>)' '[impl]

改点东西，没写 Refs。' 'abc x' 'dayuer <a@b.c>'); rc=$?
if [ "$rc" -eq 1 ] \
   && printf '%s' "$out" | grep -qF 'AI 助手署名' \
   && printf '%s' "$out" | grep -qF '必须关联 issue'; then
  ok '同时命中两条：逐条列全，不提前返回'
else
  bad '同时命中两条：逐条列全，不提前返回' '退出码 1 且两条都出现' "退出码 ${rc}，输出: $out"
fi

fail_case '提交信息正文里的署名尾注也拦' \
  '标题' "$GOOD_BODY" 'abc x
Co-Authored-By: Claude <noreply@anthropic.com>' 'dayuer <a@b.c>' 'AI 助手署名'

fail_case '带尖括号的 anthropic 邮箱（trailer 形态）也拦' \
  '标题' '[impl]

Refs #1

Signed-off-by: X <noreply@anthropic.com>' 'abc x' 'dayuer <a@b.c>' 'AI 助手署名'

pass_case '散文里提到裸邮箱不算署名' \
  '标题' '[impl]

Refs #1

要粘贴的测试输出里那一行 noreply@anthropic.com 就是它。' 'abc x' 'dayuer <a@b.c>'

fail_case 'Generated with 这一支单独也拦' \
  '标题' '[impl]

Refs #1

generated with Claude Code' 'abc x' 'dayuer <a@b.c>' 'AI 助手署名'

pass_case 'generated with details 不误伤（\b 单词边界，照原 protocol.yml）' \
  '标题' '[impl]

Refs #1

The report is generated with details from the log.' 'abc x' 'dayuer <a@b.c>'

fail_case '作者或提交者身份含 claude' \
  '标题' "$GOOD_BODY" 'abc x' 'claude <noreply@example.com>' '作者或提交者'

FENCE=$(printf '\140\140\140')
pass_case '描述里围栏块中的模式算引用，不算署名' \
  '标题' "[impl]

Refs #1

${FENCE}bash
  ok   Co-Authored-By: Claude <noreply@anthropic.com> 这一支也拦
${FENCE}" 'abc x' 'dayuer <a@b.c>'

echo '--- local-ci.sh --help 的行号范围守卫 ---'

# `-h|--help` 打的是 `sed -n '2,Np'` 的行号范围：改头注释忘了同步 N，帮助文本会静默截断。
HELP=$(bash "$ROOT/scripts/local-ci.sh" --help 2>&1)
case "$HELP" in
  *EX_USAGE*'看见 3 去修脚本。）'*)
    ok '--help 把退出码那段整段打出来（sed 行号范围没漂）' ;;
  *) bad '--help 把退出码那段整段打出来（sed 行号范围没漂）' \
       '含 EX_USAGE、且含「看见 3 去修脚本。）」' "$(printf '%s' "$HELP" | tail -1)" ;;
esac

echo '--- §4 合并条件 1：approve 的 sha vs head ---'

C='[impl] REVIEW: changes sha=aaa1111
[auditor] REVIEW: approve sha=bbb2222
[coord] 顺带说一句别的'

eq '取**最后一次** approve（前面有 changes 也不算数）' 'bbb2222' "$(last_approve_sha "$C")"
eq '只有 changes、没有 approve → 空' '' "$(last_approve_sha 'REVIEW: changes sha=aaa1111')"
eq 'approve 与 head 完全相同 → ok' 'ok' "$(approve_vs_head 'bbb2222' 'bbb2222')"
eq 'approve 短写、head 全长 → ok（按前缀比）' 'ok' \
  "$(approve_vs_head 'e2e1968aa' 'e2e1968aa55dc27cc396feb137412ee564e5ff93')"
eq 'approve 对不上 head（复审后又推了）→ stale' 'stale' "$(approve_vs_head 'aaa1111' 'bbb2222')"
eq '没有任何 approve → none' 'none' "$(approve_vs_head '' 'bbb2222')"
eq '没拿到 head → nohead（别硬判）' 'nohead' "$(approve_vs_head 'bbb2222' '')"

echo
if [ "$fails" -eq 0 ]; then echo "OK: $0 全过"; exit 0; fi
echo "FAILED: $fails 条"; exit 1
