#!/bin/sh
# Creates or updates the labels the coordination protocol uses (docs/agents/COORDINATION.md).
# Idempotent: re-running it only refreshes colours and descriptions.
set -eu
REPO="${REPO:-dayuer/jkt-guandan}"
label() { gh label create "$1" --repo "$REPO" --color "$2" --description "$3" --force >/dev/null; echo "label $1"; }

label "type:finding"     "d73a4a" "审计发现"
label "type:task"        "0e8a16" "可认领的任务"
label "type:decision"    "5319e7" "需要仓库所有者拍板"
label "P0"               "b60205" "合并或进入下一阶段前必须修"
label "P1"               "d93f0b" "本阶段内修"
label "P2"               "fbca04" "排进计划"
label "status:triage"    "ededed" "待 coord 分诊"
label "status:accepted"  "c2e0c6" "已接受，待认领"
label "status:claimed"   "bfdadc" "已认领，进行中"
label "status:in-review" "0052cc" "修复已提 PR，复审中"
label "status:verified"  "0e8a16" "修复已验证"
label "status:rejected"  "cccccc" "不接受（理由见评论）"
label "status:blocked"   "000000" "被阻塞（原因见评论）"
label "owner:coord"      "1d76db" "协调方"
label "owner:impl"       "1d76db" "实现方"
label "owner:auditor"    "1d76db" "审计方"
label "owner:design"     "1d76db" "设计稿与文案"
label "phase:1"          "c5def5" "微信内报名、身份、分享、日历；设计改版"
label "phase:2"          "c5def5" "Web Push、引导屏、WhatsApp OG"
label "phase:3"          "c5def5" "蛋蛋 Agent、牌友、学牌"
label "needs-user"       "e99695" "等仓库所有者决定"
label "board"            "000000" "协调看板（仅一个）"
