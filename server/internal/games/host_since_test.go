package games_test

import (
	"net/http"
	"testing"
	"time"
)

// 局头转交的时间（#189）：`me.became_host_at` / 列表的 `my_became_host_at`。
//
// 只有**接手**的人才有这一项 —— 开局的人本人没有，「你成了局头」对他不是新闻；
// 客户端（app/minigame/src/lib/notice.ts 的 fallback）靠它出 K2 横幅，并且判断
// 上一位局头改过的时间要不要提示给接手的这位。
func TestBecameHostAt(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)

	b1 := f.newBrowser()
	f.signUp(b1, "牌友一")
	if r := f.do(b1, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("报名失败：%d %s", r.status, r.raw)
	}
	// 第二个人：转交之后他不是局头，**也不该**看到这一项 —— 否则他会看到一条
	// 「你现在是局头」的假横幅。HostSince 是记在游戏上的，必须再按 viewer 收一次口。
	b2 := f.newBrowser()
	f.signUp(b2, "牌友二")
	if r := f.do(b2, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("报名失败：%d %s", r.status, r.raw)
	}

	// want == 0 表示这一项不该出现。
	check := func(t *testing.T, c *http.Client, want int64) {
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
			{"me.became_host_at", me["became_host_at"], want},
			{"my_became_host_at", item["my_became_host_at"], want},
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

	t.Run("开局的人本人：没有这一项", func(t *testing.T) { check(t, f.http, 0) })
	t.Run("普通座位：没有这一项", func(t *testing.T) { check(t, b1, 0) })
	t.Run("另一个普通座位：没有这一项", func(t *testing.T) { check(t, b2, 0) })

	// 局头退出 → 自动转交给座位上最早进来的人（牌友一）。
	f.clk.Advance(5 * time.Minute)
	if r := f.do(f.http, http.MethodDelete, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("局头退出失败：%d %s", r.status, r.raw)
	}
	became := f.clk.Now().Unix()

	t.Run("转交之后：新局头有这一项", func(t *testing.T) { check(t, b1, became) })
	t.Run("转交之后：没接手的人还是没有这一项", func(t *testing.T) { check(t, b2, 0) })
	t.Run("原来的局头（已经退出）：没有", func(t *testing.T) { check(t, f.http, 0) })

	// 再转交一次：**这一列要覆盖成这一次的时刻**，不能留着上一位的。留着的话新局头拿到的
	// 是别人接手的时间，而这一项在客户端是「你成了局头」的触发条件 —— 会拿别人的时刻弹给他。
	// （#189 复审意见：补一条连续转交的。）
	f.clk.Advance(5 * time.Minute)
	if r := f.do(b1, http.MethodDelete, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("第二任局头退出失败：%d %s", r.status, r.raw)
	}
	became2 := f.clk.Now().Unix()

	t.Run("连续转交两次：第二任拿到的是第二次的时刻，不是第一次的", func(t *testing.T) {
		if became2 <= became {
			t.Fatalf("时钟没往前走（第一次 %d、第二次 %d），这条测试就没有意义了", became, became2)
		}
		check(t, b2, became2)
	})
	t.Run("连续转交两次：第一任（已经退出）不再是局头", func(t *testing.T) { check(t, b1, 0) })
}
