package identity_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/secheck"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// #210：改称呼。校验和新建身份同一套（NormalizeNickname），检测也是同一个场景。
func TestChangeNickname(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	// 顺带把首尾空白去掉，和小游戏新建身份时一致。
	r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": "  老张  "})
	if r.status != http.StatusOK || r.userField("nickname") != "老张" {
		t.Fatalf("改称呼：%d %s", r.status, r.raw)
	}
	// 返回体里的 user 要能直接刷前端缓存，不用再 GET 一次。
	if r.userField("id") == "" {
		t.Fatalf("返回体里要带 user.id：%s", r.raw)
	}
	if me := f.do(http.MethodGet, "/api/me", nil); me.userField("nickname") != "老张" {
		t.Fatalf("GET /api/me 应当是新称呼：%s", me.raw)
	}

	// 长度沿用同一套规则：1–12 个字符，按字符数算（一个汉字一个）。
	for _, bad := range []string{"", "   ", strings.Repeat("十", identity.NicknameMax+1)} {
		r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": bad})
		if r.status != http.StatusBadRequest || r.errCode() != "nickname_invalid" {
			t.Fatalf("不合法的称呼 %q 应 400 nickname_invalid：%d %s", bad, r.status, r.raw)
		}
	}
	if r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": strings.Repeat("十", identity.NicknameMax)}); r.status != http.StatusOK {
		t.Fatalf("%d 个字符应当可以：%d %s", identity.NicknameMax, r.status, r.raw)
	}

	// 请求体不是 JSON 也是 400，不是 500。
	if r := f.do(http.MethodPut, "/api/me/nickname", "不是 JSON"); r.status != http.StatusBadRequest || r.errCode() != "bad_request" {
		t.Fatalf("坏请求体应 400 bad_request：%d %s", r.status, r.raw)
	}

	// 没有身份：401。
	guest := f.newBrowser()
	if r := f.doWith(guest, http.MethodPut, "/api/me/nickname", map[string]string{"nickname": "谁"}); r.status != http.StatusUnauthorized {
		t.Fatalf("访客应 401：%d", r.status)
	}
}

// 注销之后座位显示成「已注销牌友」；允许改这个名字就等于可以冒充注销的人。
func TestChangeNicknameRejectsReserved(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	// 去掉首尾空白/零宽字符之后仍然命中，也要拒。
	for _, bad := range []string{store.DeletedNickname, " " + store.DeletedNickname + " "} {
		r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": bad})
		if r.status != http.StatusBadRequest || r.errCode() != "nickname_reserved" {
			t.Fatalf("%q 应 400 nickname_reserved：%d %s", bad, r.status, r.raw)
		}
	}
	if me := f.do(http.MethodGet, "/api/me", nil); me.userField("nickname") != "老王" {
		t.Fatalf("被拒之后称呼不该变：%s", me.raw)
	}
	// 「已注销牌友」只是保留名，别的名字照常。
	if r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": "已注销"}); r.status != http.StatusOK {
		t.Fatalf("只是前缀相同的名字应当允许：%d %s", r.status, r.raw)
	}
}

// 注销过的人不能改回来。注销会删掉设备和微信关联，正常认不出身份；这里直接调服务，
// 模拟「设备上还留着 cookie」的情况 —— 那种情况下 must 先查 deleted_at 再放行。
func TestChangeNicknameRejectsDeletedUser(t *testing.T) {
	f := newFixture(t, false)
	userID := f.createIdentity(f.http, "老王")
	if r := f.do(http.MethodDelete, "/api/me", nil); r.status != http.StatusNoContent {
		t.Fatalf("注销：%d %s", r.status, r.raw)
	}

	// 注销之后设备、微信关联都删了，正常认不出身份（401）。这里直接调服务层，
	// 模拟「某台设备上的 cookie 还没来得及清」的情况 —— 那时候必须靠 deleted_at 拦住。
	if _, err := f.svc.ChangeNickname(context.Background(), userID, "新名字"); err != identity.ErrUserDeleted {
		t.Fatalf("注销后应当拒：%v", err)
	}
	// 没这个用户：ErrNotFound（不是 500）。
	if _, err := f.svc.ChangeNickname(context.Background(), "没有这个人", "新名字"); err != identity.ErrNotFound {
		t.Fatalf("不存在的用户应当 ErrNotFound：%v", err)
	}
	// 注销时写的「已注销牌友」不能被覆盖。
	var got string
	if err := f.st.Read().QueryRow(`SELECT nickname FROM users WHERE id = ?`, userID).Scan(&got); err != nil {
		t.Fatalf("查称呼: %v", err)
	}
	if got != store.DeletedNickname {
		t.Fatalf("称呼应当还是 %q，实际 %q", store.DeletedNickname, got)
	}
}

