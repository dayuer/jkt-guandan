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
