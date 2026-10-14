package games_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// #115：局头邀请牌友。只能邀请一起打过局的人，去重，重复邀请跳过，被邀请的人在详情里看到是谁邀请的。
func TestInviteFriends(t *testing.T) {
	f := newServer(t)
	host, lily, ming, stranger := f.http, f.newBrowser(), f.newBrowser(), f.newBrowser()
	f.signUp(host, "老王")
	lilyID, mingID, strangerID := f.signUp(lily, "Lily"), f.signUp(ming, "阿明"), f.signUp(stranger, "路人")

	// 第一局：老王、Lily、阿明一起坐过 → 互为牌友。
	first := f.createGame(host, nil)
	for _, c := range []*http.Client{lily, ming} {
		if r := f.do(c, http.MethodPost, "/api/games/"+first+"/seat", nil, nil); r.status != http.StatusOK {
			t.Fatalf("报名: %d %s", r.status, r.raw)
		}
	}
	code := f.createGame(host, map[string]string{"Idempotency-Key": "second"})
	path := "/api/games/" + code + "/invites"

	// 有一个不是牌友：整次失败，一个都不邀请（不能拿来探测用户 id）。
	r := f.do(host, http.MethodPost, path, map[string]any{"user_ids": []string{lilyID, strangerID}}, nil)
	if r.status != http.StatusNotFound || r.errCode() != "not_friend" {
		t.Fatalf("邀请非牌友应 404 not_friend：%d %s", r.status, r.raw)
	}
	if me := f.do(lily, http.MethodGet, "/api/games/"+code, nil, nil).body["me"].(map[string]any); me["invited_by"] != nil {
		t.Fatalf("失败的邀请不能留下记录：%v", me)
	}

	// 去重、空 id 忽略。
	r = f.do(host, http.MethodPost, path, map[string]any{"user_ids": []string{lilyID, lilyID, "", mingID}}, nil)
	if r.status != http.StatusOK || !slices.Equal(strs(r.body["invited"]), []string{lilyID, mingID}) || len(strs(r.body["skipped"])) != 0 {
		t.Fatalf("邀请: %d %s", r.status, r.raw)
	}
	// 再邀请一次：都跳过，不重复推送。
	r = f.do(host, http.MethodPost, path, map[string]any{"user_ids": []string{lilyID}}, nil)
	if r.status != http.StatusOK || len(strs(r.body["invited"])) != 0 || !slices.Equal(strs(r.body["skipped"]), []string{lilyID}) {
		t.Fatalf("重复邀请应跳过: %d %s", r.status, r.raw)
	}

	// 被邀请的人在详情里看到是谁邀请的；别人看不到。
	me := f.do(lily, http.MethodGet, "/api/games/"+code, nil, nil).body["me"].(map[string]any)
	if inv, _ := me["invited_by"].(map[string]any); inv == nil || inv["nickname"] != "老王" {
		t.Fatalf("Lily 应看到「老王邀请了你」：%v", me)
	}
	if me := f.do(stranger, http.MethodGet, "/api/games/"+code, nil, nil).body["me"].(map[string]any); me["invited_by"] != nil {
		t.Fatalf("没被邀请的人不该有 invited_by：%v", me)
	}

	// 每个新邀请的人各一条 invited 推送。
	var n int
	if err := f.st.Read().QueryRow(`SELECT COUNT(*) FROM outbox WHERE kind = 'invited'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("invited 推送应有 2 条，实际 %d %v", n, err)
	}

	// 已经在这一局里的人：跳过。阿明先报名，再被邀请一次也是跳过（已邀请过）；换一局验证「已在局里」。
	third := f.createGame(host, map[string]string{"Idempotency-Key": "third"})
	if r := f.do(ming, http.MethodPost, "/api/games/"+third+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("报名: %d %s", r.status, r.raw)
	}
	r = f.do(host, http.MethodPost, "/api/games/"+third+"/invites", map[string]any{"user_ids": []string{mingID, lilyID}}, nil)
	if r.status != http.StatusOK || !slices.Equal(strs(r.body["invited"]), []string{lilyID}) || !slices.Equal(strs(r.body["skipped"]), []string{mingID}) {
		t.Fatalf("已在局里的人应跳过: %d %s", r.status, r.raw)
	}

	// 不是局头：403 not_host。
	if r := f.do(lily, http.MethodPost, path, map[string]any{"user_ids": []string{mingID}}, nil); r.status != http.StatusForbidden || r.errCode() != "not_host" {
		t.Fatalf("非局头邀请应 403 not_host：%d %s", r.status, r.raw)
	}
}

// 一次最多邀请 MaxInvitesPerCall 人；24 小时内最多 MaxInvitesPerDay 条。
func TestInviteLimits(t *testing.T) {
	f := newServer(t)
	host, lily := f.http, f.newBrowser()
	hostID := f.signUp(host, "老王")
	lilyID := f.signUp(lily, "Lily")
	first := f.createGame(host, nil)
	if r := f.do(lily, http.MethodPost, "/api/games/"+first+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("报名: %d", r.status)
	}
	code := f.createGame(host, map[string]string{"Idempotency-Key": "second"})
	path := "/api/games/" + code + "/invites"

	many := make([]string, games.MaxInvitesPerCall+1)
	for i := range many {
		many[i] = strings.Repeat("x", i+1)
	}
	if r := f.do(host, http.MethodPost, path, map[string]any{"user_ids": many}, nil); r.status != http.StatusBadRequest {
		t.Fatalf("一次超过上限应 400：%d %s", r.status, r.raw)
	}
	if r := f.do(host, http.MethodPost, path, map[string]any{"user_ids": []string{}}, nil); r.status != http.StatusBadRequest {
		t.Fatalf("空列表应 400：%d %s", r.status, r.raw)
	}

	// 当天已经发了 MaxInvitesPerDay 条：再邀请 409 quota_exceeded。
	if err := f.st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		for i := 0; i < games.MaxInvitesPerDay; i++ {
			g := "fake-" + strings.Repeat("g", i+1)
			if _, err := tx.ExecContext(ctx, `INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, created_at)
				VALUES (?, ?, ?, 0, 'SCBD', 'x', 'classic', 0)`, g, g, hostID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO invites (game_id, user_id, invited_by, created_at) VALUES (?, ?, ?, ?)`,
				g, lilyID, hostID, f.clk.Now().Unix()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if r := f.do(host, http.MethodPost, path, map[string]any{"user_ids": []string{lilyID}}, nil); r.status != http.StatusConflict || r.errCode() != "quota_exceeded" {
		t.Fatalf("超出每日上限应 409 quota_exceeded：%d %s", r.status, r.raw)
	}
}
