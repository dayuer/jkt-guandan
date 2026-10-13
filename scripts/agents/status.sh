#!/bin/sh
# Moves an issue to a new status: drops every other status:* label, adds the new one, and
# optionally posts a comment (e.g. "CLAIM", "BLOCKED: 等域名"). Usage:
#   scripts/agents/status.sh <issue> <triage|accepted|claimed|in-review|verified|rejected|blocked> [comment]
set -eu
REPO="${REPO:-dayuer/jkt-guandan}"
[ $# -ge 2 ] || { echo "usage: $0 <issue> <status> [comment]" >&2; exit 2; }
n=$1; new="status:${2#status:}"
case "$new" in
  status:triage|status:accepted|status:claimed|status:in-review|status:verified|status:rejected|status:blocked) ;;
  *) echo "unknown status: $new" >&2; exit 2 ;;
esac
old=$(gh issue view "$n" --repo "$REPO" --json labels --jq '[.labels[].name | select(startswith("status:"))] | map(select(. != "'"$new"'")) | join(",")')
if [ -n "$old" ]; then
  gh issue edit "$n" --repo "$REPO" --remove-label "$old" --add-label "$new" >/dev/null
else
  gh issue edit "$n" --repo "$REPO" --add-label "$new" >/dev/null
fi
echo "#$n -> $new${old:+ (removed $old)}"
if [ $# -ge 3 ]; then
  gh issue comment "$n" --repo "$REPO" --body "$3" >/dev/null
  echo "#$n commented"
fi
