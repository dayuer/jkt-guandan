package games_test

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"testing"
	"time"
)

// 「我的牌局」（#211）
// ---------------------------------------------------------------------------

// codesOf 取列表响应里的短码，按返回顺序。
func codesOf(r resp) []string {
	items, _ := r.body["games"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		s, _ := m["short_code"].(string)
		out = append(out, s)
	}
	return out
}

func sameCodes(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// 要打的：我开的、我坐下的、我候补的；别人的、已取消的不在里面。开始之后挪到历史，
// 候补没轮上的不进历史；已取消的直接进历史。
func TestMyGamesUpcomingAndHistory(t *testing.T) {
	f := newServer(t)
	me := f.http
	f.signUp(me, "我")

	newHost := func(name string) *http.Client {
		c := f.newBrowser()
		f.signUp(c, name)
		return c
	}
	join := func(c *http.Client, code string) {
		t.Helper()
		if r := f.do(c, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
			t.Fatalf("报名 %s：%d %s", code, r.status, r.raw)
		}
	}

	// 各局开始时间错开一点，排序才看得出来。
	mineHosted := f.createGame(me, nil) // +2h
	f.clk.Advance(time.Minute)
	b := newHost("乙")
	seated := f.createGame(b, nil) // +2h1m
	join(me, seated)
	f.clk.Advance(time.Minute)
	c := newHost("丙")
	waiting := f.createGame(c, nil) // +2h2m
	for _, n := range []string{"丙一", "丙二", "丙三"} {
		join(newHost(n), waiting)
	}
	join(me, waiting) // 坐满了，我进候补
	d := newHost("丁")
	others := f.createGame(d, nil) // 和我无关
	e := newHost("戊")
	cancelled := f.createGame(e, nil)
	join(me, cancelled)
	if r := f.do(e, http.MethodPost, "/api/games/"+cancelled+"/cancel", nil, nil); r.status != http.StatusOK {
		t.Fatalf("取消：%d %s", r.status, r.raw)
	}

	r := f.do(me, http.MethodGet, "/api/me/games", nil, nil)
	if r.status != http.StatusOK || !sameCodes(codesOf(r), mineHosted, seated, waiting) {
		t.Fatalf("要打的不对：%v，期望 %v（%d %s）", codesOf(r), []string{mineHosted, seated, waiting}, r.status, r.raw)
	}
	if item := listItem(t, r, waiting); item["my_role"] != "waitlist" || item["waitlist_pos"] != float64(1) {
		t.Fatalf("候补那一局 my_role=%v waitlist_pos=%v", item["my_role"], item["waitlist_pos"])
	}
	if item := listItem(t, r, seated); item["my_role"] != "seated" || item["missing"] != float64(2) {
		t.Fatalf("坐下那一局 my_role=%v missing=%v", item["my_role"], item["missing"])
	}
	_ = others

	r = f.do(me, http.MethodGet, "/api/me/games/history", nil, nil)
	if r.status != http.StatusOK || !sameCodes(codesOf(r), cancelled) || r.body["has_more"] != false {
		t.Fatalf("开始前历史里只有取消的那局：%v %s", codesOf(r), r.raw)
	}

	// 全部开始以后：要打的空了；历史按开始时间从近到远，候补那一局不算。
	f.clk.Advance(3 * time.Hour)
	if r := f.do(me, http.MethodGet, "/api/me/games", nil, nil); r.status != http.StatusOK || len(codesOf(r)) != 0 {
		t.Fatalf("开始后要打的应该是空的：%v", codesOf(r))
	}
	r = f.do(me, http.MethodGet, "/api/me/games/history", nil, nil)
	if !sameCodes(codesOf(r), cancelled, seated, mineHosted) {
		t.Fatalf("历史不对：%v，期望 %v", codesOf(r), []string{cancelled, seated, mineHosted})
	}

	// 访客没有身份。
	if r := f.do(f.newBrowser(), http.MethodGet, "/api/me/games", nil, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("访客应该 401：%d", r.status)
	}
}

// 历史分页：上一页最后一条的 starts_at + short_code 带回来，接着往更早翻，不重不漏。
func TestMyGamesHistoryPaging(t *testing.T) {
	f := newServer(t)
	me := f.http
	f.signUp(me, "我")
	type game struct {
		at   int64
		code string
	}
	var made []game
	for i := 0; i < 5; i++ {
		h := f.newBrowser()
		f.signUp(h, "局头"+strconv.Itoa(i))
		code := f.createGame(h, nil)
		if r := f.do(me, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
			t.Fatalf("报名：%d %s", r.status, r.raw)
		}
		made = append(made, game{f.clk.Now().Add(2 * time.Hour).Unix(), code})
		if i%2 == 0 {
			f.clk.Advance(time.Minute) // 有两局同一时间开，靠短码排
		}
	}
	f.clk.Advance(3 * time.Hour)
	// 期望顺序：开始时间从近到远（越晚开的越靠前），同一时间按短码倒序。
	sort.Slice(made, func(i, j int) bool {
		if made[i].at != made[j].at {
			return made[i].at > made[j].at
		}
		return made[i].code > made[j].code
	})
	want := make([]string, len(made))
	for i, g := range made {
		want[i] = g.code
	}

	var got []string
	path := "/api/me/games/history?limit=2"
	for page := 0; ; page++ {
		if page > 5 {
			t.Fatal("翻不完")
		}
		r := f.do(me, http.MethodGet, path, nil, nil)
		items, _ := r.body["games"].([]any)
		got = append(got, codesOf(r)...)
		if r.body["has_more"] != true {
			break
		}
		last, _ := items[len(items)-1].(map[string]any)
		path = "/api/me/games/history?limit=2&before=" + strconv.FormatInt(int64(last["starts_at"].(float64)), 10) +
			"&before_code=" + last["short_code"].(string)
	}
	if !sameCodes(got, want...) {
		t.Fatalf("分页拼起来不对：%v，期望 %v", got, want)
	}
}

// 删：只从我自己的历史里拿掉；同局的人照样看得到；还没开始的不能删；不是我的当不存在；
// 重复删幂等；注销时连同记录一起清掉。
func TestMyGamesHideFromHistory(t *testing.T) {
	f := newServer(t)
	me := f.http
	f.signUp(me, "我")
	host := f.newBrowser()
	f.signUp(host, "局头")
	code := f.createGame(host, nil)
	if r := f.do(me, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("报名：%d %s", r.status, r.raw)
	}
	stranger := f.newBrowser()
	f.signUp(stranger, "路人")

	del := func(c *http.Client, code string) resp {
		return f.do(c, http.MethodDelete, "/api/me/games/history/"+code, nil, nil)
	}

	if r := del(me, code); r.status != http.StatusConflict || r.errCode() != "not_history" {
		t.Fatalf("还没开始不能删：%d %s", r.status, r.raw)
	}

	f.clk.Advance(3 * time.Hour)
	if r := del(stranger, code); r.status != http.StatusNotFound {
		t.Fatalf("不是我的局应该 404：%d %s", r.status, r.raw)
	}
	if r := del(me, "NOPE00"); r.status != http.StatusNotFound {
		t.Fatalf("不存在的局应该 404：%d %s", r.status, r.raw)
	}
	for i := 0; i < 2; i++ {
		if r := del(me, code); r.status != http.StatusNoContent {
			t.Fatalf("第 %d 次删：%d %s", i+1, r.status, r.raw)
		}
	}

	if r := f.do(me, http.MethodGet, "/api/me/games/history", nil, nil); len(codesOf(r)) != 0 {
		t.Fatalf("删了以后我的历史里不该有：%v", codesOf(r))
	}
	if r := f.do(host, http.MethodGet, "/api/me/games/history", nil, nil); !sameCodes(codesOf(r), code) {
		t.Fatalf("同局的局头照样看得到：%v", codesOf(r))
	}
	if r := f.do(host, http.MethodGet, "/api/games/"+code, nil, nil); r.status != http.StatusOK {
		t.Fatalf("牌局本身还在：%d", r.status)
	}

	if n := countRows(t, f, `SELECT count(*) FROM hidden_games`); n != 1 {
		t.Fatalf("应该记了 1 行，实际 %d", n)
	}
	if r := f.do(me, http.MethodDelete, "/api/me", nil, nil); r.status != http.StatusNoContent && r.status != http.StatusOK {
		t.Fatalf("注销：%d %s", r.status, r.raw)
	}
	if n := countRows(t, f, `SELECT count(*) FROM hidden_games`); n != 0 {
		t.Fatalf("注销后隐藏记录应该清掉，还剩 %d", n)
	}
}

func countRows(t *testing.T, f *srvFixture, q string) int {
	t.Helper()
	var n int
	if err := f.st.Read().QueryRowContext(context.Background(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 历史里记过分的局带比分摘要，和计分页同一套算法。
func TestMyGamesHistoryScore(t *testing.T) {
	f := newFixture(t)
	code, seats := f.fullGame()
	f.mustStart(code, seats[0])
	f.mustRecord(code, seats[0], 1, 1, 2) // 1 号头游、2 号位的队友二游：红方双上
	f.fake.Advance(time.Hour)

	items, more, err := f.games.History(context.Background(), seats[1], 0, "", 0)
	if err != nil || more || len(items) != 1 {
		t.Fatalf("历史：%v %v %v", len(items), more, err)
	}
	sc := items[0].Score
	if sc == nil || sc.Rounds != 1 || sc.RedDoubleUps != 1 || sc.RedLevel <= 2 {
		t.Fatalf("比分摘要不对：%+v", sc)
	}

	// 没记过分的局没有摘要。
	other := f.newUser("别人")
	code2 := f.newGame(other)
	f.fake.Advance(3 * time.Hour)
	items, _, err = f.games.History(context.Background(), other, 0, "", 0)
	if err != nil || len(items) != 1 || items[0].Game.ShortCode != code2 || items[0].Score != nil {
		t.Fatalf("没记分的局：%+v %v", items, err)
	}
}
