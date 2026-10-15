#!/bin/sh
# 重新生成 server/internal/share/fonts 里的字体子集（OG 图和海报用，#5、#54）。
#
# 原始字体：Google 官方 notofonts/noto-cjk 仓库，固定在下面的提交上，SIL OFL 1.1（见 server/internal/share/fonts/OFL.txt）：
#   https://raw.githubusercontent.com/notofonts/noto-cjk/$UPSTREAM/Sans/SubsetOTF/SC/NotoSansSC-Bold.otf
#   https://raw.githubusercontent.com/notofonts/noto-cjk/$UPSTREAM/Serif/SubsetOTF/SC/NotoSerifSC-Black.otf
# 子集：ASCII、Latin-1、常用标点、全角字符、GB2312 全部汉字、Big5 一级常用字（繁体），
# 加上固定文案里的字（「掼」不在 GB2312 里）。码位表由 chars.py 生成。
#
# 用法：scripts/fonts/subset.sh <放原始 .otf 的目录>
# 需要 pyftsubset（pip install fonttools==4.63.0；换版本产物可能逐字节不同）。
# 脚本先核对原始字体的 sha256，跑完再核对产物的 sha256（server/internal/share/fonts/SHA256SUMS）。
set -eu
UPSTREAM=f8d157532fbfaeda587e826d4cd5b21a49186f7c
SRC=${1:?"用法：scripts/fonts/subset.sh <原始字体目录>"}
DIR=$(cd "$(dirname "$0")" && pwd)
OUT="$DIR/../../server/internal/share/fonts"

echo "核对原始字体（notofonts/noto-cjk@${UPSTREAM}）的 sha256"
(cd "$SRC" && shasum -a 256 -c - <<SUMS
c6cb5a93abaa9edc8ee7463b7ebb7f42d618d40e6ed2f7a5371c97b0b64767c0  NotoSansSC-Bold.otf
91dde57622845beeb663f300813999febdb7fcd5e451bde199d7c954a9740cb4  NotoSerifSC-Black.otf
SUMS
)

unicodes=$(mktemp)
trap 'rm -f "$unicodes"' EXIT
python3 -I "$DIR/chars.py" > "$unicodes"
for f in NotoSansSC-Bold NotoSerifSC-Black; do
  pyftsubset "$SRC/$f.otf" --unicodes-file="$unicodes" --output-file="$OUT/$f.subset.otf" \
    --layout-features='*' --no-hinting
done
ls -la "$OUT"
(cd "$OUT" && shasum -a 256 -c SHA256SUMS)
