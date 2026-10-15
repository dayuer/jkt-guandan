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

# 并发隔离自测（#183）：单独改它们也要跑 —— 否则只有被 test-deploy.sh / restore-drill.sh
# 调起来时才有人跑，而那种改动本身不触发 docker 那项。
eq '并发隔离自测脚本单独改动也跑 docker' \
  'local-ci/docker' \
  "$(printf 'scripts/test-deploy-names.sh\nscripts/test-drill-names.sh\n' | scope_contexts)"

# docker-wait.sh（#186）被那三个 docker 脚本 source，单独改它也要跑 docker 那项。
eq 'docker-wait.sh 单独改动也跑 docker' \
  'local-ci/docker' \
  "$(printf 'scripts/docker-wait.sh\n' | scope_contexts)"

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
  out=$(protocol_failures "$2" "$3" "$4" "$5" "$ROOT/scripts"); rc=$?
  if [ "$rc" -eq 0 ] && [ -z "$out" ]; then ok "$1"
  else bad "$1" '退出码 0 且无输出' "退出码 ${rc}，输出: $out"; fi
}

fail_case() { # $1=名字 $2=title $3=body $4=commits $5=authors $6=输出应含
  local out rc
  out=$(protocol_failures "$2" "$3" "$4" "$5" "$ROOT/scripts"); rc=$?
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

改点东西，没写 Refs。' 'abc x' 'dayuer <a@b.c>' "$ROOT/scripts"); rc=$?
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

echo '--- scripts/ 里 $VAR 紧跟多字节字符（#178）---'

# UTF-8 locale 下 bash 3.2 会把紧跟 $VAR 的全角标点吃进变量名，`set -u` 直接中止（#178）。
# 这一项由 **protocol** 每个 PR 跑（见下面的断言），这里是全仓库的现状。
eq 'scripts/ 下没有这种写法' '' "$(unsafe_var_expansions "$ROOT/scripts")"

# 这个检查自己也要被测：正则哪天写坏了，它会一路静默绿下去 —— 正是它要防的那种假绿。
TMPD=$(mktemp -d)
printf 'log "镜像 $IMAGE，就绪"\n' > "$TMPD/bad.sh"
printf 'log "镜像 ${IMAGE}，就绪"\n' > "$TMPD/good.sh"
printf '# 注释：$IMAGE，不算\nlog ok\n' > "$TMPD/comment.sh"
printf "echo '\$FOO，不展开'\n" > "$TMPD/single.sh"
SCAN=$(unsafe_var_expansions "$TMPD")
case "$SCAN" in
  *bad.sh*) ok '扫得出 $VAR 紧跟全角标点' ;;
  *) bad '扫得出 $VAR 紧跟全角标点' '含 bad.sh' "$SCAN" ;;
esac
case "$SCAN" in
  *good.sh*|*comment.sh*|*single.sh*) bad '${VAR}、注释、单引号都不算' '空' "$SCAN" ;;
  *) ok '${VAR}、注释、单引号都不算' ;;
esac

# 有函数不够，还要钉住 **protocol 这个 context 真的会调用它** —— 这一项被放在 protocol
# （而不是 local-ci/meta）就是为了每个 PR 都跑；哪天有人把那个参数漏掉、或者把调用删了，
# 上面几条还是会全绿，而真实 PR 上什么都不查。
# 协议三项都合规、只有 scripts/ 里那一处脏 → protocol 必须红。
PDIRTY=$(protocol_failures '标题' "$GOOD_BODY" 'abc x' 'dayuer <a@b.c>' "$TMPD")
prc=$?
if [ "$prc" -eq 1 ] && printf '%s' "$PDIRTY" | grep -qF 'bad.sh'; then
  ok 'protocol 会调用它（协议都合规、只有脚本脏时也红）'
else
  bad 'protocol 会调用它（协议都合规、只有脚本脏时也红）' \
      '退出码 1 且输出含 bad.sh' "退出码 ${prc}，输出: $PDIRTY"
fi
rm -rf "$TMPD"

