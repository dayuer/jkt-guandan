package games_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/scoring"
)

// ---------------------------------------------------------------------------
// 记分跟进（#168、#165 复审的两条意见）
// ---------------------------------------------------------------------------

// 同一个幂等键配不同的请求体：422，不当成重放，也不记。同一个请求体重发仍是重放。
func TestScoreIdemKeyFingerprint(t *testing.T) {
	f := newFixture(t)
	code, seats := f.fullGame()
	ctx := context.Background()

	if _, err := f.games.StartScore(ctx, code, seats[0], 3, "start-1"); err != nil {
		t.Fatal(err)
	}
	// 同一个键、不同的约定：422（约定本来也不会生效，但客户端以为生效了）。
	if _, err := f.games.StartScore(ctx, code, seats[0], 0, "start-1"); !errors.Is(err, games.ErrIdemKeyReused) {
		t.Fatalf("同一个键换约定应 ErrIdemKeyReused，实际 %v", err)
	}
	if _, err := f.games.StartScore(ctx, code, seats[0], 3, "start-1"); err != nil {
		t.Fatalf("原样重发应是重放：%v", err)
	}

	rd := scoring.Round{HeadSeat: 1, PartnerPlace: 2}
	if _, err := f.games.RecordRound(ctx, code, seats[1], 1, rd, "r-1"); err != nil {
		t.Fatal(err)
	}
	// 同一个键记下一局（客户端误把键复用了）：422，第 2 局不能被静默吞掉。
	_, err := f.games.RecordRound(ctx, code, seats[1], 2, scoring.Round{HeadSeat: 2, PartnerPlace: 3}, "r-1")
	if !errors.Is(err, games.ErrIdemKeyReused) {
		t.Fatalf("同一个键记别的局应 ErrIdemKeyReused，实际 %v", err)
	}
	// 同一局号、结果不同，也是别的请求。
	if _, err := f.games.RecordRound(ctx, code, seats[1], 1, scoring.Round{HeadSeat: 1, PartnerPlace: 3}, "r-1"); !errors.Is(err, games.ErrIdemKeyReused) {
		t.Fatalf("同一个键换结果应 ErrIdemKeyReused，实际 %v", err)
	}
	sc, err := f.games.RecordRound(ctx, code, seats[1], 1, rd, "r-1")
	if err != nil || len(sc.Rounds) != 1 {
		t.Fatalf("原样重发应是重放、仍只有 1 局：%v %+v", err, sc)
	}
	// 键是按人的：别人用同一个字符串不受影响。
	if _, err := f.games.RecordRound(ctx, code, seats[2], 2, scoring.Round{HeadSeat: 2, PartnerPlace: 3}, "r-1"); err != nil {
		t.Fatalf("别人用同一个键：%v", err)
	}
}

// can_start：在座位上、满座、还没开始、开局前 30 分钟起。开始以后为假，can_record 不变。
func TestScoreCanStart(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	host := f.newUser("一号")
	code := f.newGame(host)
	var seats = []string{host}
	for _, name := range []string{"二号", "三号", "四号"} {
		u := f.newUser(name)
		seats = append(seats, u)
		if _, err := f.games.Join(ctx, code, u); err != nil {
			t.Fatal(err)
		}
	}
	outsider := f.newUser("旁观")

	read := func(viewer string) *games.Score {
		t.Helper()
		sc, err := f.games.Score(ctx, code, viewer)
		if err != nil {
			t.Fatal(err)
		}
		return sc
	}

	// 开局前 2 小时：能记（座位上、满座），但还不能开始。
	if sc := read(host); sc.CanStart || !sc.CanRecord {
		t.Fatalf("太早：%+v", sc)
	}
	f.fake.Advance(90*time.Minute - time.Second)
	if sc := read(host); sc.CanStart {
		t.Fatalf("差 1 秒到开局前 30 分钟：%+v", sc)
	}
	f.fake.Advance(time.Second)
	if sc := read(host); !sc.CanStart {
		t.Fatalf("正好开局前 30 分钟应能开始：%+v", sc)
	}
	if sc := read(outsider); sc.CanStart || sc.CanRecord {
		t.Fatalf("旁观：%+v", sc)
	}
	if sc := read(""); sc.CanStart || sc.CanRecord {
		t.Fatalf("访客：%+v", sc)
	}

	// 有人退出、局回到 open：不能开始。
	if _, err := f.games.Leave(ctx, code, seats[3]); err != nil {
		t.Fatal(err)
	}
	if sc := read(host); sc.CanStart {
		t.Fatalf("没坐满：%+v", sc)
	}
	if _, err := f.games.Join(ctx, code, seats[3]); err != nil {
		t.Fatal(err)
	}

	f.mustStart(code, seats[2], 3)
	if sc := read(host); sc.CanStart || !sc.CanRecord {
		t.Fatalf("开始以后：%+v", sc)
	}
}

