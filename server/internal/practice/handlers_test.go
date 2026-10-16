package practice_test

// 练习局接口的验收（#88 第 1 件）：建局拿回 27 张；同一局取两次一样；**响应里没有别家的手牌**；
// 越权取别人的局被拒；建新局把旧局收掉（但收掉的局还能取，复盘要用）。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/config"
	"github.com/dayuer/jkt-guandan/server/internal/httpapi"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/practice"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

const testOrigin = "https://guandan.test"

type fixture struct {
	t    *testing.T
	srv  *httptest.Server
	st   *store.Store
	http *http.Client
}

func newServer(t *testing.T) *fixture {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("执行迁移: %v", err)
	}

	clk := clock.NewFake(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	identSvc := identity.NewService(st, clk, true)
	practiceSvc := practice.NewService(st, clk)

	srv := httptest.NewTLSServer(httpapi.New(httpapi.Options{
		Config:   config.Config{DataDir: t.TempDir(), Dev: true, PublicURL: testOrigin},
		Store:    st,
		Clock:    clk,
		Identity: identSvc,
		Routes:   slices.Concat(identity.Routes(identSvc), practice.Routes(practiceSvc)),
	}))
	t.Cleanup(srv.Close)

	f := &fixture{t: t, srv: srv, st: st}
	f.http = f.newBrowser()
	return f
}

// newBrowser 建一个独立 cookie 的客户端（测试服务器每次返回同一个 *http.Client，直接共用一个
// 会把两个「人」串成一个）。
func (f *fixture) newBrowser() *http.Client {
	f.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		f.t.Fatalf("建 cookie jar: %v", err)
	}
	return &http.Client{
		Transport: f.srv.Client().Transport,
		Jar:       jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// signUp 给这个客户端一个身份（走真的接口，不塞 cookie）。
func (f *fixture) signUp(client *http.Client, nickname string) {
	f.t.Helper()
	r := f.do(client, http.MethodPost, "/api/me", map[string]any{"nickname": nickname}, nil)
	if r.status != http.StatusOK {
		f.t.Fatalf("填称呼失败：%d %s", r.status, r.raw)
	}
}

type resp struct {
	status int
	raw    string
	body   map[string]any
}

func (r resp) field(key string) any { return r.body[key] }

func (r resp) errCode() string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func (f *fixture) do(client *http.Client, method, path string, body any, headers map[string]string) resp {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			f.t.Fatalf("编码请求体: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, reader)
	if err != nil {
		f.t.Fatalf("建请求: %v", err)
	}
	req.Header.Set("Origin", testOrigin)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		f.t.Fatalf("读响应: %v", err)
	}
	out := resp{status: res.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// toStrings 把响应里的 JSON 数组（[]any）收成 []string。
func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// cardKeys 从响应原文里数出**牌面 key**（`♠K/0` / `大王/0` 这种）。
//
// 用数「原文里出现几张牌」来核「没有别家的手牌」：一局 108 张，谁的手牌泄漏出来都会让这个数
// 大于 27。比逐个字段断言更硬 —— 换个字段名夹带也躲不过。
var cardKey = regexp.MustCompile(`([♠♥♣♦](10|[2-9JQKA])|大王|小王)/\d+`)

func TestPracticeCreateAndGet(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "练习的人")

	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	if created.status != http.StatusOK {
		t.Fatalf("建局失败：%d %s", created.status, created.raw)
	}

	id, _ := created.field("id").(string)
	if id == "" {
		t.Fatalf("建局没有返回 id：%s", created.raw)
	}
	hand, _ := created.field("hand").([]any)
	if len(hand) != 27 {
		t.Fatalf("自己的手牌该是 27 张，拿到 %d 张", len(hand))
	}
	// 27 张各不相同（同一张牌只发一次；重复说明洗牌或发牌有问题）。
	seen := map[string]bool{}
	for _, c := range hand {
		key, _ := c.(string)
		if seen[key] {
			t.Fatalf("手牌里有重复的牌 %s：%v", key, hand)
		}
		seen[key] = true
	}
	if got := created.field("level").(float64); got != 2 {
		t.Fatalf("第一副该打 2，拿到 %v", got)
	}
	seat := int(created.field("seat").(float64))
	if seat < 1 || seat > 4 {
		t.Fatalf("座位该是 1–4，拿到 %d", seat)
	}
	if got := int(created.field("turn").(float64)); got != seat {
		t.Fatalf("首圈该是我领出（我坐 %d 号座），turn 是 %d", seat, got)
	}
	if created.field("revision").(float64) != 1 || created.field("ended").(bool) {
		t.Fatalf("新建的局该是 revision 1、没结束：%s", created.raw)
	}
	counts, _ := created.field("counts").([]any)
	if len(counts) != 4 {
		t.Fatalf("四家的张数该是 4 个数：%s", created.raw)
	}
	for i, c := range counts {
		if int(c.(float64)) != 27 {
			t.Fatalf("开局四家都该是 27 张，第 %d 家是 %v", i+1, c)
		}
	}

	// 只有自己的手牌：原文里牌的个数正好 27（别人的牌一张都不许出现）。
	if n := len(cardKey.FindAllString(created.raw, -1)); n != 27 {
		t.Fatalf("响应里有 %d 张牌，只该有自己那 27 张：%s", n, created.raw)
	}
	for _, forbidden := range []string{"hands", "opponents", "others", "ai_hand"} {
		if strings.Contains(created.raw, forbidden) {
			t.Fatalf("响应里出现了 %q —— 别家的牌不许出这个包：%s", forbidden, created.raw)
		}
	}

	// 同一局取两次：一模一样（断线回来 / 刷新拿到的就是同一个局面）。
	first := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
	second := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
	if first.status != http.StatusOK {
		t.Fatalf("取局失败：%d %s", first.status, first.raw)
	}
	if first.raw != second.raw {
		t.Fatalf("同一局取两次不一样：\n%s\n%s", first.raw, second.raw)
	}
	// 而且和建局时返回的是同一个局面（存档重放回来对得上）。
	if first.raw != created.raw {
		t.Fatalf("取回来的局面和建局时不一样：\n建局：%s\n取回：%s", created.raw, first.raw)
	}
}

func TestPracticeOthersGameIsNotFound(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "开的人")
	other := f.newBrowser()
	f.signUp(other, "别人")

	id, _ := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil).field("id").(string)

	// 别人拿不到：**返回 404 而不是 403** —— id 是不透明随机串，没必要透露「存在但不是你的」。
	got := f.do(other, http.MethodGet, "/api/practice/"+id, nil, nil)
	if got.status != http.StatusNotFound || got.errCode() != "practice_not_found" {
		t.Fatalf("别人的局该 404 practice_not_found，拿到 %d %s", got.status, got.raw)
	}
	// 不存在的 id 也是同一个答案（两种情况的响应一模一样）。
	missing := f.do(other, http.MethodGet, "/api/practice/不存在的ID", nil, nil)
	if missing.status != got.status || missing.raw != got.raw {
		t.Fatalf("「不是你的」和「不存在」该长得一样：%s vs %s", got.raw, missing.raw)
	}
}

