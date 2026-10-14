# 打印字体子集要保留的 Unicode 码位（逗号分隔），给 pyftsubset --unicodes-file 用。
import sys
cps=set(range(0x20,0x7f))|set(range(0xa0,0x100))|set(range(0x2000,0x2070))|set(range(0x3000,0x3040))|set(range(0xff00,0xfff0))
for b1 in range(0xa1,0xf8):
    for b2 in range(0xa1,0xff):
        try:
            cps.add(ord(bytes([b1,b2]).decode('gb2312')))
        except Exception:
            pass
# Big5 一级常用字（0xA440–0xC67E，5401 字，以教育部《常用國字標準字體表》4808 字为基础）：
# 繁体的店名、姓氏不能被悄悄丢掉（#54 复审，coord 裁定方案 A）。和 GB2312 重合的字只算一次。
for code in range(0xa440, 0xc67f):
    b1, b2 = code >> 8, code & 0xff
    if not (0x40 <= b2 <= 0x7e or 0xa1 <= b2 <= 0xfe):
        continue
    try:
        cps.add(ord(bytes([b1, b2]).decode('big5')))
    except Exception:
        pass
# 固定文案里的字（掼 不在 GB2312 里）
fixed="随时掼掼蛋·雅加达扫码占座纯娱乐，不设彩头差人已成局取消结束今天明天后天周一二三四五六日局头"
cps|=set(ord(c) for c in fixed)
print(",".join("U+%04X"%c for c in sorted(cps)))