// startedGameAfterGrace 坐满、开始记分、记一局，再把时钟拨到开局后 JoinGrace 之外。
func (f *fixture) startedGameAfterGrace() (string, [4]string) {
	f.t.Helper()
	code, seats := f.fullGame()
	f.mustStart(code, seats[0], 3)
	f.mustRecord(code, seats[0], 1, 1, 2)
	f.fake.Advance(20*time.Minute + games.JoinGrace + time.Minute)
	return code, seats
}

func seatOf(t *testing.T, d *games.Detail, userID string) int {
	t.Helper()
	for _, s := range d.Seats {
		if s.UserID == userID {
			return s.SeatNo
		}
	}
	return 0
}

// #168：记分开始以后，原来坐在某个座号上的人过了 JoinGrace 还能回到自己空出来的那个座号，接着记分。
func TestScoreRejoinOwnSeatAfterGrace(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code, seats := f.startedGameAfterGrace()

	if _, err := f.games.Leave(ctx, code, seats[2]); err != nil { // 3 号退出
		t.Fatal(err)
	}
	if _, err := f.record(code, seats[0], 2, 1, 2); !errors.Is(err, games.ErrNotFull) {
		t.Fatalf("缺人应 not_full，实际 %v", err)
	}
	d, err := f.games.Join(ctx, code, seats[2])
	if err != nil {
		t.Fatalf("原座位的人应能回来：%v", err)
	}
	if got := seatOf(t, d, seats[2]); got != 3 {
		t.Fatalf("应回到 3 号，实际 %d", got)
	}
	if d.Game.Status != games.StatusFull {
		t.Fatalf("回来后应满座：%s", d.Game.Status)
	}
	sc := f.mustRecord(code, seats[2], 2, 3, 2)
	if len(sc.Rounds) != 2 || sc.Rounds[1].Winner != scoring.Red {
		t.Fatalf("回来后接着记，3 号仍是红方：%+v", sc.Rounds)
	}
	// 再走再回也行（重复提交是幂等的）。
	if _, err := f.games.Join(ctx, code, seats[2]); err != nil {
		t.Fatal(err)
	}
	f.checkInvariants(code)
}

// 别人、或者原座位的人想坐别的座号：过了 JoinGrace 一律不行。
func TestScoreRejoinOnlyOwnSeat(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code, seats := f.startedGameAfterGrace()
	stranger := f.newUser("路人")

	if _, err := f.games.Leave(ctx, code, seats[1]); err != nil { // 2 号退出
		t.Fatal(err)
	}
	if _, err := f.games.Join(ctx, code, stranger); !errors.Is(err, games.ErrStarted) {
		t.Fatalf("没坐过的人应 game_started，实际 %v", err)
	}
	// 4 号退出后想坐 2 号的空位：他原来的 4 号也空着，所以回 4 号，不会换队。
	if _, err := f.games.Leave(ctx, code, seats[3]); err != nil {
		t.Fatal(err)
	}
	d, err := f.games.Join(ctx, code, seats[3])
	if err != nil {
		t.Fatal(err)
	}
	if got := seatOf(t, d, seats[3]); got != 4 {
		t.Fatalf("应回到自己的 4 号，实际 %d", got)
	}

	// 自己的座号被人占了（JoinGrace 之内有人补上），过了 JoinGrace 就不能坐别的空位。
	f2 := newFixture(t)
	code2, seats2 := f2.fullGame()
	f2.mustStart(code2, seats2[0], 3)
	f2.fake.Advance(30 * time.Minute) // 开局后 10 分钟，还在 JoinGrace 之内
	if _, err := f2.games.Leave(ctx, code2, seats2[1]); err != nil {
		t.Fatal(err)
	}
	sub := f2.newUser("替补")
	d, err = f2.games.Join(ctx, code2, sub)
	if err != nil {
		t.Fatal(err)
	}
	if got := seatOf(t, d, sub); got != 2 {
		t.Fatalf("替补应补在 2 号，实际 %d", got)
	}
	f2.fake.Advance(games.JoinGrace)
	if _, err := f2.games.Leave(ctx, code2, seats2[3]); err != nil { // 4 号空出来
		t.Fatal(err)
	}
	if _, err := f2.games.Join(ctx, code2, seats2[1]); !errors.Is(err, games.ErrStarted) {
		t.Fatalf("原 2 号不能去坐 4 号，实际 %v", err)
	}
	// 替补退出：2 号最后是替补坐的，原 2 号也不能回去了，替补可以。
	if _, err := f2.games.Leave(ctx, code2, sub); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.games.Join(ctx, code2, seats2[1]); !errors.Is(err, games.ErrStarted) {
		t.Fatalf("2 号最后是替补坐的，原 2 号应 game_started，实际 %v", err)
	}
	if d, err := f2.games.Join(ctx, code2, sub); err != nil || seatOf(t, d, sub) != 2 {
		t.Fatalf("替补应能回 2 号：%v", err)
	}
	f2.checkInvariants(code2)
}

