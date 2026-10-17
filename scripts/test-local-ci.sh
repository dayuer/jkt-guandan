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

# 计分规则的对照用例两端共用（#174）：服务端规则改了，小游戏的 vitest 也要跑。
eq '计分规则改动也要跑小游戏' \
  "$(lines local-ci/test local-ci/minigame local-ci/vuln local-ci/docker local-ci/e2e)" \
  "$(printf 'server/internal/scoring/scoring.go\n' | scope_contexts)"

# #253 P7：那份对照用例挪到了仓库根 testdata/scoring/（原来是 server/internal/scoring/testdata/，
# 应用反向伸进服务端内部）。挪走之后它自己**必须**有分支，否则改用例时一条检查都不跑。
eq '计分对照用例改动：两端都要跑' \
  "$(lines local-ci/test local-ci/minigame)" \
  "$(printf 'testdata/scoring/vectors.json\n' | scope_contexts)"

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

# #253：两端共用的纯逻辑搬到了 app/shared（第一批是 status）。H5 和小游戏都 import 它，
# 所以两边的检查都要跑；这个目录不在 app/h5 下，不会被 h5 的分支抢走，但它自己**必须有**
# 一条分支 —— 漏了就是「改了没人跑」。
# docker 那条也挂着：H5 的镜像是从零 npm ci + npm run build 的，共享代码会被打进 dist，
# 目录结构一变（P1 就撞过）只有跑镜像构建才发现得了。
eq 'app/shared 改动：test / minigame / docker / e2e 都要跑' \
  "$(lines local-ci/test local-ci/minigame local-ci/docker local-ci/e2e)" \
  "$(printf 'app/shared/src/status.ts\n' | scope_contexts)"

# #87：小游戏从第 6 步起 import app/rules（计分规则搬过去了），改了引擎小游戏的包也会变 ——
# 和上面 app/h5/src/* 那条同一个道理。这条断言就是钉住这件事的：漏了它，改规则引擎时
# 小游戏那边不会跑，打包坏了也不会红。
eq '规则引擎改动：rules 和 minigame 都要跑' \
  "$(lines local-ci/rules local-ci/minigame)" \
  "$(printf 'app/rules/src/card.ts\napp/rules/package.json\n' | scope_contexts)"

# 两端共用的牌例（#87 的 TS 版和 #204 的 Go 版读同一批文件）。它在 server/ 和 app/ 之外 ——
# 不单独列出来的话，只改牌例的 PR 一条检查都不跑（静默漏检）。
eq '共用牌例改动：两端都要跑' \
  "$(lines local-ci/test local-ci/rules)" \
  "$(printf 'testdata/rules/deck.json\n' | scope_contexts)"

# #253 的共享层守门脚本：它跑在 `make shared-check`（= local-ci/test）里。scripts/ 下
# 别的检查脚本都各自挂着一项，这个漏挂就同样是「改了没人跑」。
eq '共享层守门脚本改动要跑 test' \
  'local-ci/test' \
  "$(printf 'scripts/check-shared-imports.mjs\nscripts/check-shared-imports.test.mjs\n' | scope_contexts)"

# Go 端牌例读取器里的条款号正则被 app/rules 的 rule-sync.test.ts 逐字比对（#87 配套）：
# 只改它也要跑 rules；原来服务端那四项照旧（它本身是 Go 测试）。
eq 'Go 端牌例读取器改动：服务端四项 + rules' \
  "$(lines local-ci/test local-ci/rules local-ci/vuln local-ci/docker local-ci/e2e)" \
  "$(printf 'server/internal/rules/fixtures_test.go\n' | scope_contexts)"
eq 'server/internal/rules 下别的文件不额外跑 rules（只有正则所在的那个文件）' \
  "$(lines local-ci/test local-ci/vuln local-ci/docker local-ci/e2e)" \
  "$(printf 'server/internal/rules/combo.go\n' | scope_contexts)"

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
# #196：test-deploy.sh 现在 source local-ci-lib.sh 并用它的 docker_build_or_die，
# 所以改 lib 两样都要跑（自测 + docker）。漏掉 docker 的话，改了那个函数不会有人集成验证。
eq 'local-ci-lib.sh 改动：meta + docker 都要跑' \
  "$(lines local-ci/meta local-ci/docker)" \
  "$(printf 'scripts/local-ci-lib.sh\n' | scope_contexts)"

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
for c in local-ci/test local-ci/vuln local-ci/h5-audit local-ci/minigame local-ci/rules local-ci/nginx local-ci/docker local-ci/e2e local-ci/meta; do
  case " $CONTEXT_ORDER " in *" $c "*) : ;; *) missing="$missing $c" ;; esac
done
eq 'CONTEXT_ORDER 列全了每个 context' '' "$missing"

echo '--- context_command ---'

for c in $CONTEXT_ORDER; do
  if [ -n "$(context_command "$c")" ]; then ok "context_command 认识 $c"
  else bad "context_command 认识 $c" '非空' '空'; fi
done

# context_tools 漏写是**静默**跳过工具检查（require_tools '' 直接返回 0），加检查项时最容易漏这一处。
missing_tools=''
for c in $CONTEXT_ORDER; do
  [ -n "$(context_tools "$c")" ] || missing_tools="$missing_tools $c"
done
eq 'CONTEXT_ORDER 里每个 context 都声明了 context_tools' '' "$missing_tools"
# make e2e 靠 scripts/e2e-port.sh 探端口（#228），那是 python3。
case " $(context_tools local-ci/e2e) " in
  *' python3 '*) ok 'e2e 的工具里有 python3（e2e-port.sh 要用）' ;;
  *) bad 'e2e 的工具里有 python3（e2e-port.sh 要用）' '含 python3' "$(context_tools local-ci/e2e)" ;;
