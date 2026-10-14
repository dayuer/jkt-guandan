package identity_test

import (
	"net/http"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/identity"
)

// #116：号码按 E.164 校验，服务端自己整理，不信前端。
func TestNormalizeWAPhone(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"+6281234567890", "+6281234567890", true},
		{" +62 812-3456-7890 ", "+6281234567890", true},
		{"+62 (812) 3456.7890", "+6281234567890", true},
		{"006281234567890", "+6281234567890", true}, // 00 当作 +
		{"+8613800138000", "+8613800138000", true},
		{"081234567890", "", false},      // 本地写法：不猜区号
		{"6281234567890", "", false},     // 没有 +
		{"+0812345678", "", false},       // 区号不能以 0 开头
		{"+62812", "", false},            // 太短
		{"+6281234567890123", "", false}, // 超过 15 位
		{"+62 812 abc 7890", "", false},
		{"", "", false},
	} {
		got, err := identity.NormalizeWAPhone(tc.in)
		if tc.ok != (err == nil) || got != tc.want {
			t.Errorf("NormalizeWAPhone(%q) = %q, %v；期望 %q, ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestBindWhatsApp(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	// 没绑定：GET /api/me 里没有 wa_phone 这个字段。
	if me := f.do(http.MethodGet, "/api/me", nil); me.status != http.StatusOK || me.body["wa_phone"] != nil {
		t.Fatalf("没绑定时不该有 wa_phone：%d %s", me.status, me.raw)
	}
	// 没绑定时解绑也是 204。
	if r := f.do(http.MethodDelete, "/api/me/wa", nil); r.status != http.StatusNoContent {
		t.Fatalf("没绑定时解绑应 204：%d %s", r.status, r.raw)
	}

	// 不合法：400 invalid_input，带说明。
	r := f.do(http.MethodPut, "/api/me/wa", map[string]string{"phone": "0812 3456 7890"})
	if r.status != http.StatusBadRequest || r.errCode() != "invalid_input" {
		t.Fatalf("本地写法应 400 invalid_input：%d %s", r.status, r.raw)
	}

	// 绑定：服务端整理成 E.164 后返回。
	r = f.do(http.MethodPut, "/api/me/wa", map[string]string{"phone": "+62 812-3456-7890"})
	if r.status != http.StatusOK || r.str("wa_phone") != "+6281234567890" {
		t.Fatalf("绑定：%d %s", r.status, r.raw)
	}
	if me := f.do(http.MethodGet, "/api/me", nil); me.str("wa_phone") != "+6281234567890" {
		t.Fatalf("GET /api/me 应带上号码：%s", me.raw)
	}

	// 号码只给自己看：另一个人的 /api/me 里没有。
	other := f.newBrowser()
	f.createIdentity(other, "Lily")
	if me := f.doWith(other, http.MethodGet, "/api/me", nil); me.body["wa_phone"] != nil {
		t.Fatalf("别人的 /api/me 里不该有我的号码：%s", me.raw)
	}

	// 解绑。
	if r := f.do(http.MethodDelete, "/api/me/wa", nil); r.status != http.StatusNoContent {
		t.Fatalf("解绑：%d %s", r.status, r.raw)
	}
	if me := f.do(http.MethodGet, "/api/me", nil); me.body["wa_phone"] != nil {
		t.Fatalf("解绑后不该有 wa_phone：%s", me.raw)
	}

	// 没有身份：401。
	guest := f.newBrowser()
	if r := f.doWith(guest, http.MethodPut, "/api/me/wa", map[string]string{"phone": "+6281234567890"}); r.status != http.StatusUnauthorized {
		t.Fatalf("访客应 401：%d", r.status)
	}
}

// 改号码有频率限制：每个用户每小时 10 次（绑定、解绑都算）。
func TestBindWhatsAppRateLimited(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")
	for i := 0; i < 10; i++ {
		if r := f.do(http.MethodPut, "/api/me/wa", map[string]string{"phone": "+6281234567890"}); r.status != http.StatusOK {
			t.Fatalf("第 %d 次：%d %s", i+1, r.status, r.raw)
		}
	}
	if r := f.do(http.MethodDelete, "/api/me/wa", nil); r.status != http.StatusTooManyRequests || r.errCode() != "rate_limited" {
		t.Fatalf("超过频率应 429 rate_limited：%d %s", r.status, r.raw)
	}
}
