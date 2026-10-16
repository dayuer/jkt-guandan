package games_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// 注销（§14）：`DELETE /api/me` 在牌局侧的行为——还没结束的局按正常退出走，
// 已结束的局留着座位当历史记录。

// newUserWithToken 和 newUser 一样，另外把设备 token 也返回，用来验证注销之后
// 这台设备认不出身份了。
func (f *fixture) newUserWithToken(nickname string) (userID, token string) {
	f.t.Helper()
	ident, token, err := f.ident.CreateOrGet(context.Background(), nil, nickname, "test-ua")
	if err != nil {
		f.t.Fatalf("建用户 %s: %v", nickname, err)
	}
	return ident.User.ID, token
}

// deleteUser 走一遍注销，返回 DeleteMe 的入参身份。
func (f *fixture) deleteUser(userID, token string) {
	f.t.Helper()
	f.ident.SetGameCleaner(f.games)
	ident, err := f.ident.Current(context.Background(), token)
	if err != nil {
		f.t.Fatalf("取身份: %v", err)
	}
	if err := f.ident.DeleteMe(context.Background(), ident); err != nil {
		f.t.Fatalf("注销: %v", err)
	}
}

func (f *fixture) seatIDs(code string) []string {
	f.t.Helper()
	rows, err := f.st.Read().Query(`
		SELECT s.user_id FROM seats s JOIN games g ON g.id = s.game_id
		 WHERE g.short_code = ? ORDER BY s.seat_no`, code)
	if err != nil {
		f.t.Fatalf("查座位: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func (f *fixture) hostOf(code string) string {
	f.t.Helper()
	var host string
	if err := f.st.Read().QueryRow(
		`SELECT host_id FROM games WHERE short_code = ?`, code).Scan(&host); err != nil {
		f.t.Fatalf("查局头: %v", err)
	}
	return host
}

// 注销的人如果是局头，局要按 §5.1 转给座位上最早进来的人，而不是跟着一起消失。
func TestDeleteMeTransfersHost(t *testing.T) {
	f := newFixture(t)
	hostID, hostToken := f.newUserWithToken("局头")
	nextID := f.newUser("下一位")

	code := f.newGame(hostID)
	if _, err := f.games.Join(context.Background(), code, nextID); err != nil {
		t.Fatalf("报名: %v", err)
	}

	f.deleteUser(hostID, hostToken)

	if got := f.hostOf(code); got != nextID {
		t.Fatalf("局头应当转给 %s，实际 %s", nextID, got)
	}
	if seats := f.seatIDs(code); len(seats) != 1 || seats[0] != nextID {
		t.Fatalf("注销的人应当已经离开，剩下的座位是 %v", seats)
	}
	f.checkInvariants(code)

	// 注销的那台设备认不出身份了。
	if _, err := f.ident.Current(context.Background(), hostToken); err == nil {
		t.Fatal("注销之后原来的设备不该还能认出身份")
	}
}

// 注销的人走了之后，候补要照常补位（§5.1）。
func TestDeleteMePromotesFromWaitlist(t *testing.T) {
	f := newFixture(t)
	hostID := f.newUser("局头")
	leavingID, leavingToken := f.newUserWithToken("要走的人")
	thirdID := f.newUser("第三")
	waitingID := f.newUser("候补")

	code := f.newGame(hostID)
	for _, id := range []string{leavingID, thirdID} {
		if _, err := f.games.Join(context.Background(), code, id); err != nil {
			t.Fatalf("报名: %v", err)
		}
	}
	// 坐满 4 个：局头 + 要走的人 + 第三 + 最后一位。
	fourthID := f.newUser("第四")
	if _, err := f.games.Join(context.Background(), code, fourthID); err != nil {
		t.Fatalf("报名: %v", err)
	}
	// 再来一个进候补。
	detail, err := f.games.Join(context.Background(), code, waitingID)
	if err != nil {
		t.Fatalf("候补: %v", err)
	}
	if detail.Me != "waitlist" {
		t.Fatalf("第 5 个人应当是候补，实际 %s", detail.Me)
	}

	f.deleteUser(leavingID, leavingToken)

	after, err := f.games.Get(context.Background(), code, waitingID)
	if err != nil {
		t.Fatalf("取牌局: %v", err)
	}
	if after.Me != "seat" {
		t.Fatalf("候补应当补上位，实际角色 %s", after.Me)
	}
	if after.Game.Status != "full" {
		t.Fatalf("补位之后仍然满员，实际 %s", after.Game.Status)
	}
	if len(after.Waitlist) != 0 {
		t.Fatalf("候补队列应当空了，实际 %v", after.Waitlist)
	}
	f.checkInvariants(code)
}

// 已结束的局是历史记录：座位行留着，人数统计不变；称呼由 identity 改成「已注销牌友」。
func TestDeleteMeKeepsSeatsInFinishedGames(t *testing.T) {
	f := newFixture(t)
	hostID := f.newUser("局头")
	playerID, playerToken := f.newUserWithToken("牌友")

	code := f.newGame(hostID)
	if _, err := f.games.Join(context.Background(), code, playerID); err != nil {
		t.Fatalf("报名: %v", err)
	}

	// 这一局打完了。
	var gameID string
	if err := f.st.Read().QueryRow(`SELECT id FROM games WHERE short_code = ?`, code).Scan(&gameID); err != nil {
		t.Fatal(err)
	}
	if err := f.games.End(context.Background(), gameID); err != nil {
		t.Fatalf("结束牌局: %v", err)
	}

	f.deleteUser(playerID, playerToken) // 注销的是普通座位上的牌友，不是局头

	seats := f.seatIDs(code)
	if len(seats) != 2 {
		t.Fatalf("已结束的局应当保留座位行，实际剩 %d 个", len(seats))
	}
	var nickname string
	if err := f.st.Read().QueryRow(`SELECT nickname FROM users WHERE id = ?`, playerID).Scan(&nickname); err != nil {
		t.Fatalf("查称呼: %v", err)
	}
	if nickname != store.DeletedNickname {
		t.Fatalf("称呼应当被抹成 %q，实际 %q", store.DeletedNickname, nickname)
	}
}

// 注销要把个人数据都清掉：设备、推送订阅、认领令牌、幂等键，以及未发出的提醒。
func TestDeleteMeRemovesPersonalData(t *testing.T) {
	f := newFixture(t)
	hostID := f.newUser("局头")
	leavingID, leavingToken := f.newUserWithToken("要走的人")

	code := f.newGame(hostID)
	if _, err := f.games.Join(context.Background(), code, leavingID); err != nil {
		t.Fatalf("报名: %v", err)
	}
	// 签发一个认领令牌，注销后应当一起消失。
	if _, _, _, err := f.ident.IssueClaim(context.Background(), leavingID, "1.2.3.0/24"); err != nil {
		t.Fatalf("签发认领: %v", err)
	}

	f.deleteUser(leavingID, leavingToken)

	for _, tc := range []struct {
		what  string
		query string
	}{
		{"设备", `SELECT count(*) FROM devices WHERE user_id = ?`},
		{"认领令牌", `SELECT count(*) FROM claim_tokens WHERE user_id = ?`},
		{"未发出的提醒", `SELECT count(*) FROM outbox WHERE user_id = ?`},
	} {
		if n := f.count(tc.query, leavingID); n != 0 {
			t.Errorf("注销之后 %s 应当没了，还有 %d 条", tc.what, n)
		}
	}

	var deletedAt sql.NullInt64
	var wa sql.NullString
	if err := f.st.Read().QueryRow(
		`SELECT deleted_at, wa_phone FROM users WHERE id = ?`, leavingID).Scan(&deletedAt, &wa); err != nil {
		t.Fatalf("查用户: %v", err)
	}
	if !deletedAt.Valid || deletedAt.Int64 == 0 {
		t.Fatal("用户行应当写上 deleted_at")
	}
	if wa.Valid {
		t.Fatal("WhatsApp 号码应当被清空")
	}
}

// 重复注销是安全的：前端可能重试，第二次不该报错，也不该把已经不在的数据再删一遍。
func TestDeleteMeIsIdempotent(t *testing.T) {
	f := newFixture(t)
	userID, token := f.newUserWithToken("要走的人")
	f.ident.SetGameCleaner(f.games)

	ident, err := f.ident.Current(context.Background(), token)
	if err != nil {
		t.Fatalf("取身份: %v", err)
	}
	if err := f.ident.DeleteMe(context.Background(), ident); err != nil {
		t.Fatalf("第一次注销: %v", err)
	}
	if err := f.ident.DeleteMe(context.Background(), ident); err != nil {
		t.Fatalf("第二次注销应当也是成功的（幂等），实际 %v", err)
	}

	var nickname string
	if err := f.st.Read().QueryRow(`SELECT nickname FROM users WHERE id = ?`, userID).Scan(&nickname); err != nil {
		t.Fatal(err)
	}
	if nickname != store.DeletedNickname {
		t.Fatalf("称号 = %q", nickname)
	}
}

// 注销的人还在别的、由别人当局头的局里时，那些人应当收到「有人退出了」的通知。
func TestDeleteMeNotifiesOtherSeats(t *testing.T) {
	f := newFixture(t)
	hostID := f.newUser("局头")
	leavingID, leavingToken := f.newUserWithToken("要走的人")

	code := f.newGame(hostID)
	if _, err := f.games.Join(context.Background(), code, leavingID); err != nil {
		t.Fatalf("报名: %v", err)
	}
	before := f.count(`SELECT count(*) FROM outbox WHERE user_id = ?`, hostID)

	f.deleteUser(leavingID, leavingToken)

	after := f.count(`SELECT count(*) FROM outbox WHERE user_id = ?`, hostID)
	if after <= before {
		t.Fatalf("局头应当收到「有人退出了」的提醒：之前 %d 条，之后 %d 条", before, after)
	}
}

// §14 / UU PDP：自助注销（DELETE /api/me）也要把埋点上的 user_id 置空，和 admin 命令一致。
// 埋点本身留着做汇总，别人的不动。
func TestDeleteMeNullsEventUserID(t *testing.T) {
	f := newFixture(t)
	leavingID, leavingToken := f.newUserWithToken("要走的人")
	otherID := f.newUser("别人")
	f.ident.SetGameCleaner(f.games)

	err := f.st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO events (at, user_id, name, env) VALUES
			(1, ?1, 'detail_view', 'wechat'), (2, ?1, 'join_ok', 'wechat'), (3, ?2, 'detail_view', 'pwa')`,
			leavingID, otherID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	f.deleteUser(leavingID, leavingToken)

	if n := f.count(`SELECT count(*) FROM events WHERE user_id = ?`, leavingID); n != 0 {
		t.Fatalf("注销后埋点上不该再有这个人的 user_id，还有 %d 条", n)
	}
	if n := f.count(`SELECT count(*) FROM events WHERE name IN ('detail_view', 'join_ok')`); n != 3 {
		t.Fatalf("埋点本身要留着，应有 3 条，实际 %d", n)
	}
	if n := f.count(`SELECT count(*) FROM events WHERE user_id = ?`, otherID); n != 1 {
		t.Fatalf("别人的埋点不该动，实际 %d 条", n)
	}
}