esac

eq 'test 就是 make test' 'make test  # go vet + gofmt + 前端构建 + go test -race + 前端测试' "$(context_command local-ci/test)"

# #147 之后：`make test` 必须真的跑 gofmt 检查。main 上 4d4882c 那笔「chore: gofmt」按别的
# 版本的 gofmt 把 play_test.go 改坏了，而 CI 不查 gofmt，就一直没人发现。这条断言钉住：
# 描述改了、但依赖里没有 gofmt-check（或者反过来）都要红。
# 注意不能用 `make -n test | grep -q`：本脚本开了 pipefail，grep -q 一命中就关管道，
# make 吃 SIGPIPE，整条管道反而返回非 0（假红）。先落到变量里再 case 匹配。
make_test_rules="$(make -n test 2>/dev/null)"
case "$make_test_rules" in
  *'/bin/gofmt'*) gofmt_wired=yes ;;
  *) gofmt_wired=no ;;
esac
eq 'make test 真的会跑 gofmt 检查' 'yes' "$gofmt_wired"

# #279：本项目的 buildx builder（缓存有上限）只影响 docker 那项 —— 它不改任何代码，
# 只改「怎么 build 镜像」。挂错了范围要么白跑，要么该跑的时候没跑。
eq 'buildx builder 的脚本只触发 docker 那项' 'local-ci/docker' \
  "$(printf 'scripts/ci-builder.sh\nscripts/ci-buildkitd.toml\n' | scope_contexts)"

# #278 复审的阻塞项：**gofmt 自己出错时守门不能静默通过**。make 用不带 -e 的 sh -c 跑，
# 命令替换里的失败会被吞掉、gofmt 的错误又走 stderr，于是「gofmt 不存在 / 文件有语法错误 /
# 文件名被拆开 / 文件被删了」都表现为「输出为空 → 通过」。这里拿 GOFMT 指一个不存在的路径，
# 断言 make gofmt-check 必须失败（make 会返回 2）。
make gofmt-check GOFMT=/nonexistent/gofmt >/dev/null 2>&1
gofmt_missing_rc=$?
eq 'gofmt 不存在时 make gofmt-check 必须失败' 'yes' \
  "$([ "$gofmt_missing_rc" -ne 0 ] && echo yes || echo no)"
eq 'minigame 与原 ci.yml 一致' \
  'cd app/minigame && npm ci && npm run typecheck && npm test && npm run build' \
  "$(context_command local-ci/minigame)"
eq 'meta 跑 CI 自己的自测' 'bash scripts/test-local-ci.sh' "$(context_command local-ci/meta)"
eq 'rules 跑规则引擎自己的 typecheck + vitest' \
  'cd app/rules && npm ci && npm run typecheck && npm test' \
  "$(context_command local-ci/rules)"

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
eq 'rules 要的 node 目录' 'app/rules' "$(node_dirs_for_context local-ci/rules)"
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

echo '--- 失败时留下完整日志和现场（#202）---'

# 文件名：sha 前 7 位 + context 短名 + 时间戳。短名是为了 status 描述塞得下
#（GitHub 只有 140 字，而 macOS 的 $TMPDIR 本身就占 45 个字）。
eq '日志名：sha 取前 7 位、context 去掉 local-ci/ 前缀' \
  'ac6c141-docker-20261016-061500-777.log' \
  "$(log_file_name ac6c14176552e526641a12de1d0ae66f8d6b1eb2 local-ci/docker 20261016-061500 777)"
eq '日志名：别的 context 也一样' \
  'ac6c141-test-20261016-061500-777.log' \
  "$(log_file_name ac6c1417 local-ci/test 20261016-061500 777)"
eq '日志名：sha 短于 7 位也不炸' 'abc-x-t-777.log' "$(log_file_name abc x t 777)"
# 同一个 SHA、同一秒、同一个 context 跑两次不能撞名 —— 后跑的那次成功会把先跑那次
# 失败日志删掉，等于白留。所以文件名末尾带 $$。
eq '日志名：同一个 sha/秒/context，不同进程也不撞名' '1' \
  "$([ "$(log_file_name abc1234 local-ci/docker t 111)" != "$(log_file_name abc1234 local-ci/docker t 222)" ] && echo 1 || echo 0)"
eq '日志名：不传 pid 时默认用 $$' "$(log_file_name abc1234 local-ci/docker t)" "$(log_file_name abc1234 local-ci/docker t $$)"

# 描述上限 140 个**字符**（GitHub 的规格）。中文一个字算一个，砍的是尾巴（路径在后）。
#
# ⚠️ 这些断言在 **`LC_ALL=C`** 下也必须过 —— agent / launchd / cron 起的 shell 常常就是
# 那个环境。**断言自己也别用 `${#…}` 数字符**：那东西同样跟着 locale 走，会把「代码错了」
# 和「断言错了」搅在一起。统一走 utf8_len。
eq '描述：不超就不动' 'abc' "$(truncate_desc abc)"
d140="$(printf 'x%.0s' $(seq 1 140))"
eq '描述：刚好 140 不动' "$d140" "$(truncate_desc "$d140")"

eq 'utf8_len：ASCII 就是字节数' '3' "$(utf8_len abc)"
eq 'utf8_len：空串是 0' '0' "$(utf8_len '')"
cn140="$(printf '环%.0s' $(seq 1 140))"
eq 'utf8_len：140 个汉字就是 140（不跟 locale 变）' '140' "$(utf8_len "$cn140")"
eq '描述：140 个汉字**不**被截（按字符算，不是按字节）' "$cn140" "$(truncate_desc "$cn140")"