# 反过来：scripts/ 干净时，这一项不该把 protocol 带红（否则每个 PR 都会红）。
PCLEAN=$(protocol_failures '标题' "$GOOD_BODY" 'abc x' 'dayuer <a@b.c>' "$ROOT/scripts"); prc=$?
if [ "$prc" -eq 0 ] && [ -z "$PCLEAN" ]; then ok 'scripts 干净时 protocol 不受这一项影响'
else bad 'scripts 干净时 protocol 不受这一项影响' '退出码 0 且无输出' "退出码 ${prc}，输出: $PCLEAN"; fi

# 漏传第 5 个参数必须**报错**，不能静默跳过第 ④ 项（那会是一条绿 status 底下什么都没查）。
PNOARG=$(protocol_failures '标题' "$GOOD_BODY" 'abc x' 'dayuer <a@b.c>' 2>&1); prc=$?
if [ "$prc" -ne 0 ] && printf '%s' "$PNOARG" | grep -q 'protocol_failures 需要 scripts 目录'; then
  ok '漏传 scripts 目录时大声报错，不静默跳过'
else
  bad '漏传 scripts 目录时大声报错，不静默跳过' '非 0 且提示缺参数' "退出码 ${prc}，输出: $PNOARG"
fi

echo '--- 上报 status 的重试与收尾复核（#187）---'

# 用**假的 gh**：让前 N 次 POST 失败，看重试和复核是不是真的兜住了。
# 「本地全绿、GitHub 停在 pending」就是这么来的，所以这几条要能红。
TMPG=$(mktemp -d)
cat > "$TMPG/gh" <<'SH'
#!/bin/sh
case "$1 $2" in
  "api -X")                                     # 发 status
    printf '%s\n' "$*" >> "$STATE/posts"
    n=$(wc -l < "$STATE/posts" | tr -d ' ')
    [ "$n" -le "${FAIL_POST:-0}" ] && exit 1
    exit 0 ;;
esac
[ -n "$GET_JSON" ] || exit 1                    # 拉 status
printf '%s\n' "$GET_JSON"
SH
chmod +x "$TMPG/gh"
# 退避和复核等待都设成 0：不真睡，测试才快。
export POST_STATUS_BACKOFF=0 POST_STATUS_REPAIR_DELAY=0
SHA40=0123456789abcdef0123456789abcdef01234567

# 1) 前两次 POST 失败、第三次成功 → 重试救回来
STATE=$(mktemp -d "$TMPG/state.XXXXXX"); export STATE FAIL_POST=2 GET_JSON=''
post_status_retry "$TMPG/gh" "$SHA40" local-ci/test success 描述 >/dev/null 2>&1; rc=$?
eq '重试：前两次失败、第三次成功 → 0' '0' "$rc"
eq '重试：一共试了 3 次' '3' "$(wc -l < "$STATE/posts" | tr -d ' ')"

# 2) 一直失败 → 用尽次数后非 0，并且说清试了几次
STATE=$(mktemp -d "$TMPG/state.XXXXXX"); export STATE FAIL_POST=99 GET_JSON=''
out=$(post_status_retry "$TMPG/gh" "$SHA40" local-ci/test success 描述 4 2>&1); rc=$?
eq '重试：一直失败 → 非 0' '1' "$rc"
case "$out" in
  *"共试 4 次"*) ok '重试：说清了试了几次' ;;
  *) bad '重试：说清了试了几次' '含「共试 4 次」' "$out" ;;
esac

# 3) 收尾复核：记录里是 success、当前还停在 pending → 补发，而且补的是**最终结论**
R=$(mktemp)
printf '%s\n' 'local-ci/test pending 运行中' 'local-ci/test success 描述甲' > "$R"
STATE=$(mktemp -d "$TMPG/state.XXXXXX"); export STATE FAIL_POST=0 GET_JSON='[{"context":"local-ci/test","state":"pending"}]'
out=$(repair_statuses "$TMPG/gh" "$SHA40" "$R" 2>&1); rc=$?
eq '复核：还停在 pending → 补发成功、返回 0' '0' "$rc"
case "$out" in
  *"补发了 1 条"*) ok '复核：报了补发条数' ;;
  *) bad '复核：报了补发条数' '含「补发了 1 条」' "$out" ;;
