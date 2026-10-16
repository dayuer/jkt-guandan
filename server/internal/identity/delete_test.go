package identity_test

// 注销（§14、`DELETE /api/me`）要清掉**这个人自己的**东西。练习局（#88）就是这种：
// 一个人的牌，没别人参与，注销就删。这条用例修的是「加了新表忘了加进删除清单」
// 那类漏（#270 复审 B1）。

import (
	"net/http"
	"testing"
)

func TestDeleteMeRemovesPracticeGames(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "union-1")
	login := f.wxLogin("c1", "练习的人")
	uid := login.userField("id")

	// 那个人的两局练习局：一局开着、一局已结束。
	f.exec(`INSERT INTO practice_games (id, user_id, level, seat, state, revision, created_at, ended_at)
		VALUES ('p-open', ?, 2, 1, '{}', 1, 0, NULL), ('p-done', ?, 2, 2, '{}', 1, 0, 0)`, uid, uid)
	if n := f.countRows(`SELECT count(*) FROM practice_games WHERE user_id = ?`, uid); n != 2 {
		t.Fatalf("先得有两局练习局，实际 %d", n)
	}

	if r := f.mini(http.MethodDelete, "/api/me", nil, bearer(login.str("device_token"))); r.status != http.StatusNoContent {
		t.Fatalf("注销：%d %s", r.status, r.raw)
	}
	if n := f.countRows(`SELECT count(*) FROM practice_games WHERE user_id = ?`, uid); n != 0 {
		t.Fatalf("注销后练习局该全删掉，还剩 %d 局", n)
	}
}