# 126 个字的一条：按字节算是 378、会被误截，按字符算不该动 —— 这就是 auditor 报的那条。
d126="$(printf '环%.0s' $(seq 1 126))"
eq '描述：126 个字不超上限、**不**该被截' "$d126" "$(truncate_desc "$d126")"

cn141="$(printf '环%.0s' $(seq 1 141))"
t141="$(truncate_desc "$cn141")"
eq '描述：141 个汉字砍到 140 个字符' '140' "$(utf8_len "$t141")"
case "$t141" in
  *…) ok '描述：砍完以「…」结尾' ;;
  *) bad '描述：砍完以「…」结尾' '以 … 结尾' "$t141" ;;
esac
# 关键：绝不能从半个字中间切开 —— 那样不是合法 UTF-8，GitHub 要么拒收要么存成乱码。
if printf '%s' "$t141" | iconv -f UTF-8 -t UTF-8 >/dev/null 2>&1; then
  ok '描述：砍完仍是**合法 UTF-8**（没从半个字中间切）'
else
  bad '描述：砍完仍是**合法 UTF-8**（没从半个字中间切）' 'iconv 认它是合法 UTF-8' '不是'
fi

# 函数本身也不该跟着 locale 变：同一件事在 LC_ALL=C 下跑，结果必须一样。
eq '描述：LC_ALL=C 下结果一样（#178 那一类）' '140' \
  "$(LC_ALL=C bash -c ". '$ROOT/scripts/local-ci-lib.sh'; utf8_len \"\$(truncate_desc "$cn141")\"" 2>/dev/null)"
eq '描述：LC_ALL=C 下 140 个汉字也不被截' "$cn140" \
  "$(LC_ALL=C bash -c ". '$ROOT/scripts/local-ci-lib.sh'; truncate_desc \"$cn140\"" 2>/dev/null)"


# macOS 的 $TMPDIR 结尾**自带一个 `/`**，不去掉路径就成 `/var/…/T//local-ci-logs`：
# 打印难看，而且 status 描述只有 140 字，白占一格。真跑红的那次就是这么发现的。
# ⚠️ 每条都要先把 LOCAL_CI_LOG_DIR 清空：它优先级更高，不清的话外面跑 CI 时
# （`LOCAL_CI_LOG_DIR=… scripts/local-ci.sh`）这几条就会拿到那个目录、**在 CI 里红**
# —— 直接跑本文件时它是空的，所以只在 CI 里露出来。第一版就是这么漏的。
eq '日志目录：$TMPDIR 结尾的斜杠要去掉' '/x/local-ci-logs' "$(LOCAL_CI_LOG_DIR= TMPDIR=/x/ local_ci_log_dir)"
eq '日志目录：LOCAL_CI_LOG_DIR 也去尾斜杠' '/y' "$(LOCAL_CI_LOG_DIR=/y/ TMPDIR= local_ci_log_dir)"
eq '日志目录：正常路径不动' '/z/local-ci-logs' "$(LOCAL_CI_LOG_DIR= TMPDIR=/z local_ci_log_dir)"
eq '日志目录：不设 TMPDIR 时退回 /tmp' '/tmp/local-ci-logs' "$(LOCAL_CI_LOG_DIR= TMPDIR= local_ci_log_dir)"

# 保留最近 N 份。
PL=$(mktemp -d)
for i in 1 2 3 4 5; do printf 'x\n' > "$PL/a-$i.log"; done
printf 'x\n' > "$PL/a-3.docker.txt"   # 第 3 份的现场，该跟着一起删
prune_logs "$PL" 2
eq '只留最近 2 份' '2' "$(ls "$PL"/*.log 2>/dev/null | wc -l | tr -d ' ')"
eq '留下的确实是最新的两份' 'a-4.log a-5.log' "$(ls "$PL"/*.log 2>/dev/null | xargs -n1 basename | sort | tr '\n' ' ' | sed 's/ $//')"
eq '被删那份的 .docker.txt 也一起删了' '0' "$(ls "$PL"/a-3.docker.txt 2>/dev/null | wc -l | tr -d ' ')"
eq '空目录不炸' '0' "$(prune_logs "$PL/nope" 3; echo $?)"
rm -rf "$PL"

# 真跑一遍 capture_to_log：**成功不留文件、失败留文件**（这就是「用假的失败 context 自测」）。
CL=$(mktemp -d)
out=$(capture_to_log local-ci/test "$CL/ok.log" true 2>&1); rc=$?
eq '跑成功 → 返回 0' '0' "$rc"
eq '跑成功 → 不留日志文件' '0' "$(ls -A "$CL" | wc -l | tr -d ' ')"

out=$(capture_to_log local-ci/test "$CL/bad.log" sh -c 'echo 出错了; exit 1' 2>&1); rc=$?
eq '跑失败 → 原样带回退出码' '1' "$rc"
eq '跑失败 → 留下完整日志' '1' "$(ls "$CL"/bad.log 2>/dev/null | wc -l | tr -d ' ')"
case "$out" in
  *"这一项的完整日志：$CL/bad.log"*) ok '跑失败 → 打印日志路径' ;;
  *) bad '跑失败 → 打印日志路径' "含「这一项的完整日志：$CL/bad.log」" "$out" ;;
esac
case "$(cat "$CL/bad.log")" in
  *"出错了"*) ok '日志里有完整输出' ;;
  *) bad '日志里有完整输出' '含「出错了」' "$(cat "$CL/bad.log")" ;;
esac
eq '非 docker 的项不写现场文件' '0' "$(ls "$CL"/*.docker.txt 2>/dev/null | wc -l | tr -d ' ')"

