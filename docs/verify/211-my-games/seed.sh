#!/usr/bin/env bash
# #211 走查用的数据：「阿杰」（openid acc-me）有三局要打的、三局历史。
#
# 局都走真接口开、真接口报名、真接口记分；只有「已经开始了」这件事接口做不到
# （开局时间必须在将来），所以开完之后直接改库里的 starts_at，把它挪到过去。
#
#   API=http://127.0.0.1:18811 DB=/tmp/impl-211-run/data/guandan.db docs/verify/211-my-games/seed.sh
set -euo pipefail
API=${API:?API=http://127.0.0.1:端口}
DB=${DB:?DB=服务端数据目录里的 guandan.db}

login() { # openid 称呼 → token
  curl -sf -X POST "$API/api/wx/login" -H 'content-type: application/json' \
    -d "{\"code\":\"dev:$1\",\"nickname\":\"$2\"}" | jq -r .device_token
}
call() { # token 方法 路径 [body]
  curl -sf -X "$2" "$API$3" -H "authorization: Bearer $1" -H 'content-type: application/json' \
    -H "Idempotency-Key: seed-$RANDOM-$RANDOM" ${4:+-d "$4"}
}
open_game() { # token 几小时后 片区 场地 玩法 → 短码
  local at=$(( $(date +%s) + $2 * 3600 ))
  call "$1" POST /api/games "{\"starts_at\":$at,\"area\":\"$3\",\"venue\":\"$4\",\"kind\":\"$5\"}" | jq -r .game.short_code
}
seat() { call "$1" POST "/api/games/$2/seat" >/dev/null; }
to_past() { # 短码 几小时前
  sqlite3 "$DB" "UPDATE games SET starts_at = strftime('%s','now') - $2*3600 WHERE short_code = '$1';"
}
# 开局 6 小时后服务端的清理任务（每 10 分钟一次）会把局收成 done。这里直接做同一件事，
# 不然截图里要等下一轮清理才会从「已开局」变成「已打完」。
finish() { sqlite3 "$DB" "UPDATE games SET status = 'done', ended_at = strftime('%s','now') WHERE short_code = '$1';"; }

ME=$(login acc-me 阿杰)
A=$(login acc-a 老王); B=$(login acc-b 小李); C=$(login acc-c 阿芳)
D=$(login acc-d 大刘); E=$(login acc-e 陈姐); F=$(login acc-f 老周)

# ── 要打的 ──
# 我开的，差 2 人
U1=$(open_game "$ME" 26 SCBD "Pacific Place 3 楼" classic); seat "$A" "$U1"
# 别人开的，我已报名，已成局
U2=$(open_game "$B" 50 Kuningan "Kota Kasablanka" classic); seat "$ME" "$U2"; seat "$C" "$U2"; seat "$D" "$U2"
# 坐满了，我排候补第 1 位
U3=$(open_game "$E" 74 PIK "PIK Avenue" no-level); seat "$A" "$U3"; seat "$C" "$U3"; seat "$F" "$U3"; seat "$ME" "$U3"

# ── 历史 ──
# 打完记过分的（升级局）：我坐 2 号
H1=$(open_game "$F" 3 Senayan "Senayan City" classic); seat "$ME" "$H1"; seat "$B" "$H1"; seat "$D" "$H1"
to_past "$H1" 20
call "$F" POST "/api/games/$H1/score" '{}' >/dev/null
n=1
for r in "1 2" "2 3" "1 3" "3 2" "1 2"; do
  set -- $r
  call "$F" POST "/api/games/$H1/score/rounds" "{\"expected_round_no\":$n,\"head_seat\":$1,\"partner_place\":$2}" >/dev/null
  n=$((n + 1))
done
finish "$H1"
# 我开的、后来取消了
H2=$(open_game "$ME" 4 Kemang "Kemang Village" classic)
call "$ME" POST "/api/games/$H2/cancel" >/dev/null
to_past "$H2" 50
# 不升级局，记过分
H3=$(open_game "$C" 5 BSD "AEON Mall BSD" no-level); seat "$ME" "$H3"; seat "$A" "$H3"; seat "$E" "$H3"
to_past "$H3" 100
call "$C" POST "/api/games/$H3/score" '{}' >/dev/null
n=1
for r in "1 2" "2 3" "4 2"; do
  set -- $r
  call "$C" POST "/api/games/$H3/score/rounds" "{\"expected_round_no\":$n,\"head_seat\":$1,\"partner_place\":$2}" >/dev/null
  n=$((n + 1))
done
finish "$H3"

echo "要打的：$U1 $U2 $U3"
echo "历史：$H1 $H2 $H3"
