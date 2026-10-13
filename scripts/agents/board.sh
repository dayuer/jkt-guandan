#!/bin/sh
# Regenerates the auto section of the coordination board (COORDINATION.md §5) from labels and PR
# comments. Everything above the start marker is coord's hand-written part and is kept verbatim.
# Titles and comment text only ever flow through jq and printf '%s', never through eval.
set -eu
REPO="${REPO:-dayuer/jkt-guandan}"
START='<!-- board:auto:start -->'
END='<!-- board:auto:end -->'

board=$(gh issue list --repo "$REPO" --label board --state open --json number --jq '.[0].number // empty')
[ -n "$board" ] || { echo "no open issue labelled board" >&2; exit 1; }

body=$(gh issue view "$board" --repo "$REPO" --json body --jq .body)
manual=$(printf '%s\n' "$body" | awk -v s="$START" 'index($0, s) { exit } { print }')

row='.[] | "- #\(.number) \(.title) `\([.labels[].name | select(startswith("status:") or test("^P[0-9]$") or startswith("phase:"))] | join(" "))`"'
list() { out=$(gh issue list --repo "$REPO" --state open --limit 100 --search "-label:board" "$@" --json number,title,labels --jq "$row"); printf '%s\n' "${out:-- 无}"; }

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
{
  printf '%s\n\n' "$manual"
  printf '%s\n' "$START"
  printf '%s\n\n' "_以下由 \`scripts/agents/board.sh\` 自动生成，请勿手改。_"
  for r in coord impl auditor design; do
    printf '### owner:%s\n' "$r"; list --label "owner:$r"; printf '\n'
  done
  printf '### 待分诊\n'; list --label status:triage; printf '\n'
  printf '### 阻塞\n'; list --label status:blocked; printf '\n'
  printf '### needs-user\n'; list --label needs-user; printf '\n'

  printf '### 合并队列\n'
  prs=$(gh pr list --repo "$REPO" --state open --json number,title,headRefOid,headRefName --jq '.[] | "\(.number)\t\(.headRefOid)\t\(.headRefName)\t\(.title)"')
  if [ -z "$prs" ]; then
    printf '%s\n' '- 无'
  else
    printf '%s\n' "$prs" | while IFS="$(printf '\t')" read -r num head ref title; do
      # COORDINATION.md §4.1: a REVIEW whose agent marker equals the PR body's marker is the author
      # reviewing itself and does not count. Reviews without a marker (the repo owner) always count.
      res=$(gh pr view "$num" --repo "$REPO" --json body,comments --jq '
        def marker: ([capture("<!-- agent: (?<a>[^ ]+) -->")][0].a // "");
        (.body | marker) as $author
        | [.comments[].body
           | select(test("REVIEW: (approve|changes) sha=[0-9a-f]{40}"))
           | capture("REVIEW: (?<v>approve|changes) sha=(?<s>[0-9a-f]{40})") + {a: marker}]
        | (map(select($author == "" or .a != $author)) | last) as $ok
        | (any(.[]; $author != "" and .a == $author)) as $self
        | "\($ok.v // "-") \($ok.s // "-") \(if $self then "self" else "-" end)"')
      verdict=${res%% *}; rest=${res#* }; sha=${rest%% *}; self=${rest#* }
      short=$(printf '%.12s' "$sha")
      if [ "$verdict" = - ]; then state='待复审'
      elif [ "$verdict" = approve ] && [ "$sha" = "$head" ]; then state='✅ approve，与 head 一致'
      elif [ "$verdict" = approve ]; then state="⚠️ approve 的是 $short，head 已变，需重新 REVIEW"
      else state="❌ changes requested ($short)"
      fi
      [ "$self" = self ] && state="$state；⚠️ 含作者自审，无效"
      printf -- '- #%s `%s` head=`%.12s` — %s — %s\n' "$num" "$ref" "$head" "$state" "$title"
    done
  fi
  printf '\n_更新于 %s UTC_\n' "$(date -u '+%Y-%m-%d %H:%M')"
  printf '%s\n' "$END"
} > "$tmp"

gh issue edit "$board" --repo "$REPO" --body-file "$tmp" >/dev/null
echo "board #$board refreshed"