# docker 那一项失败时另外写一份现场（ps -a / volume ls 的快照 + 最后 200 行）。
FD=$(mktemp -d)
cat > "$FD/docker" <<'DK'
#!/bin/sh
case "$1 $2" in
  "ps -a")     printf '  guandan-deploytest-1  Up 2 minutes  127.0.0.1:28480->8080/tcp\n  other-container  Up  1 minute\n' ;;
  "volume ls") printf '  guandan-deploytest-1-data\n  other-volume\n' ;;
esac
DK
chmod +x "$FD/docker"
out=$(PATH="$FD:$PATH" capture_to_log local-ci/docker "$CL/d.log" sh -c 'echo 挂了; exit 1' 2>&1); rc=$?
eq 'docker 失败 → 退出码照旧' '1' "$rc"
eq 'docker 失败 → 写了现场文件' '1' "$(ls "$CL"/d.docker.txt 2>/dev/null | wc -l | tr -d ' ')"
scene="$(cat "$CL/d.docker.txt" 2>/dev/null)"
case "$scene" in
  *guandan-deploytest-1*) ok '现场里有本次运行的容器' ;;
  *) bad '现场里有本次运行的容器' '含 guandan-deploytest-1' "$scene" ;;
esac
case "$scene" in
  *other-container*) bad '现场里不该有别人的容器（只列 deploytest/drill/smoke）' '不含 other-container' "$scene" ;;
  *) ok '现场里不该有别人的容器（只列 deploytest/drill/smoke）' ;;
esac
case "$scene" in
  *"最后 200 行"*) ok '现场里附了日志尾部' ;;
  *) bad '现场里附了日志尾部' '含「最后 200 行」' "$scene" ;;
esac
case "$out" in
  *"docker 现场：$CL/d.docker.txt"*) ok 'docker 失败 → 也打印现场路径' ;;
  *) bad 'docker 失败 → 也打印现场路径' "含「docker 现场：$CL/d.docker.txt」" "$out" ;;
esac
# ⚠️ **经过 `run_context` 的真调用方式**（#203 复审的阻塞）：`capture_to_log` 在子壳里跑命令，
# `run_context` 设的 `ENV_REASON` 带不出来，描述会退回成通用的「环境未就绪：<命令>」——
# 「拉镜像失败（网络或镜像源）」那句白写了。
# 前面那些 `true` / `sh -c 'exit 1'` 都**不经过 run_context**，所以一条都没抓到。
# 这又是「测试的调用方式和真实调用方式不一致」。
FD2=$(mktemp -d)
cat > "$FD2/docker" <<'DK'
#!/bin/sh
case "$1" in
  info)  exit 0 ;;                                    # 前置检查：守护进程在
  build) printf 'ERROR: failed to authorize: dial tcp: lookup auth.docker.io: i/o timeout\n' >&2; exit 1 ;;
  # #279 起 docker 那项走 `docker buildx build --builder guandan-ci`（scripts/ci-builder.sh），
  # 所以这里也要把 buildx 的路造出来，否则假 docker 会让构建「成功」、网络失败场景测不到。
  buildx)
    case "$2" in
      inspect) exit 0 ;;                              # builder 已存在
      build) printf 'ERROR: failed to authorize: dial tcp: lookup auth.docker.io: i/o timeout\n' >&2; exit 1 ;;
    esac ;;
  rmi)   exit 0 ;;
esac
exit 0
DK
chmod +x "$FD2/docker"
# ⚠️ **不能写成 `out=$(… capture_to_log …)`**：命令替换本身就是子壳，`ENV_REASON`
# 一样带不出来 —— 那样这条会**假红**（代码是对的）。重定向到文件再看。
PATH="$FD2:$PATH" DOCKER_BUILD_RETRY_BASE=0 capture_to_log local-ci/docker "$CL/ctx.log" run_context local-ci/docker > "$CL/ctxout.txt" 2>&1
rc=$?
out="$(cat "$CL/ctxout.txt")"
eq '真调用方式：拉镜像网络问题 → 2（环境未就绪）' '2' "$rc"
eq '真调用方式：原因是「拉镜像失败」（改用文件带出来，不再被丢在子壳里）' \
  '拉镜像失败（网络或镜像源）' "$ENV_REASON"
desc="$(env_not_ready_desc "$ENV_REASON" 'docker build + scripts/container-smoke.sh')$(log_note "$CL/ctx.log")"
case "$desc" in
  *"拉镜像失败"*) ok '真调用方式：拼出来的描述里有「拉镜像失败」' ;;
  *) bad '真调用方式：拼出来的描述里有「拉镜像失败」' '含「拉镜像失败」' "$desc" ;;
esac
eq '真调用方式：docker 现场也生成了' '1' "$(ls "$CL"/ctx.docker.txt 2>/dev/null | wc -l | tr -d ' ')"
case "$(cat "$CL/ctx.docker.txt" 2>/dev/null)" in
  *"自己清理之后"*) ok '现场里注明了快照是清理之后拍的' ;;
  *) bad '现场里注明了快照是清理之后拍的' '含「自己清理之后」' "$(cat "$CL/ctx.docker.txt" 2>/dev/null)" ;;
esac
rm -rf "$FD2"

# 日志文件权限：别人不该读到（Linux 上没 TMPDIR 时默认落在共享的 /tmp）。
eq '日志文件权限是 600（别人读不到）' 'rw-------' "$(ls -l "$CL/ctx.log" 2>/dev/null | cut -c2-10)"

rm -rf "$CL" "$FD"

