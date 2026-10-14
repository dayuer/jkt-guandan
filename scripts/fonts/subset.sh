#!/bin/sh
# 重新生成 internal/share/fonts 里的字体子集（OG 图和海报用，#5）。
# 原始字体：Google 官方 notofonts/noto-cjk 仓库的 SubsetOTF/SC，SIL OFL 1.1（见 internal/share/fonts/OFL.txt）。
# 子集：ASCII、Latin-1、常用标点、全角字符、GB2312 全部汉字，加上固定文案里的字（「掼」不在 GB2312 里）。
# 用法：scripts/fonts/subset.sh <放原始 .otf 的目录>   需要 pyftsubset（pip install fonttools）
set -eu
SRC=${1:?"用法：scripts/fonts/subset.sh <原始字体目录>"}
DIR=$(cd "$(dirname "$0")" && pwd)
OUT="$DIR/../../internal/share/fonts"
unicodes=$(mktemp)
trap 'rm -f "$unicodes"' EXIT
python3 -I "$DIR/chars.py" > "$unicodes"
for f in NotoSansSC-Bold NotoSerifSC-Black; do
  pyftsubset "$SRC/$f.otf" --unicodes-file="$unicodes" --output-file="$OUT/$f.subset.otf" \
    --layout-features='*' --no-hinting
done
ls -la "$OUT"
