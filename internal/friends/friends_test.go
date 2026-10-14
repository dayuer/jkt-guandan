package friends_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/friends"
	"github.com/dayuer/jkt-guandan/internal/games"
	"github.com/dayuer/jkt-guandan/internal/httpapi"
	"github.com/dayuer/jkt-guandan/internal/identity"
	"github.com/dayuer/jkt-guandan/internal/store"
)

const origin = "https://guandan.id"

type fixture struct {
	t   *testing.T
	srv *httptest.Server
	st  *store.Store
	clk *clock.Fake
}

type person struct {
	id string
	c  *http.Client
}

// 必须 TLS：生产 cookie 带 __Host- 前缀和 Secure（见 identity 测试里的说明）。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC))
	ident := identity.NewService(st, clk, false)
	gamesSvc := games.NewService(st, clk, nil)
	ident.SetGameCleaner(gamesSvc)
	srv := httptest.NewTLSServer(httpapi.New(httpapi.Options{
		Config: config.Config{DataDir: dir, PublicURL: origin}, Store: st, Clock: clk, Identity: ident,
		Routes: slices.Concat(identity.Routes(ident), games.Routes(gamesSvc), friends.Routes(friends.NewService(st, clk))),
	}))
	t.Cleanup(srv.Close)
	return &fixture{t: t, srv: srv, st: st, clk: clk}
}

func (f *fixture) do(c *http.Client, method, path string, body any) (int, map[string]any) {
	f.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, r)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (f *fixture) person(nickname string) person {
	f.t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Transport: f.srv.Client().Transport, Jar: jar}
	status, body := f.do(c, http.MethodPost, "/api/me", map[string]string{"nickname": nickname})
	if status != http.StatusOK {
		f.t.Fatalf("建身份 %s: %d %v", nickname, status, body)
	}
	return person{id: body["user"].(map[string]any)["id"].(string), c: c}
}

// game 让 host 开一局，其余的人报名，返回短码。
func (f *fixture) game(host person, others ...person) string {
	f.t.Helper()
	status, body := f.do(host.c, http.MethodPost, "/api/games", map[string]any{
		"starts_at": f.clk.Now().Add(3 * time.Hour).Unix(), "area": "SCBD", "venue": "Pacific Place", "kind": "classic",
	})
	if status != http.StatusOK {
		f.t.Fatalf("开局: %d %v", status, body)
	}
	code := body["game"].(map[string]any)["short_code"].(string)
	for _, p := range others {
		if status, body := f.do(p.c, http.MethodPost, "/api/games/"+code+"/seat", nil); status != http.StatusOK {
			f.t.Fatalf("报名: %d %v", status, body)
		}
	}
	return code
}

type row struct {
	nickname string
	n        int
	starred  bool
}

func (f *fixture) friends(p person) []row {
	f.t.Helper()
	status, body := f.do(p.c, http.MethodGet, "/api/friends", nil)
	if status != http.StatusOK {
		f.t.Fatalf("牌友列表: %d %v", status, body)
	}
	var out []row
	for _, x := range body["friends"].([]any) {
		m := x.(map[string]any)
		out = append(out, row{m["user"].(map[string]any)["nickname"].(string), int(m["together_count"].(float64)), m["starred"].(bool)})
	}
	return out
}

func TestFriendsListAndStars(t *testing.T) {
	f := newFixture(t)
	wang, lily, ming, jie, zhao := f.person("老王"), f.person("Lily"), f.person("阿明"), f.person("阿杰"), f.person("老赵")

	f.game(wang, lily, ming)       // 老王、Lily、阿明一起
	f.game(lily, wang)             // 老王和 Lily 又一局
	f.game(jie, zhao)              // 和老王无关
	cancelled := f.game(wang, jie) // 取消了的局不算
	if status, _ := f.do(wang.c, http.MethodPost, "/api/games/"+cancelled+"/cancel", nil); status != http.StatusOK {
		t.Fatalf("取消: %d", status)
	}

	// 一起打过的局数多的在前；取消的局、没一起坐过的人都不算。
	if got, want := f.friends(wang), []row{{"Lily", 2, false}, {"阿明", 1, false}}; !slices.Equal(got, want) {
		t.Fatalf("老王的牌友 %v，期望 %v", got, want)
	}

	// 星标的排最前；重复星标是幂等的。
	for range 2 {
		if status, body := f.do(wang.c, http.MethodPut, "/api/follows/"+ming.id, nil); status != http.StatusNoContent {
			t.Fatalf("星标: %d %v", status, body)
		}
	}
	if got, want := f.friends(wang), []row{{"阿明", 1, true}, {"Lily", 2, false}}; !slices.Equal(got, want) {
		t.Fatalf("星标后 %v，期望 %v", got, want)
	}
	// 星标是单向的：阿明那边看老王没有星标。
	if got := f.friends(ming); !slices.Contains(got, row{"老王", 1, false}) || slices.ContainsFunc(got, func(r row) bool { return r.starred }) {
		t.Fatalf("阿明的牌友 %v：老王应在、且没有星标", got)
	}

	// 没一起坐过局（含只在取消的局里同桌）、自己、不存在的 id：一律 404，不能拿来探测用户。
	for _, target := range []string{jie.id, zhao.id, wang.id, "01NOTAUSER"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			status, body := f.do(wang.c, method, "/api/follows/"+target, nil)
			if status != http.StatusNotFound || body["error"].(map[string]any)["code"] != "not_friend" {
				t.Errorf("%s %s：%d %v，期望 404 not_friend", method, target, status, body)
			}
		}
	}

	// 取消星标，幂等。
	for range 2 {
		if status, _ := f.do(wang.c, http.MethodDelete, "/api/follows/"+ming.id, nil); status != http.StatusNoContent {
			t.Fatalf("取消星标: %d", status)
		}
	}
	if got := f.friends(wang); got[0].starred || got[1].starred {
		t.Fatalf("取消后不该有星标：%v", got)
	}
}

// 已注销、被封禁的人不出现在牌友列表里，也不能被星标。
func TestFriendsExcludeDeletedAndBanned(t *testing.T) {
	f := newFixture(t)
	wang, lily, ming, jie := f.person("老王"), f.person("Lily"), f.person("阿明"), f.person("阿杰")
	f.game(wang, lily, ming, jie)

	if status, _ := f.do(lily.c, http.MethodDelete, "/api/me", nil); status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("注销: %d", status)
	}
	if _, err := f.st.Ban(context.Background(), ming.id, f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if got, want := f.friends(wang), []row{{"阿杰", 1, false}}; !slices.Equal(got, want) {
		t.Fatalf("老王的牌友 %v，期望只剩阿杰", got)
	}
	for _, target := range []string{lily.id, ming.id} {
		if status, _ := f.do(wang.c, http.MethodPut, "/api/follows/"+target, nil); status != http.StatusNotFound {
			t.Errorf("星标已注销或被封禁的人应 404，实际 %d", status)
		}
	}
}

// 牌友列表有上限。
func TestFriendsListIsCapped(t *testing.T) {
	if friends.MaxFriends <= 0 || friends.MaxFriends > 500 {
		t.Fatalf("MaxFriends = %d", friends.MaxFriends)
	}
}