# status 描述里那句「日志在 …」：文件在才有，成功时日志已删所以是空串。
LT=$(mktemp)
eq '描述：日志还在就写上路径' "；日志 $LT" "$(log_note "$LT")"
eq '描述：日志不在了就是空串' '' "$(log_note "${LT}.nope")"
eq '描述：没传也是空串' '' "$(log_note '')"
rm -f "$LT"
echo '--- §4.2 docker 拉镜像失败：环境未就绪 vs 代码红（#193）---'

# 判定本身用小函数测：日志是**真的 docker/buildkit 报错原文**。
DK=$(mktemp -d)
cat > "$DK/pull-dns.log" <<'L'
#1 [internal] load metadata for docker.io/library/node:22-alpine
#1 ERROR: failed to solve: node:22-alpine: failed to resolve source metadata for docker.io/library/node:22-alpine:latest: failed to authorize: failed to fetch oauth token: Post "https://auth.docker.io/token": dial tcp: lookup auth.docker.io: i/o timeout
L
cat > "$DK/pull-deadline.log" <<'L'
#1 ERROR: failed to solve: golang:1.25-alpine: failed to resolve source metadata for docker.io/library/golang:1.25-alpine:latest: failed to authorize: DeadlineExceeded
L
cat > "$DK/pull-502.log" <<'L'
#2 [1/6] FROM docker.io/library/node:22-alpine
#2 ERROR: failed to do request: Head "https://registry-1.docker.io/v2/library/node/manifests/22-alpine": Bad Gateway
L
cat > "$DK/pull-ratelimit.log" <<'L'
#2 ERROR: failed to solve: failed to copy: toomanyrequests: You have reached your pull rate limit.
L
cat > "$DK/run-fail.log" <<'L'
#7 [4/6] RUN npm ci
#7 12.34 npm ERR! code ELIFECYCLE
#7 ERROR: process "/bin/sh -c npm ci" did not complete successfully: exit code: 1
L
cat > "$DK/dockerfile-typo.log" <<'L'
ERROR: failed to solve: Dockerfile parse error line 12: unknown instruction: COPYY
L
# 这条是**故意**的陷阱：RUN 步骤自己打了一行 "502 Bad Gateway"（比如它 curl 了个网关），
# 但它**是有退出码的** —— 属于代码问题。「网络特征优先」的写法会把它记成环境未就绪，
# 那就是假绿，正是 dirty_tree_action 防的那一类。
cat > "$DK/run-prints-502.log" <<'L'
#7 [4/6] RUN curl -fsS http://gateway/health
#7 0.21 curl: (22) The requested URL returned error: 502 Bad Gateway
#7 ERROR: process "/bin/sh -c curl -fsS http://gateway/health" did not complete successfully: exit code: 22
L
: > "$DK/empty.log"

# --- 这些**必须**判成 code（#196 复审阻塞）----------------------------------
# auditor 用不存在的 tag 真跑了一次，这是代码错，第一版却被判成环境问题、重试 3 次、
# 发成 error，还提示「这不是代码问题，不用改 Dockerfile」。
#
# 根因：`failed to resolve source metadata` 是 buildkit 解析 FROM 失败时的**通用前缀**，
# 什么原因都会带 —— 它不是网络特征。我把它塞进特征表，等于给这一类错误开了后门；
# 而「RUN 退出码优先」那道防线在这里**够不着**（tag 不存在时根本没有 RUN 步骤，自然没有退出码）。
#
# 下一条是 auditor 那次跑出来的**真日志**（除了镜像名没改一个字）。
cat > "$DK/from-not-found.log" <<'L'
#0 building with "orbstack" instance using docker driver

#1 [internal] load build definition from Dockerfile
#1 transferring dockerfile: 77B 0.0s done
#1 DONE 0.0s

#2 [internal] load metadata for docker.io/library/alpine:this-tag-does-not-exist-196
#2 ERROR: docker.io/library/alpine:this-tag-does-not-exist-196: not found
------
 > [internal] load metadata for docker.io/library/alpine:this-tag-does-not-exist-196:
------
Dockerfile:1
--------------------
   1 | >>> FROM alpine:this-tag-does-not-exist-196
   2 |
--------------------
ERROR: failed to build: failed to solve: alpine:this-tag-does-not-exist-196: failed to resolve source metadata for docker.io/library/alpine:this-tag-does-not-exist-196: docker.io/library/alpine:this-tag-does-not-exist-196: not found
L
cat > "$DK/manifest-unauthorized.log" <<'L'
#2 ERROR: failed to solve: docker.io/library/private:latest: failed to resolve source metadata for docker.io/library/private:latest: unexpected status from HEAD request to https://registry-1.docker.io/v2/library/private/manifests/latest: 401 Unauthorized
L
cat > "$DK/auth-denied.log" <<'L'
#2 ERROR: failed to solve: docker.io/library/private:latest: failed to resolve source metadata for docker.io/library/private:latest: denied: requested access to the resource is denied
L
# 私有镜像没给凭证：`failed to authorize` 单独出现不算网络 —— 那是配置问题。
cat > "$DK/authorize-401.log" <<'L'
#2 ERROR: failed to solve: private.example.com/team/app:latest: failed to resolve source metadata for private.example.com/team/app:latest: failed to authorize: 401 Unauthorized
L
cat > "$DK/authorize-denied.log" <<'L'
#2 ERROR: failed to solve: private.example.com/team/app:latest: failed to resolve source metadata for private.example.com/team/app:latest: failed to authorize: denied: access denied
L
# 还是那一行，但**同时**带上了超时特征：这才是真的网络故障。
cat > "$DK/authorize-timeout.log" <<'L'
#2 ERROR: failed to solve: private.example.com/team/app:latest: failed to resolve source metadata for private.example.com/team/app:latest: failed to authorize: failed to fetch oauth token: Post "https://private.example.com/token": dial tcp: lookup private.example.com: i/o timeout
L

