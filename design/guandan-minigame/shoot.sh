#!/bin/sh
# 用 headless Chrome 把 prototype.html 的每一屏截成 2 倍图，放到 screenshots/。
# 用法：sh design/guandan-minigame/shoot.sh
set -eu
DIR=$(cd "$(dirname "$0")" && pwd)
CHROME=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
OUT="$DIR/screenshots"
mkdir -p "$OUT"
shot() { # 编号 宽 高
  "$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=2 \
    --window-size="$2,$3" --virtual-time-budget=2000 \
    --screenshot="$OUT/$1.png" "file://$DIR/prototype.html#$1" >/dev/null 2>&1
  echo "$OUT/$1.png"
}
for id in M1 M2 M3 M4 M5 M6 N1 N2 N3 N4 T1 T2 T3; do shot "$id" 390 844; done
for id in F1 F2; do shot "$id" 780 620; done
shot C0 820 400
for id in C1 C2 C3 C4 C5; do shot "$id" 500 400; done