func TestPracticeRequiresIdentity(t *testing.T) {
	f := newServer(t)
	// 没填称呼、没有 cookie 的客户端（安不匿名都由中间件说了算）。
	anon := f.newBrowser()
	f.signUp(f.http, "开的人")
	id, _ := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil).field("id").(string)

	for _, c := range []struct {
		name string
		got  resp
	}{
		{"建局", f.do(anon, http.MethodPost, "/api/practice", map[string]any{}, nil)},
		{"取局", f.do(anon, http.MethodGet, "/api/practice/"+id, nil, nil)},
	} {
		if c.got.status != http.StatusUnauthorized {
			t.Fatalf("%s 没身份该 401，拿到 %d %s", c.name, c.got.status, c.got.raw)
		}
	}
}

func TestPracticeNewGameEndsTheOldOne(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "开的人")

	first := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id1, _ := first.field("id").(string)
	second := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id2, _ := second.field("id").(string)
	if id1 == id2 {
		t.Fatal("第二次建局该是一局新的")
	}
	// 而且发的是**一副新牌**（同一副牌重发的话这里会一样 —— 那就是「随机源坏了」）。
	if first.field("hand") != nil && second.field("hand") != nil &&
		strings.Join(toStrings(first.field("hand")), ",") == strings.Join(toStrings(second.field("hand")), ",") {
		t.Fatal("两局发到了同一副牌 —— 洗牌没起作用")
	}

	// 旧局被收掉：还取得到（复盘要用），但标了 ended。
	old := f.do(f.http, http.MethodGet, "/api/practice/"+id1, nil, nil)
	if old.status != http.StatusOK {
		t.Fatalf("收掉的局也该取得到（复盘要做），拿到 %d %s", old.status, old.raw)
	}
	if !old.field("ended").(bool) {
		t.Fatalf("旧局该标 ended：%s", old.raw)
	}
	if old.field("id").(string) != id1 {
		t.Fatalf("取回来的该是旧局：%s", old.raw)
	}
	// 新局没被收。
	if f.do(f.http, http.MethodGet, "/api/practice/"+id2, nil, nil).field("ended").(bool) {
		t.Fatal("新局不该是 ended")
	}

	// 库里只有一条开着的（「一人同时只有一局」那条部分唯一索引兜着）。
	var open int
	if err := f.st.Read().QueryRowContext(context.Background(),
		`SELECT count(*) FROM practice_games WHERE user_id = (SELECT user_id FROM practice_games WHERE id = ?)
		 AND ended_at IS NULL`, id2).Scan(&open); err != nil {
		t.Fatalf("查开着的局: %v", err)
	}
	if open != 1 {
		t.Fatalf("同时开着的练习局该只有 1 局，有 %d 局", open)
	}
}