esac
grep -q 'state=success' "$STATE/posts" \
  && ok '复核：补发用的是记录里的最终结论（success，不是那行 pending）' \
  || bad '复核：补发用的是最终结论' '含 state=success' "$(cat "$STATE/posts")"

# 4) 复核：都对得上 → 不补、返回 0
STATE=$(mktemp -d "$TMPG/state.XXXXXX"); export STATE FAIL_POST=0 GET_JSON='[{"context":"local-ci/test","state":"success"}]'
out=$(repair_statuses "$TMPG/gh" "$SHA40" "$R" 2>&1); rc=$?
eq '复核：对得上 → 0' '0' "$rc"
eq '复核：对得上时不补发' '0' "$(cat "$STATE/posts" 2>/dev/null | wc -l | tr -d ' ')"

# 5) 复核：补发也失败 → 非 0（调用方据此非零退出、大声报错）
STATE=$(mktemp -d "$TMPG/state.XXXXXX"); export STATE FAIL_POST=99 GET_JSON='[{"context":"local-ci/test","state":"pending"}]'
out=$(repair_statuses "$TMPG/gh" "$SHA40" "$R" 2>&1); rc=$?
eq '复核：补发也失败 → 非 0' '1' "$rc"
case "$out" in
  *"补发失败"*) ok '复核：补发失败时大声报错' ;;
  *) bad '复核：补发失败时大声报错' '含「补发失败」' "$out" ;;
esac
# 这一对才是拦「只看 repaired」那条假绿的：全都没补上时**不能**说「都比对过 / 没有要补」。
# 旧逻辑（只看 repaired）下这两条会红 —— 见 PR 里的红/绿输出。
case "$out" in
  *"都比对过"*|*"没有要补"*)
    bad '复核：全都没补上时不能说「都不用补」' '不含「都比对过」「没有要补」' "$out" ;;
  *) ok '复核：全都没补上时不能说「都不用补」' ;;
esac
case "$out" in
  *"还有 1 条没补上"*) ok '复核：说清了还有几条没补上' ;;
  *) bad '复核：说清了还有几条没补上' '含「还有 1 条没补上」' "$out" ;;
esac

# 6) 复核：连当前 status 都取不到 → 非 0（宁可让人去看一眼，也别当成都对得上）
STATE=$(mktemp -d "$TMPG/state.XXXXXX"); export STATE FAIL_POST=0 GET_JSON=''
out=$(repair_statuses "$TMPG/gh" "$SHA40" "$R" 2>&1); rc=$?
eq '复核：取不到 status → 非 0' '1' "$rc"

# 7) 复核只碰**本次跑过的** context；「第一次 POST 就失败、连 pending 都没发上去」也要补。
#    远端那一堆里还有个别的 context 停着 pending（上一次运行留下的），**不该去动它**。
STATE=$(mktemp -d "$TMPG/state.XXXXXX")
export STATE FAIL_POST=0
export GET_JSON='[{"context":"local-ci/docker","state":"pending"}]'
R2=$(mktemp)
printf '%s\n' 'local-ci/test success 描述甲' > "$R2"   # 本次只跑了 test
out=$(repair_statuses "$TMPG/gh" "$SHA40" "$R2" 2>&1); rc=$?
eq '复核：连 pending 都没发上去的 context 也补' '0' "$rc"
grep -q 'context=local-ci/test' "$STATE/posts" \
  && ok '复核：补的是本次跑过的 context' \
  || bad '复核：补的是本次跑过的 context' '含 context=local-ci/test' "$(cat "$STATE/posts")"
grep -q 'local-ci/docker' "$STATE/posts" \
  && bad '复核：不碰本次没跑的 context' '不含 local-ci/docker' "$(cat "$STATE/posts")" \
  || ok '复核：不碰本次没跑的 context'
rm -f "$R2"

echo '--- 退出码与分离头指针（#186）---'

