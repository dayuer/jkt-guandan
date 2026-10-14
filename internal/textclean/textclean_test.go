package textclean

import "testing"

func TestClean(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"零宽字符", "\u200b\u200c\u200d\u200e\u200f\ufeff", ""},
		{"RLO 包裹", "\u202etxet\u202c", "txet"},
		{"隔离符", "\u2066a\u2067b\u2068c\u2069", "abc"},
		{"混合正常文字", "老\u200b王\u202e 的牌局\n", "老王 的牌局"},
		{"中文", "朗诵 Café 北京", "朗诵 Café 北京"},
		{"emoji", "🂡 打牌🎉", "🂡 打牌🎉"},
		{"emoji 连接序列保留", "👨\u200d👩\u200d👧", "👨\u200d👩\u200d👧"},
		{"彩虹旗", "\U0001f3f3\ufe0f\u200d\U0001f308", "\U0001f3f3\ufe0f\u200d\U0001f308"},
		{"心火", "\u2764\ufe0f\u200d\U0001f525", "\u2764\ufe0f\u200d\U0001f525"},
		{"情侣", "\U0001f469\u200d\u2764\ufe0f\u200d\U0001f468", "\U0001f469\u200d\u2764\ufe0f\u200d\U0001f468"},
		{"肤色加 ZWJ", "\U0001f469\U0001f3fd\u200d\U0001f4bb", "\U0001f469\U0001f3fd\u200d\U0001f4bb"},
		{"苏格兰旗标签", "\U0001f3f4\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f", "\U0001f3f4\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f"},
		{"孤立标签去掉", "a\U000e0067b", "ab"},
		{"文字后 ZWJ 再接 emoji 去掉", "a\u200d\U0001f600", "a\U0001f600"},
		{"首尾孤立 ZWJ 去掉", "\u200da\u200d", "a"},
		{"控制字符", "a\x00b\tc\u0085", "abc"},
		{"全空白", " \u200b 　", ""},
		{"空串", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Clean(c.in); got != c.want {
				t.Errorf("Clean(%q) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}