// 被局头移出的人还在 game_bans 里：不能回座位。
func TestScoreRejoinBanned(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code, seats := f.startedGameAfterGrace()

	if _, err := f.games.Kick(ctx, code, seats[0], seats[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.games.Join(ctx, code, seats[2]); !errors.Is(err, games.ErrBanned) {
		t.Fatalf("被移出的人应 banned，实际 %v", err)
	}
}

// 还没开始记分：照旧，过了 JoinGrace 谁都回不来；JoinGrace 之内回来按最小空座号。
func TestRejoinBeforeScoreStartedKeepsOldRule(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code, seats := f.fullGame()
	f.fake.Advance(20*time.Minute + games.JoinGrace + time.Minute)

	if _, err := f.games.Leave(ctx, code, seats[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.games.Join(ctx, code, seats[2]); !errors.Is(err, games.ErrStarted) {
		t.Fatalf("没开始记分应照旧 game_started，实际 %v", err)
	}

	f2 := newFixture(t)
	code2, seats2 := f2.fullGame()
	if _, err := f2.games.Leave(ctx, code2, seats2[3]); err != nil { // 4 号
		t.Fatal(err)
	}
	if _, err := f2.games.Leave(ctx, code2, seats2[1]); err != nil { // 2 号
		t.Fatal(err)
	}
	d, err := f2.games.Join(ctx, code2, seats2[3])
	if err != nil {
		t.Fatal(err)
	}
	if got := seatOf(t, d, seats2[3]); got != 2 {
		t.Fatalf("没开始记分时按最小空座号，应是 2 号，实际 %d", got)
	}
}

// 记分开始以后、JoinGrace 之内：原座位的人回原座号（两个空座时不会坐到对方队里去）。
func TestScoreRejoinWithinGracePrefersOwnSeat(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code, seats := f.fullGame()
	f.mustStart(code, seats[0], 3)

	if _, err := f.games.Leave(ctx, code, seats[3]); err != nil { // 4 号
		t.Fatal(err)
	}
	if _, err := f.games.Leave(ctx, code, seats[1]); err != nil { // 2 号
		t.Fatal(err)
	}
	d, err := f.games.Join(ctx, code, seats[3])
	if err != nil {
		t.Fatal(err)
	}
	if got := seatOf(t, d, seats[3]); got != 4 {
		t.Fatalf("应回到自己的 4 号，实际 %d", got)
	}
	// 没坐过的人照旧取最小空座号。
	other := f.newUser("新人")
	if d, err = f.games.Join(ctx, code, other); err != nil || seatOf(t, d, other) != 2 {
		t.Fatalf("新人应坐 2 号：%v", err)
	}
	f.checkInvariants(code)
}

// HTTP：同一个键不同请求体 422；can_start 在响应里；回原座位走 POST /seat。
func TestScoreFollowupsOverHTTP(t *testing.T) {
	f := newServer(t)
	browsers := []*http.Client{f.http, f.newBrowser(), f.newBrowser(), f.newBrowser()}
	f.signUp(browsers[0], "一号")
	code := f.createGame(browsers[0], nil)
	for i, b := range browsers[1:] {
		f.signUp(b, []string{"二号", "三号", "四号"}[i])
		if r := f.do(b, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
			t.Fatalf("报名：%d %s", r.status, r.raw)
		}
	}
	path := "/api/games/" + code + "/score"
	rounds := path + "/rounds"

	r := f.do(browsers[0], http.MethodGet, path, nil, nil)
	if r.status != http.StatusOK || r.body["can_start"] != false || r.body["can_record"] != true {
		t.Fatalf("太早：%d %s", r.status, r.raw)
	}
	f.clk.Advance(90 * time.Minute)
	if r := f.do(browsers[0], http.MethodGet, path, nil, nil); r.body["can_start"] != true {
		t.Fatalf("开局前 30 分钟：%s", r.raw)
	}
	if r := f.do(f.newBrowser(), http.MethodGet, path, nil, nil); r.body["can_start"] != false {
		t.Fatalf("访客：%s", r.raw)
	}
	r = f.do(browsers[0], http.MethodPost, path, map[string]any{}, nil)
	if r.status != http.StatusOK || r.body["can_start"] != false || r.body["can_record"] != true {
		t.Fatalf("开始：%d %s", r.status, r.raw)
	}

	key := map[string]string{"Idempotency-Key": "k-1"}
	if r := f.do(browsers[1], http.MethodPost, rounds, map[string]any{"expected_round_no": 1, "head_seat": 1, "partner_place": 2}, key); r.status != http.StatusOK {
		t.Fatalf("记第 1 局：%d %s", r.status, r.raw)
	}
	r = f.do(browsers[1], http.MethodPost, rounds, map[string]any{"expected_round_no": 2, "head_seat": 2, "partner_place": 2}, key)
	if r.status != http.StatusUnprocessableEntity || r.errCode() != "idempotency_key_reused" {
		t.Fatalf("同一个键不同请求体：%d %s", r.status, r.raw)
	}
	if r := f.do(browsers[1], http.MethodGet, path, nil, nil); len(r.body["rounds"].([]any)) != 1 {
		t.Fatalf("不该记下第 2 局：%s", r.raw)
	}

	// #168：过了 JoinGrace，3 号退出再回来，回到 3 号。
	f.clk.Advance(30*time.Minute + games.JoinGrace + time.Minute)
	if r := f.do(browsers[2], http.MethodDelete, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("退出：%d %s", r.status, r.raw)
	}
	stranger := f.newBrowser()
	f.signUp(stranger, "路人")
	if r := f.do(stranger, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusConflict || r.errCode() != "game_started" {
		t.Fatalf("路人：%d %s", r.status, r.raw)
	}
	if r := f.do(browsers[2], http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("回座：%d %s", r.status, r.raw)
	}
	if r := f.do(browsers[2], http.MethodPost, rounds, map[string]any{"expected_round_no": 2, "head_seat": 3, "partner_place": 2}, nil); r.status != http.StatusOK {
		t.Fatalf("回座后记分：%d %s", r.status, r.raw)
	}
}

// 注销：按正常退出离开记分中的局，但离座记录随个人数据一起删掉（他不会再回来）。
func TestScoreDeleteMeDropsSeatLeaver(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code, seats := f.fullGame()
	f.mustStart(code, seats[0], 3)
	u, token := f.newUserWithToken("五号")
	if _, err := f.games.Leave(ctx, code, seats[3]); err != nil {
		t.Fatal(err)
	}
	if d, err := f.games.Join(ctx, code, u); err != nil || seatOf(t, d, u) != 4 {
		t.Fatalf("五号应补在 4 号：%v", err)
	}
	f.deleteUser(u, token)
	if n := f.count(`SELECT count(*) FROM score_seat_leavers WHERE user_id = ?`, u); n != 0 {
		t.Fatalf("注销后不该留离座记录：%d", n)
	}
	// 4 号最后离开的是五号（注销也是离开），原 4 号那一行已被覆盖，他也不能再回 4 号。
	if n := f.count(`SELECT count(*) FROM score_seat_leavers WHERE user_id = ?`, seats[3]); n != 0 {
		t.Fatalf("4 号的离座记录应已被五号覆盖：%d", n)
	}
	f.checkInvariants(code)
}
