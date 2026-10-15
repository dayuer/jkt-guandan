package games_test

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/secheck"
)

// countingChecker 是开发模式的假检测器外加计数（只数场景 3），看哪些请求真的送检了。
type countingChecker struct {
	mu    sync.Mutex
	texts []string
}

func (c *countingChecker) Check(ctx context.Context, openid string, scene int, content string) (secheck.Verdict, error) {
	// 登录时的称呼是场景 1，不算；地点、备注都应是场景 3。
	if scene == secheck.SceneForum {
		c.mu.Lock()
		c.texts = append(c.texts, content)
		c.mu.Unlock()
	}
	return secheck.DevChecker{}.Check(ctx, openid, scene, content)
}

func (c *countingChecker) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.texts)
}

// newSecFixture 接上小游戏登录（dev:<openid>）和内容安全检测，返回一个小游戏用户的 Bearer 头。
func newSecFixture(t *testing.T) (*srvFixture, *countingChecker, map[string]string) {
	t.Helper()
	f := newServer(t)
	f.ident.SetWx(identity.DevWxAppID, identity.DevWxExchanger{})
	cc := &countingChecker{}
	guard := &secheck.Guard{Checker: cc, AppID: identity.DevWxAppID, Store: f.st}
	f.ident.SetContentGuard(guard)
	f.games.SetContentGuard(guard)

	r := f.do(f.newBrowser(), http.MethodPost, "/api/wx/login", map[string]string{"code": "dev:open-1", "nickname": "老王"}, nil)
	tok := r.str("device_token")
	if r.status != http.StatusOK || tok == "" {
		t.Fatalf("小游戏登录：%d %s", r.status, r.raw)
	}
	return f, cc, map[string]string{"Authorization": "Bearer " + tok}
}

func (f *srvFixture) gameBody(venue, note string) map[string]any {
	return map[string]any{
		"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(), "area": "SCBD", "venue": venue, "kind": "classic", "note": note,
	}
}

// #146：小游戏用户开局、改局时地点和备注送微信检测，risky 返回 422 sensitive_word + field。
func TestContentCheckCreateAndUpdate(t *testing.T) {
	f, cc, mini := newSecFixture(t)
	c := f.newBrowser()

	r := f.do(c, http.MethodPost, "/api/games", f.gameBody("Pacific Place", "带上"+secheck.DevRiskyWord), mini)
	if r.status != http.StatusUnprocessableEntity || r.errCode() != "sensitive_word" || r.errField() != "note" {
		t.Fatalf("备注应被拦：%d %s", r.status, r.raw)
	}
	r = f.do(c, http.MethodPost, "/api/games", f.gameBody(secheck.DevRiskyWord, ""), mini)
	if r.status != http.StatusUnprocessableEntity || r.errField() != "venue" {
		t.Fatalf("地点应被拦：%d %s", r.status, r.raw)
	}
	r = f.do(c, http.MethodPost, "/api/games", f.gameBody("Pacific Place", "八点开打"), mini)
	if r.status != http.StatusOK {
		t.Fatalf("应能开局：%d %s", r.status, r.raw)
	}
	code := r.gameField("short_code").(string)
	version := r.gameField("version").(float64)
	before := cc.n()

	patch := func(body map[string]any) resp {
		h := map[string]string{"If-Match": strconv.FormatInt(int64(version), 10)}
		for k, v := range mini {
			h[k] = v
		}
		return f.do(c, http.MethodPatch, "/api/games/"+code, body, h)
	}
	r = patch(map[string]any{"note": "改成" + secheck.DevRiskyWord})
	if r.status != http.StatusUnprocessableEntity || r.errField() != "note" {
		t.Fatalf("改备注应被拦：%d %s", r.status, r.raw)
	}
	r = patch(map[string]any{"venue": "Senayan City"})
	if r.status != http.StatusOK {
		t.Fatalf("改地点应通过：%d %s", r.status, r.raw)
	}
	if got := cc.n() - before; got != 2 {
		t.Fatalf("两次改局应各送检一次，实际 %d", got)
	}

	// 不是局头的人改局：不送检，照旧 403。
	other := f.do(f.newBrowser(), http.MethodPost, "/api/wx/login", map[string]string{"code": "dev:open-2", "nickname": "小李"}, nil)
	before = cc.n()
	h := map[string]string{"If-Match": "1", "Authorization": "Bearer " + other.str("device_token")}
	r = f.do(f.newBrowser(), http.MethodPatch, "/api/games/"+code, map[string]any{"note": "x"}, h)
	if r.status != http.StatusForbidden || cc.n() != before {
		t.Fatalf("非局头不该送检：%d %s，送检 %d 次", r.status, r.raw, cc.n()-before)
	}
}

// H5 用户（没有小游戏 openid）不送检：一期的本地词表照旧管着地点、备注。
func TestContentCheckSkipsH5User(t *testing.T) {
	f, cc, _ := newSecFixture(t)
	f.signUp(f.http, "网页用户")
	r := f.do(f.http, http.MethodPost, "/api/games", f.gameBody("Pacific Place", secheck.DevRiskyWord), nil)
	if r.status != http.StatusOK {
		t.Fatalf("H5 用户不该被微信检测拦：%d %s", r.status, r.raw)
	}
	if cc.n() != 0 {
		t.Fatalf("H5 用户不该送检：%d 次", cc.n())
	}
}