eq 'FROM 的 tag 不存在（真日志）→ code' 'code' "$(docker_failure_kind "$DK/from-not-found.log")"
eq '镜像源 401 Unauthorized → code' 'code' "$(docker_failure_kind "$DK/manifest-unauthorized.log")"
eq '镜像源 denied → code' 'code' "$(docker_failure_kind "$DK/auth-denied.log")"
eq 'failed to authorize + 401 → code（私有镜像没给凭证是配置问题）' 'code' "$(docker_failure_kind "$DK/authorize-401.log")"
eq 'failed to authorize + denied → code' 'code' "$(docker_failure_kind "$DK/authorize-denied.log")"
eq 'failed to authorize + i/o timeout → network（真的网络故障）' 'network' "$(docker_failure_kind "$DK/authorize-timeout.log")"

cat > "$DK/pull-refused.log" <<'L'
#2 ERROR: failed to do request: Head "https://registry-1.docker.io/v2/library/node/manifests/22-alpine": dial tcp 44.208.254.194:443: connect: connection refused
L
eq '镜像源 connection refused → network（#196 复审意见）' 'network' "$(docker_failure_kind "$DK/pull-refused.log")"

eq '拉镜像 DNS 超时 → network' 'network' "$(docker_failure_kind "$DK/pull-dns.log")"
eq 'failed to authorize: DeadlineExceeded → network' 'network' "$(docker_failure_kind "$DK/pull-deadline.log")"
eq '镜像源 Bad Gateway → network' 'network' "$(docker_failure_kind "$DK/pull-502.log")"
eq '镜像源限流 toomanyrequests → network' 'network' "$(docker_failure_kind "$DK/pull-ratelimit.log")"
eq 'RUN 步骤失败（有 exit code）→ code' 'code' "$(docker_failure_kind "$DK/run-fail.log")"
eq 'Dockerfile 写错 → code' 'code' "$(docker_failure_kind "$DK/dockerfile-typo.log")"
eq 'RUN 自己打了 502 但有 exit code → code（不能被网络特征骗过去）' 'code' "$(docker_failure_kind "$DK/run-prints-502.log")"
eq '空日志 → code（认不出来的一律算代码问题）' 'code' "$(docker_failure_kind "$DK/empty.log")"

# 重试本身：假 docker 记调用次数，按脚本给出的结论回应。
DB=$(mktemp -d)
cat > "$DB/docker" <<'DK'
#!/bin/sh
n=0; [ -f "$DBG_COUNT" ] && n=$(cat "$DBG_COUNT")
n=$((n + 1)); printf '%s' "$n" > "$DBG_COUNT"
case "$DBG_MODE" in
  flaky)  [ "$n" -le 2 ] || { printf 'Successfully built abc\n'; exit 0; }
          printf 'ERROR: failed to solve: failed to authorize: dial tcp: lookup auth.docker.io: i/o timeout\n' >&2; exit 1 ;;
  down)   printf 'ERROR: failed to solve: failed to authorize: failed to fetch oauth token: i/o timeout\n' >&2; exit 1 ;;
  code)   printf 'ERROR: failed to solve: Dockerfile parse error line 3: unknown instruction: COPYY\n' >&2; exit 1 ;;
esac
exit 0
DK
chmod +x "$DB/docker"

DLOG=$(mktemp)
# ⚠️ DBG_COUNT / DBG_MODE 必须写在命令**前缀**里：写成 `DBG_COUNT=x out=$(…)` 是
# 「只有赋值的简单命令」，变量不会导出给子进程，假 docker 就读不到（第一版这么写，全红）。
out=$(PATH="$DB:$PATH" DBG_COUNT="$DB/n" DBG_MODE=flaky DOCKER_BUILD_RETRY_BASE=0 docker_build_retrying "$DLOG" -t x . 2>&1); rc=$?
eq '网络抖动：重试之后成功 → 0' '0' "$rc"
eq '网络抖动：一共试了 3 次' '3' "$(cat "$DB/n")"
case "$out" in
  *"秒后重试"*) ok '网络抖动：说了在重试' ;;
  *) bad '网络抖动：说了在重试' '含「秒后重试」' "$out" ;;
esac

out=$(PATH="$DB:$PATH" DBG_COUNT="$DB/n2" DBG_MODE=down DOCKER_BUILD_RETRY_BASE=0 docker_build_retrying "$DLOG" -t x . 2>&1); rc=$?
eq '网络一直不好：重试完发 error（返回 2）' '2' "$rc"
eq '网络一直不好：试满 3 次' '3' "$(cat "$DB/n2")"
case "$out" in
  *"环境未就绪"*) ok '网络一直不好：描述里写清是环境未就绪' ;;
  *) bad '网络一直不好：描述里写清是环境未就绪' '含「环境未就绪」' "$out" ;;
esac
case "$out" in
  *"i/o timeout"*) ok '网络一直不好：原始错误照样打出来' ;;
  *) bad '网络一直不好：原始错误照样打出来' '含 i/o timeout' "$out" ;;
esac

out=$(PATH="$DB:$PATH" DBG_COUNT="$DB/n3" DBG_MODE=code DOCKER_BUILD_RETRY_BASE=0 docker_build_retrying "$DLOG" -t x . 2>&1); rc=$?
eq 'Dockerfile 写错：发 failure（返回 1）' '1' "$rc"
eq 'Dockerfile 写错：**不重试**（只跑 1 次）' '1' "$(cat "$DB/n3")"
case "$out" in
  *"环境未就绪"*) bad 'Dockerfile 写错：不能说成环境未就绪' '不含「环境未就绪」' "$out" ;;
  *) ok 'Dockerfile 写错：不能说成环境未就绪' ;;
