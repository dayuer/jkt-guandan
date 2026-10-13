#!/bin/sh
# Read-only snapshot to run at the start of every round of work (COORDINATION.md §6).
# Usage: scripts/agents/sync.sh <coord|impl|auditor|design>
set -eu
REPO="${REPO:-dayuer/jkt-guandan}"
role="${1:-}"
case "$role" in coord|impl|auditor|design) ;; *) echo "usage: $0 <coord|impl|auditor|design>" >&2; exit 2 ;; esac
row='.[] | "  #\(.number)  \(.title)  [\([.labels[].name | select(startswith("status:") or startswith("P") or startswith("phase:"))] | join(" "))]"'

board=$(gh issue list --repo "$REPO" --label board --state open --json number --jq '.[0].number // empty')
echo "== 协调看板 #${board:-?}"
[ -n "$board" ] && gh issue view "$board" --repo "$REPO" --json body --jq .body
echo
echo "== owner:$role 名下打开的 issue"
gh issue list --repo "$REPO" --label "owner:$role" --state open --limit 100 --json number,title,labels --jq "$row"
if [ "$role" = coord ]; then
  echo; echo "== 待分诊 status:triage"
  gh issue list --repo "$REPO" --label status:triage --state open --limit 100 --json number,title,labels --jq "$row"
fi
echo
echo "== 打开的 PR"
gh pr list --repo "$REPO" --state open --json number,title,headRefName,headRefOid \
  --jq '.[] | "  #\(.number)  \(.headRefName)  head=\(.headRefOid[0:12])  \(.title)"'
echo
echo "== needs-user"
gh issue list --repo "$REPO" --label needs-user --state open --json number,title,labels --jq "$row"