eq '退出码：全绿 0' '0' "$(final_exit_code '' '' '' 0)"
eq '退出码：代码红 1（最优先）' '1' "$(final_exit_code ' local-ci/test' '' '' 1)"
eq '退出码：环境未就绪 2' '2' "$(final_exit_code '' ' local-ci/docker' '' 0)"
eq '退出码：脚本自身出错 3' '3' "$(final_exit_code '' '' ' local-ci/meta' 0)"
eq '退出码：status 没兜住 4' '4' "$(final_exit_code '' '' '' 1)"
eq '退出码：环境优先于 status' '2' "$(final_exit_code '' ' x' '' 1)"
eq '退出码：脚本 bug 优先于 status' '3' "$(final_exit_code '' '' ' x' 1)"

# 分离头指针（$3 是 `git symbolic-ref -q HEAD` 的退出码：0=在分支上，非 0=detached）
reject_rc() { should_reject_detached "$1" "$2" "$3"; printf '%s' "$?"; }
eq 'detached + 带上报 + 没给 --pr → 拒绝' '0' "$(reject_rc 1 '' 1)"
eq 'detached + 带上报 + 给了 --pr → 放行' '1' "$(reject_rc 1 999 1)"
eq '在分支上 + 带上报 → 放行' '1' "$(reject_rc 1 '' 0)"
eq 'detached + --no-post → 放行（本来就不上报）' '1' "$(reject_rc 0 '' 1)"

# `--pr <n>` 真的能用：配假 gh 跑一次协议检查（--base 指到 HEAD，改动范围是空的，只跑 protocol；
# --no-post 不上报）。这样测的是真的 local-ci.sh，不是复制的逻辑。
PG=$(mktemp -d)
cat > "$PG/gh" <<'GH'
#!/bin/sh
if [ "$1 $2" = "pr view" ]; then
  printf '%s\n' '{"number":999,"title":"T","body":"[impl] x\n\nFixes #1\n"}'
  exit 0
fi
exit 0
GH
chmod +x "$PG/gh"
out=$(cd "$ROOT" && GH_BIN="$PG/gh" PATH="$PG:$PATH" bash scripts/local-ci.sh --base HEAD --pr 999 --no-post 2>&1); rc=$?
eq '--pr：能跑通（退出 0）' '0' "$rc"
case "$out" in
  *"PR #999"*) ok '--pr：按号认出 PR 并打出来' ;;
  *) bad '--pr：按号认出 PR 并打出来' '含 PR #999' "$out" ;;
esac
rm -rf "$PG"

# 8) 手动补发命令要按**实际结论**打印（#186 复审）。原来写死在 local-ci.sh 里，
#    测试碰不到；改成 `manual_repair_commands` 之后这里能断言。
REP=$(mktemp)
printf '%s\n' 'local-ci/test failure 挂了' > "$REP"
out=$(manual_repair_commands ghost "$SHA40" "$REP")
case "$out" in
  *"state=failure"*) ok '手动补发：按实际结论打 state=failure' ;;
  *) bad '手动补发：按实际结论打 state=failure' '含 state=failure' "$out" ;;
esac
case "$out" in
  *"state=success"*) bad '手动补发：红的 context 不能打 state=success' '不含 state=success' "$out" ;;
  *) ok '手动补发：红的 context 不能打 state=success' ;;
esac
# 同一个 context 先记 pending 再记 failure：要用**最后**那条（failure）。
printf '%s\n' 'local-ci/test pending 运行中' 'local-ci/test failure 挂了' > "$REP"
out=$(manual_repair_commands ghost "$SHA40" "$REP")
case "$out" in
  *"state=failure"*) ok '手动补发：同一 context 取最后一条结论' ;;
  *) bad '手动补发：同一 context 取最后一条结论' '含 state=failure' "$out" ;;
esac
eq '手动补发：一个 context 只打一行' '1' "$(printf '%s\n' "$out" | grep -c 'statuses/')"
rm -f "$REP"

