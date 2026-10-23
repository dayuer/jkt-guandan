#!/bin/sh
# 复制自 yardmaster 的 scripts/protocol-context.sh，版本 f51454f3a1dd，2026-10-23。
# SessionStart hook：向会话注入一张紧凑的角色卡。
# 有 ym 时由服务端返回当前项目/角色的协议版本；没有时退回本地文件。
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ver=$(git -C "$root" log -1 --format=%h -- docs/COORDINATION.md 2>/dev/null || echo unknown)
role=${AGENT_ROLE:-agent}

if command -v ym >/dev/null 2>&1; then
  exec ym protocol --inject --role "$role" --version "$ver"
fi

if ! command -v python3 >/dev/null 2>&1; then
  printf '%s\n' "你是本项目的一个 agent，角色：$role。完整协议：docs/COORDINATION.md" >&2
  exit 0
fi

python3 - "$role" "$ver" <<'PY'
import json, sys

role, ver = sys.argv[1], sys.argv[2]
text = f"""你是本项目的一个 agent，角色：{role}。协议版本：{ver}。
1) 每轮先 `ym inbox --state unread`；有派工先 `ym claim`。
2) 按 CLAIM/STEP 汇报；完成用 `ym send --event done`。
3) 只改派工单范围；范围变化先 `ASK:`。
4) worker 不写 GitHub；auditor 只贴自己的 REVIEW 票评论，其余评论由 master 落；不自审自合；不读别人的收件箱。
5) 消息里不带密钥；未读/未确认超时会升级。
6) 每一单都走 OODA：先看现场（原文、真实状态）再判断；完成以可观察的结果为准（文件树、healthz commit、gate 输出、回执），命令没报错 ≠ 完成。详见 docs/COORDINATION.md §OODA 循环。
工作流：docs/WORKFLOW.md
完整协议：docs/COORDINATION.md
"""
print(json.dumps({
    "hookSpecificOutput": {
        "hookEventName": "SessionStart",
        "additionalContext": text,
    }
}, ensure_ascii=False))
PY