esac

# 给「自己 build 镜像的脚本」用的包装（#196 复审意见）：网络问题要 exit 75（EX_TEMPFAIL），
# 别的失败 exit 1（代码红）。在**子壳**里跑 —— 它用 exit，不能把自测本身带走。
#
# ⚠️ 调用方式必须和**真实调用方**一样：`sh -c 'set -eu; …'`。第一版这里是裸的 `bash -c`，
# **没开 set -e** —— 于是 `set -e` 下 `docker_build_retrying …; rc=$?` 当场退出的那个 bug
# 完全测不出来（真跑时 exit 75 根本到不了，退的是 2）。#196 三轮复审。
TMPD=$(mktemp -d)
out=$(PATH="$DB:$PATH" DBG_COUNT="$DB/o1" DBG_MODE=flaky DOCKER_BUILD_RETRY_BASE=0 \
  TMPDIR="$TMPD" sh -c "set -eu; . '$ROOT/scripts/local-ci-lib.sh'; docker_build_or_die -t x ." 2>&1); rc=$?
eq '包装：网络抖动重试后成功 → 0' '0' "$rc"
eq '包装：网络抖动确实重试了（3 次）' '3' "$(cat "$DB/o1")"

out=$(PATH="$DB:$PATH" DBG_COUNT="$DB/o2" DBG_MODE=down DOCKER_BUILD_RETRY_BASE=0 \
  TMPDIR="$TMPD" sh -c "set -eu; . '$ROOT/scripts/local-ci-lib.sh'; docker_build_or_die -t x ." 2>&1); rc=$?
eq '包装：网络一直不好 → 75（EX_TEMPFAIL 专用码，不是笼统的 2）' '75' "$rc"
case "$out" in
  *"环境未就绪"*) ok '包装：说清了这是环境问题' ;;
  *) bad '包装：说清了这是环境问题' '含「环境未就绪」' "$out" ;;
esac
# 失败路径上那个临时日志也要清掉。第一版在 `set -e` 下当场就退出了，`rm -f "$log"` 根本
# 没执行到 —— auditor 报的「临时日志漏了」就是它。
eq '包装：失败时不留临时日志' '' "$(ls "$TMPD" 2>/dev/null | grep '^local-ci-docker' || true)"

# 步骤退出码 → run_context 退出码。#196 复审的阻塞就在这条映射上：
# 原来把 test-deploy.sh 的**任何** exit 2 都当成环境问题，可它跑在 `set -eu` 下，
# 语法错、引用未设置的变量都退 2 —— 那些是代码 bug，被发成 error 就是假绿。
# 这几条等价于「test-deploy.sh 退 X 时 run_context 返回什么」。
step_rc() { docker_step_rc "$1"; }
eq '步骤退 2（set -eu 下的语法错 / 未定义变量）→ 1（代码红，**不是**环境问题）' '1' "$(step_rc 2)"
eq '步骤退 75（拉镜像的网络问题）→ 2（环境未就绪）' '2' "$(step_rc 75)"
eq '步骤退 1 → 1' '1' "$(step_rc 1)"
eq '步骤退 0 → 0' '0' "$(step_rc 0)"
eq '步骤退 3 / 127 之类的怪码 → 1（认不出来的一律当代码问题）' '1' "$(step_rc 3)"
eq '步骤退 126（不能执行）→ 1' '1' "$(step_rc 126)"
eq '75 就是 sysexits 的 EX_TEMPFAIL，别改成一个会被别处用掉的码' '75' "$EX_TEMPFAIL"

out=$(PATH="$DB:$PATH" DBG_COUNT="$DB/o3" DBG_MODE=code DOCKER_BUILD_RETRY_BASE=0 \
  TMPDIR="$TMPD" sh -c "set -eu; . '$ROOT/scripts/local-ci-lib.sh'; docker_build_or_die -t x ." 2>&1); rc=$?
eq '包装：Dockerfile 写错 → 1（编排器发 failure）' '1' "$rc"
eq '包装：Dockerfile 写错不重试' '1' "$(cat "$DB/o3")"

# 环境未就绪那条 status 的描述（#196 复审意见）：有具体原因就写它。
eq '描述：有具体原因就用它' \
  '环境未就绪：拉镜像失败（网络或镜像源）' \
  "$(env_not_ready_desc '拉镜像失败（网络或镜像源）' 'docker build + scripts/container-smoke.sh')"
eq '描述：没有具体原因退回「跑了什么命令」' \
  '环境未就绪：docker build + scripts/container-smoke.sh' \
  "$(env_not_ready_desc '' 'docker build + scripts/container-smoke.sh')"
eq '描述：兜底也要截断到 100 字（GitHub 的 description 很短）' \
  "环境未就绪：$(printf '12345'; printf 'x%.0s' $(seq 1 95))" \
  "$(env_not_ready_desc '' "$(printf '12345'; printf 'x%.0s' $(seq 1 200))")"

# 编排器把 2 发成 error、把 1 发成 failure —— 这是 #193 的落脚点。
eq '编排器：2 → error（环境未就绪）' 'error' "$(status_state_for_rc 2)"
eq '编排器：1 → failure（代码红）' 'failure' "$(status_state_for_rc 1)"
eq '编排器：0 → success' 'success' "$(status_state_for_rc 0)"
eq '编排器：3 → error（脚本自身出错）' 'error' "$(status_state_for_rc 3)"

rm -rf "$DK" "$DB" "$DLOG"

echo '--- e2e 端口按次分配（#228）---'

eq 'e2e-port.sh 单独改动跑 meta + e2e' \
  "$(lines local-ci/meta local-ci/e2e)" \
  "$(printf 'scripts/e2e-port.sh\n' | scope_contexts)"

