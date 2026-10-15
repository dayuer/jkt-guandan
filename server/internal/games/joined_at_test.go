package games_test

import (
	"net/http"
	"testing"
	"time"
)

// #160：详情的 me.joined_at / me.promoted_at 和列表的 my_joined_at / my_promoted_at，按当前身份算。
// 座位上的人是坐下的时间；候补里的是排候补的时间；补位上来的人加入时间仍从排候补算，另给补位时间。
func TestMyJoinedAndPromotedAt(t *testing.T) {
	f := newServer(t)
	t0 := f.clk.Now().Unix()
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)

	seated := make([]*http.Client, 0, 3)
	for _, name := range []string{"牌友一", "牌友二", "牌友三"} {
		f.clk.Advance(time.Minute)
		c := f.newBrowser()
		f.signUp(c, name)
		if r := f.do(c, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
			t.Fatalf("%s 报名失败：%d %s", name, r.status, r.raw)
		}
		seated = append(seated, c)
	}
	f.clk.Advance(time.Minute)
	waiter := f.newBrowser()
	f.signUp(waiter, "候补")
	if r := f.do(waiter, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("候补报名失败：%d %s", r.status, r.raw)
	}
	waitedAt := f.clk.Now().Unix()
	outsider := f.newBrowser()
	f.signUp(outsider, "路人")
	guest := f.newBrowser()

	// check 对比详情和列表里的两个时间；0 表示这一项不该出现。
	check := func(t *testing.T, c *http.Client, joined, promoted int64) {
		t.Helper()
		d := f.do(c, http.MethodGet, "/api/games/"+code, nil, nil)
		if d.status != http.StatusOK {
			t.Fatalf("详情：%d %s", d.status, d.raw)
		}
		me, _ := d.body["me"].(map[string]any)
		item := listItem(t, f.do(c, http.MethodGet, "/api/games", nil, nil), code)
		for _, got := range []struct {
			where string
			v     any
			want  int64
		}{
			{"me.joined_at", me["joined_at"], joined},
			{"me.promoted_at", me["promoted_at"], promoted},
			{"my_joined_at", item["my_joined_at"], joined},
			{"my_promoted_at", item["my_promoted_at"], promoted},
		} {
			if got.want == 0 {
				if got.v != nil {
					t.Errorf("%s 不该出现，实际 %v", got.where, got.v)
				}
			} else if got.v != float64(got.want) {
				t.Errorf("%s = %v，期望 %d", got.where, got.v, got.want)
			}
		}
	}

	t.Run("局头的座位", func(t *testing.T) { check(t, f.http, t0, 0) })
	t.Run("报名坐下的座位", func(t *testing.T) { check(t, seated[1], t0+2*60, 0) })
	t.Run("候补", func(t *testing.T) { check(t, waiter, waitedAt, 0) })
	t.Run("不在局里", func(t *testing.T) { check(t, outsider, 0, 0) })
	t.Run("访客", func(t *testing.T) { check(t, guest, 0, 0) })

	// 牌友一退出，候补补上：加入时间还是排候补的时间，补位时间是现在。
	f.clk.Advance(10 * time.Minute)
	if r := f.do(seated[0], http.MethodDelete, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("退出失败：%d %s", r.status, r.raw)
	}
	promotedAt := f.clk.Now().Unix()
	t.Run("补位之后", func(t *testing.T) {
		d := f.do(waiter, http.MethodGet, "/api/games/"+code, nil, nil)
		if me, _ := d.body["me"].(map[string]any); me["role"] != "seat" {
			t.Fatalf("候补应当已经补上，实际 %v", me)
		}
		check(t, waiter, waitedAt, promotedAt)
	})
	t.Run("退出的人", func(t *testing.T) { check(t, seated[0], 0, 0) })
	t.Run("别人补位不影响我", func(t *testing.T) { check(t, seated[1], t0+2*60, 0) })
}
