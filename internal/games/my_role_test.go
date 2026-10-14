package games_test

import (
	"net/http"
	"strconv"
	"testing"
)

// listItem 取 GET /api/games 里指定短码的那一项。
func listItem(t *testing.T, r resp, code string) map[string]any {
	t.Helper()
	items, _ := r.body["games"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["short_code"] == code {
			return m
		}
	}
	t.Fatalf("列表里没有 %s：%s", code, r.raw)
	return nil
}

// #114：列表条目带 my_role / waitlist_pos，按当前身份算；访客一律是 none。
func TestListHasMyRoleAndWaitlistPos(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)

	// 再来 3 个坐满，之后两个人排候补。
	for _, name := range []string{"牌友一", "牌友二", "牌友三"} {
		c := f.newBrowser()
		f.signUp(c, name)
		if r := f.do(c, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
			t.Fatalf("%s 报名失败：%d %s", name, r.status, r.raw)
		}
	}
	wait1, wait2 := f.newBrowser(), f.newBrowser()
	f.signUp(wait1, "候补一")
	f.clk.Advance(1) // 候补顺序按 created_at，再按 rowid
	f.signUp(wait2, "候补二")
	for _, c := range []*http.Client{wait1, wait2} {
		if r := f.do(c, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
			t.Fatalf("候补报名失败：%d %s", r.status, r.raw)
		}
	}
	outsider := f.newBrowser()
	f.signUp(outsider, "路人")
	guest := f.newBrowser()

	cases := []struct {
		name   string
		client *http.Client
		role   string
		pos    float64
	}{
		{"局头在座位上", f.http, "seated", 0},
		{"候补第 1 位", wait1, "waitlist", 1},
		{"候补第 2 位", wait2, "waitlist", 2},
		{"没报名的人", outsider, "none", 0},
		{"访客没有身份", guest, "none", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := f.do(c.client, http.MethodGet, "/api/games", nil, nil)
			if r.status != http.StatusOK {
				t.Fatalf("%d %s", r.status, r.raw)
			}
			item := listItem(t, r, code)
			if item["my_role"] != c.role || item["waitlist_pos"] != c.pos {
				t.Fatalf("my_role=%v waitlist_pos=%v，期望 %s / %v", item["my_role"], item["waitlist_pos"], c.role, c.pos)
			}
		})
	}
}

// #114：rescheduled_at 只在时间或地点真的变了才有；改备注、原样重发都不算。
func TestRescheduledAtOnlyWhenTimeOrVenueChanges(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)
	other := f.newBrowser()
	f.signUp(other, "牌友")

	patch := func(body map[string]any) resp {
		t.Helper()
		d := f.do(f.http, http.MethodGet, "/api/games/"+code, nil, nil)
		v := strconv.FormatInt(int64(d.gameField("version").(float64)), 10)
		r := f.do(f.http, http.MethodPatch, "/api/games/"+code, body, map[string]string{"If-Match": v})
		if r.status != http.StatusOK {
			t.Fatalf("改局失败：%d %s", r.status, r.raw)
		}
		return r
	}
	rescheduled := func() any {
		d := f.do(other, http.MethodGet, "/api/games/"+code, nil, nil)
		if item := listItem(t, f.do(other, http.MethodGet, "/api/games", nil, nil), code)["rescheduled_at"]; item != d.gameField("rescheduled_at") {
			t.Fatalf("详情和列表的 rescheduled_at 不一致：%v / %v", d.gameField("rescheduled_at"), item)
		}
		return d.gameField("rescheduled_at")
	}

	if v := rescheduled(); v != nil {
		t.Fatalf("没改过就不该有 rescheduled_at，实际 %v", v)
	}
	patch(map[string]any{"note": "带水"})
	patch(map[string]any{"venue": "Pacific Place"}) // 地点原样重发
	if v := rescheduled(); v != nil {
		t.Fatalf("改备注、原样重发地点都不算改期，实际 %v", v)
	}

	f.clk.Advance(5 * 60 * 1e9)
	patch(map[string]any{"venue": "Grand Indonesia"})
	first := rescheduled()
	if first != float64(f.clk.Now().Unix()) {
		t.Fatalf("改地点后 rescheduled_at 应当是现在，实际 %v", first)
	}

	f.clk.Advance(5 * 60 * 1e9)
	patch(map[string]any{"starts_at": f.clk.Now().Add(3 * 3600 * 1e9).Unix()})
	if second := rescheduled(); second != float64(f.clk.Now().Unix()) || second == first {
		t.Fatalf("改时间后 rescheduled_at 应当更新，实际 %v（上次 %v）", second, first)
	}
}

// 原样重发同一个 starts_at 不算改期：不给座位上的人发「改期」推送，也不记 rescheduled_at。
func TestPatchSameStartsAtSendsNoReschedulePush(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)
	other := f.newBrowser()
	f.signUp(other, "牌友")
	if r := f.do(other, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("报名失败：%d %s", r.status, r.raw)
	}

	rescheduledRows := func() int {
		var n int
		if err := f.st.Read().QueryRow(`SELECT count(*) FROM outbox WHERE kind = 'rescheduled'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	patchTime := func(startsAt int64) {
		t.Helper()
		d := f.do(f.http, http.MethodGet, "/api/games/"+code, nil, nil)
		v := strconv.FormatInt(int64(d.gameField("version").(float64)), 10)
		r := f.do(f.http, http.MethodPatch, "/api/games/"+code, map[string]any{"starts_at": startsAt}, map[string]string{"If-Match": v})
		if r.status != http.StatusOK {
			t.Fatalf("改局失败：%d %s", r.status, r.raw)
		}
	}

	d := f.do(f.http, http.MethodGet, "/api/games/"+code, nil, nil)
	same := int64(d.gameField("starts_at").(float64))
	patchTime(same)
	if n := rescheduledRows(); n != 0 {
		t.Fatalf("时间没变，不该有改期推送，实际 %d 条", n)
	}
	if v := f.do(other, http.MethodGet, "/api/games/"+code, nil, nil).gameField("rescheduled_at"); v != nil {
		t.Fatalf("时间没变，不该有 rescheduled_at，实际 %v", v)
	}

	patchTime(same + 3600)
	if n := rescheduledRows(); n != 2 { // 座位上的两个人各一条
		t.Fatalf("改了时间应给座位上的 2 个人各发一条，实际 %d 条", n)
	}
}