# 9) 带上报时 PR 的 head 必须等于本地 HEAD（#186 复审）
mismatch_rc() { should_reject_pr_head_mismatch "$1" "$2" "$3"; printf '%s' "$?"; }
eq 'PR head 对不上 + 带上报 → 拒绝' '0' "$(mismatch_rc 1 abc def)"
eq 'PR head 一样 → 放行' '1' "$(mismatch_rc 1 abc abc)"
eq '没取到 PR（head 空）→ 放行' '1' "$(mismatch_rc 1 '' abc)"
eq '--no-post → 放行' '1' "$(mismatch_rc 0 abc def)"

# 真跑一次 local-ci.sh 看它是不是真的拒了（假 gh 给的 headRefOid 和本地 HEAD 不一样）。
# ⚠️ 这一条要求工作区干净（带上报时脏树会被更早的那道检查拦下、退出码也是 64，
# 但信息不一样）—— CI 里跑一定是干净的。
PG=$(mktemp -d)
cat > "$PG/gh" <<'GH'
#!/bin/sh
if [ "$1 $2" = "pr view" ]; then
  printf '%s\n' '{"number":999,"title":"T","body":"[impl] x\n\nFixes #1\n","headRefOid":"0000000000000000000000000000000000000000"}'
  exit 0
fi
exit 0
GH
chmod +x "$PG/gh"
out=$(cd "$ROOT" && GH_BIN="$PG/gh" PATH="$PG:$PATH" bash scripts/local-ci.sh --base HEAD --pr 999 2>&1); rc=$?
eq '--pr：head 对不上时带上报直接拒绝（64）' '64' "$rc"
case "$out" in
  *"对不上"*) ok '--pr：head 对不上时说清了原因' ;;
  *) bad '--pr：head 对不上时说清了原因' '含「对不上」' "$out" ;;
esac
# 只看退出码是不够的：工作区不干净那道检查**也是** exit 64。所以还要确认这次拒绝来自
# head 核对。少了这一条，把核对整个删掉（演示过）这条测试照样会过。
case "$out" in
  *"工作区不干净"*) bad '--pr：拒绝来自 head 核对（不是脏树那道检查）' '不含「工作区不干净」' "$out" ;;
  *) ok '--pr：拒绝来自 head 核对（不是脏树那道检查）' ;;
esac
rm -rf "$PG"

# 10) 自测编号的边界（#186）：编号上限是 1248，1249 会被拒。
#
# ⚠️ **只跑 1249 那一次，不要真跑一个合法编号**（#186 复审阻塞）：钉死一个编号真跑，
# 就会真的去绑 `20000+编号*8` 开头的那一组端口 —— 这台机器上每一次 local-ci/meta 都抢
# 同一组口，#181/#183 做出来的「按编号划窗口」就白做了（impl / auditor / coord 的
# pre-merge 经常同时跑 meta，会表现成随机的红、挡住合并）。
#
# 1249 这一次是安全的：它在绑端口**之前**就退出了（那个越界检查在 `run_once` 前面）。
X=$(cd "$ROOT" && DEPLOYTEST_RUN=1249 sh scripts/test-deploy-names.sh 2>&1); rc=$?
[ "$rc" -ne 0 ] && ok 'names 自测：编号 1249 越界被拒（不绕回窗口 0）' \
  || bad 'names 自测：编号 1249 越界被拒（不绕回窗口 0）' '非 0' "退出码 $rc"
case "$X" in
  *"超出范围"*) ok 'names 自测：拒绝时说清了原因' ;;
  *) bad 'names 自测：拒绝时说清了原因' '含「超出范围」' "$X" ;;
esac

# 11) 重试次数给非数字时不要死循环（#186 复审里的小问题）
STATE=$(mktemp -d "$TMPG/state.XXXXXX"); export STATE FAIL_POST=99 GET_JSON=''
out=$(post_status_retry "$TMPG/gh" "$SHA40" local-ci/test success 描述 abc 2>&1); rc=$?
eq '重试：次数给非数字 → 退回默认、不死循环' '1' "$rc"
eq '重试：非数字时确实试了 4 次' '4' "$(wc -l < "$STATE/posts" | tr -d ' ')"

unset STATE FAIL_POST GET_JSON POST_STATUS_BACKOFF POST_STATUS_REPAIR_DELAY
rm -rf "$TMPG" "$R"

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