# 相邻编号（并发起的两路 PID 就是相邻的）窗口不能重叠：只算起点，不探端口。
W7=$(cd "$ROOT" && E2E_RUN=7 sh scripts/e2e-port.sh --window)
W8=$(cd "$ROOT" && E2E_RUN=8 sh scripts/e2e-port.sh --window)
eq 'e2e 窗口：编号 7 的起点' '40056' "$W7"
eq 'e2e 窗口：相邻编号的窗口首尾相接、不重叠' '8' "$((W8 - W7))"
eq 'e2e 窗口：最大编号 1099 停在 49152 以下（避开 macOS 临时端口）' '48792' \
  "$(cd "$ROOT" && E2E_RUN=1099 sh scripts/e2e-port.sh --window)"
eq 'e2e 窗口：编号 0 从 40000 起，不和 restore-drill 的 30000–39999 重叠' '40000' \
  "$(cd "$ROOT" && E2E_RUN=0 sh scripts/e2e-port.sh --window)"

X=$(cd "$ROOT" && E2E_RUN=1100 sh scripts/e2e-port.sh --window 2>&1); rc=$?
eq 'e2e 编号 1100 越界被拒（不绕回窗口 0）' '2' "$rc"
case "$X" in
  *"超出范围"*) ok 'e2e 编号越界：说清了原因' ;;
  *) bad 'e2e 编号越界：说清了原因' '含「超出范围」' "$X" ;;
esac
X=$(cd "$ROOT" && E2E_RUN=abc sh scripts/e2e-port.sh --window 2>&1); rc=$?
eq 'e2e 编号不是数字被拒' '2' "$rc"

# 真探端口的两条用**本次自测进程自己的编号**，不钉死编号 —— 钉死的话本机每次 meta 都去
# 绑同一组口，并发跑时互相撞（#186 复审吃过这个亏）。
# 占口的是一个后台 python：只占给定的口，写出 ready 文件后等着；结束时只杀这一个 PID。
hold_ports() { # $1=ready 文件 $2...=端口；后台运行，调用方自己 kill $!
  python3 - "$@" <<'PY' &
import socket, sys, time
ready, ports = sys.argv[1], [int(p) for p in sys.argv[2:]]
held = []
for p in ports:
    s = socket.socket(); s.bind(("127.0.0.1", p)); s.listen(1); held.append(s)
open(ready, "w").close()
time.sleep(30)
PY
}
wait_ready() { local i=0; while [ ! -e "$1" ] && [ "$i" -lt 50 ]; do sleep 0.1; i=$((i+1)); done; [ -e "$1" ]; }

ERUN=$(( $$ % 1100 ))
ESTART=$(cd "$ROOT" && E2E_RUN=$ERUN sh scripts/e2e-port.sh --window)
EP=$(mktemp -d)

# 窗口前两个口被占：跳过它们，拿第三个（还在自己窗口里）。
hold_ports "$EP/r1" "$ESTART" "$((ESTART + 1))"; HOLD=$!
if wait_ready "$EP/r1"; then
  got=$(cd "$ROOT" && E2E_RUN=$ERUN sh scripts/e2e-port.sh 2>&1); rc=$?
  eq 'e2e 端口：跳过窗口里被占的口' "0 $((ESTART + 2))" "$rc $got"
else
  bad 'e2e 端口：跳过窗口里被占的口' '占口进程就绪' '占不上（这一段已经被别人占了？）'
fi
kill "$HOLD" 2>/dev/null; wait "$HOLD" 2>/dev/null

# 整个窗口被占：报错退出，不越界去抢下一个编号的窗口。
hold_ports "$EP/r2" $(seq "$ESTART" "$((ESTART + 7))"); HOLD=$!
if wait_ready "$EP/r2"; then
  got=$(cd "$ROOT" && E2E_RUN=$ERUN sh scripts/e2e-port.sh 2>&1); rc=$?
  [ "$rc" -ne 0 ] && ok 'e2e 端口：窗口占满时报错退出' \
    || bad 'e2e 端口：窗口占满时报错退出' '非 0' "退出码 ${rc}，输出 ${got}"
  case "$got" in
    *"${ESTART}–$((ESTART + 7))"*) ok 'e2e 端口：报错写清了是哪一段' ;;
    *) bad 'e2e 端口：报错写清了是哪一段' "含 ${ESTART}–$((ESTART + 7))" "$got" ;;
  esac
else
  bad 'e2e 端口：窗口占满时报错退出' '占口进程就绪' '占不上（这一段已经被别人占了？）'
fi
kill "$HOLD" 2>/dev/null; wait "$HOLD" 2>/dev/null
rm -rf "$EP"

# make e2e 真的把分到的端口交给 Playwright；显式给了 E2E_PORT 就不再分配。
MK=$(cd "$ROOT" && make -n e2e 2>/dev/null)
case "$MK" in
  *'scripts/e2e-port.sh'*'E2E_PORT=$port'*) ok 'make e2e：没给 E2E_PORT 时用 e2e-port.sh 分配并传下去' ;;
  *) bad 'make e2e：没给 E2E_PORT 时用 e2e-port.sh 分配并传下去' '含 scripts/e2e-port.sh 和 E2E_PORT=$port' "$MK" ;;
esac
case "$MK" in
  *'${E2E_PORT:-'*) ok 'make e2e：显式给的 E2E_PORT 优先' ;;
  *) bad 'make e2e：显式给的 E2E_PORT 优先' '含 ${E2E_PORT:-' "$MK" ;;
esac

echo
if [ "$fails" -eq 0 ]; then echo "OK: $0 全过"; exit 0; fi
echo "FAILED: $fails 条"; exit 1