// 小游戏用户改称呼也要过内容安全检测（场景 1 资料），和新建身份时一致。
func TestChangeNicknameChecksContent(t *testing.T) {
	f := newFixture(t, false)
	f.svc.SetContentGuard(&secheck.Guard{Checker: secheck.DevChecker{}, AppID: testAppID, Store: f.st})

	// 这个用户没有小游戏的 openid（H5 测试用户）→ Guard 跳过，放行。
	userID := f.createIdentity(f.http, "老王")
	if r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": "好名字"}); r.status != http.StatusOK {
		t.Fatalf("没有 openid 的用户应当放行：%d %s", r.status, r.raw)
	}

	// 给这个用户挂上小游戏 openid，再来一次：带敏感词的被拒。
	f.exec(`INSERT INTO user_links (provider, subject, user_id, created_at) VALUES (?, ?, ?, ?)`,
		"wx_openid:"+testAppID, "open-1", userID, f.clk.Now().Unix())
	r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": "老王" + secheck.DevRiskyWord})
	if r.status != http.StatusUnprocessableEntity || r.errCode() != "sensitive_word" {
		t.Fatalf("敏感词应 422 sensitive_word：%d %s", r.status, r.raw)
	}
	// 错误体里要带 field，前端才知道高亮哪个输入框（和新建身份同一个错误体）。
	if e, _ := r.body["error"].(map[string]any); e["field"] != "nickname" {
		t.Fatalf("错误体里应当带 field=nickname：%s", r.raw)
	}
	// 换一个干净的名字：放行，而且称呼真的改了。
	if r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": "老张"}); r.status != http.StatusOK {
		t.Fatalf("干净的名字应当放行：%d %s", r.status, r.raw)
	}
	if me := f.do(http.MethodGet, "/api/me", nil); me.userField("nickname") != "老张" {
		t.Fatalf("称呼应当改成老张：%s", me.raw)
	}
}

// 改称呼有频率限制：每个用户每小时 10 次。
func TestChangeNicknameRateLimited(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")
	for i := 0; i < 10; i++ {
		if r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": "名字" + string(rune('a'+i))}); r.status != http.StatusOK {
			t.Fatalf("第 %d 次：%d %s", i+1, r.status, r.raw)
		}
	}
	if r := f.do(http.MethodPut, "/api/me/nickname", map[string]string{"nickname": "再来"}); r.status != http.StatusTooManyRequests || r.errCode() != "rate_limited" {
		t.Fatalf("超过频率应 429 rate_limited：%d %s", r.status, r.raw)
	}

	// 限的是「每个用户」，不是全局：换个人照样能改。
	other := f.newBrowser()
	f.createIdentity(other, "Lily")
	if r := f.doWith(other, http.MethodPut, "/api/me/nickname", map[string]string{"nickname": "Lily 2"}); r.status != http.StatusOK {
		t.Fatalf("另一个人不该受影响：%d %s", r.status, r.raw)
	}
}

// #221：保留称呼（「已注销牌友」）在**新建**这条路上也要挡。之前只有改称呼挡了，
// 于是可以新建一个同名身份去冒充注销的人。
func TestReservedNicknameRejectedEverywhere(t *testing.T) {
	// 规范化之后命中才算数：前后空白、中间的零宽字符都要绕不过去。
	bad := []string{
		store.DeletedNickname,
		" " + store.DeletedNickname + " ",
		"已注销\u200b牌友",
		"\u200b已注销牌友\u200b",
	}

	// ① 纯函数这一支（三条路都走它）。
	for _, in := range bad {
		if got, err := identity.NormalizeNickname(in); err != identity.ErrNicknameReserved {
			t.Errorf("NormalizeNickname(%q) = %q, %v；期望 ErrNicknameReserved", in, got, err)
		}
	}
	// 只是前缀相同的名字照常能用。
	if got, err := identity.NormalizeNickname("已注销"); err != nil || got != "已注销" {
		t.Errorf("「已注销」应当允许：%q %v", got, err)
	}
	// 长度检查和保留名检查互不影响：只有空白还是不合法，不是「不能用」。
	if _, err := identity.NormalizeNickname("   "); err != identity.ErrNicknameInvalid {
		t.Errorf("只有空白应当还是 ErrNicknameInvalid，得到 %v", err)
	}

	// ② H5 新建身份（POST /api/me）。
	f := newFixture(t, false)
	for _, in := range bad {
		r := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": in})
		if r.status != http.StatusBadRequest || r.errCode() != "nickname_reserved" {
			t.Fatalf("H5 新建用 %q 应 400 nickname_reserved：%d %s", in, r.status, r.raw)
		}
	}
	if n := f.countRows(`SELECT count(*) FROM users`); n != 0 {
		t.Fatalf("被拒时不该建用户，实际 %d 个", n)
	}
	if n := f.countRows(`SELECT count(*) FROM devices`); n != 0 {
		t.Fatalf("被拒时不该建设备，实际 %d 台", n)
	}
	// 换个名字照常能建。
	if r := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "老王"}); r.status != http.StatusOK {
		t.Fatalf("普通称呼应当能建：%d %s", r.status, r.raw)
	}

	// ③ 小游戏新建身份（POST /api/wx/login）。
	f2, wx := newWxFixture(t)
	for i, in := range bad {
		code, openid := fmt.Sprintf("c%d", i), fmt.Sprintf("open-%d", i)
		wx.add(code, openid, "")
		// 这里不能走 f.wxLogin：那个 helper 自己要求 200，被拒时它会先把用例判死。
		r := f2.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": code, "nickname": in}, nil)
		if r.status != http.StatusBadRequest || r.errCode() != "nickname_reserved" {
			t.Fatalf("小游戏新建用 %q 应 400 nickname_reserved：%d %s", in, r.status, r.raw)
		}
	}
	if n := f2.countRows(`SELECT count(*) FROM users`); n != 0 {
		t.Fatalf("小游戏被拒时不该建用户，实际 %d 个", n)
	}
	// 同一个微信换个名字：正常建成。
	wx.add("ok", "open-ok", "")
	if r := f2.wxLogin("ok", "老王"); r.status != http.StatusOK {
		t.Fatalf("普通称呼应当能建：%d %s", r.status, r.raw)
	}
}
