package games_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/scoring"
)

// ---------------------------------------------------------------------------
// 牌桌计分（#165，§5.6）
// ---------------------------------------------------------------------------

// fullGame 开一局并坐满：返回短码和 1–4 号座上的人。时钟拨到开局前 20 分钟（可以开始记分）。
func (f *fixture) fullGame() (string, [4]string) {
	f.t.Helper()
	var seats [4]string
	seats[0] = f.newUser("一号")
	code := f.newGame(seats[0])
	for i, name := range []string{"二号", "三号", "四号"} {
		seats[i+1] = f.newUser(name)
		if _, err := f.games.Join(context.Background(), code, seats[i+1]); err != nil {
			f.t.Fatalf("报名: %v", err)
		}
	}
	f.fake.Advance(2*time.Hour - 20*time.Minute)
	return code, seats
}

func (f *fixture) record(code, user string, expected int, head, partner int) (*games.Score, error) {
	return f.games.RecordRound(context.Background(), code, user, expected,
		scoring.Round{HeadSeat: head, PartnerPlace: partner}, "")
}

func (f *fixture) mustRecord(code, user string, expected int, head, partner int) *games.Score {
	f.t.Helper()
	sc, err := f.record(code, user, expected, head, partner)
	if err != nil {
		f.t.Fatalf("记第 %d 局: %v", expected, err)
	}
	return sc
}

func (f *fixture) mustStart(code, user string) *games.Score {
	f.t.Helper()
	sc, err := f.games.StartScore(context.Background(), code, user, "")
	if err != nil {
		f.t.Fatalf("开始记分: %v", err)
	}
	return sc
}

// 设计稿的例子走一遍服务：六局记完红方 J；改第 4 局后红方 10，第 6 局随之重算，记录留下谁改的。
func TestScoreDesignExampleThroughService(t *testing.T) {
	f := newFixture(t)
	code, seats := f.fullGame()

	sc := f.mustStart(code, seats[1])
	if !sc.Started || sc.Settings.Kind != scoring.KindClassic || !sc.CanRecord {
		t.Fatalf("%+v", sc)
	}
	example := [][2]int{{1, 2}, {4, 3}, {3, 4}, {1, 2}, {2, 2}, {1, 3}}
	for i, rd := range example {
		sc = f.mustRecord(code, seats[i%4], i+1, rd[0], rd[1])
	}
	if sc.State.Red.Level != 11 || sc.State.Black.Level != 7 || sc.State.PlayingLevel != 11 {
		t.Fatalf("%+v", sc.State)
	}
	if sc.Rounds[2].RecordedBy.ID != seats[2] || sc.Rounds[2].RecordedBy.Nickname != "三号" {
		t.Fatalf("第 3 局应是三号记的：%+v", sc.Rounds[2].RecordedBy)
	}

	f.fake.Advance(time.Minute)
	sc, err := f.games.EditRound(context.Background(), code, seats[3], 4,
		scoring.Round{HeadSeat: 1, PartnerPlace: 3}, &scoring.Round{HeadSeat: 1, PartnerPlace: 2})
	if err != nil {
		t.Fatalf("改第 4 局: %v", err)
	}
	if sc.State.Red.Level != 10 || sc.State.Playing != scoring.Red || sc.State.PlayingLevel != 10 {
		t.Fatalf("%+v", sc.State)
	}
	if r := sc.Rounds[5]; r.LevelBefore != 8 || r.LevelAfter != 10 {
		t.Fatalf("第 6 局应重算成 8→10：%+v", r.RoundResult)
	}
	r4 := sc.Rounds[3]
	if r4.EditedBy == nil || r4.EditedBy.ID != seats[3] || !r4.EditedAt.Equal(f.clk.Now().Truncate(time.Second)) || r4.RecordedBy.ID != seats[3] {
		t.Fatalf("第 4 局要留下谁记的、谁改的：%+v", r4)
	}
	// 读接口（访客）看到的是同一份。
	read, err := f.games.Score(context.Background(), code, "")
	if err != nil {
		t.Fatal(err)
	}
	if read.State != sc.State || read.CanRecord || read.Revision != sc.Revision {
		t.Fatalf("访客读到的不一样：%+v", read)
	}
	// 1（开始）+ 6（记）+ 1（改）。
	if sc.Revision != 8 {
		t.Fatalf("revision 应为 8，实际 %d", sc.Revision)
	}
	f.checkInvariants(code)
}

// 两人同时记同一局（S12）：后到的拿到 RoundConflict，带着已经记下的那一局。
func TestScoreRoundConflict(t *testing.T) {
	f := newFixture(t)
	code, seats := f.fullGame()
	f.mustStart(code, seats[0])
	f.mustRecord(code, seats[0], 1, 1, 2)

	_, err := f.record(code, seats[1], 1, 2, 3)
	var conflict *games.RoundConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("应返回 RoundConflict，实际 %v", err)
	}
	if conflict.Existing.No != 1 || conflict.Existing.HeadSeat != 1 || conflict.Existing.RecordedBy.ID != seats[0] {
		t.Fatalf("%+v", conflict.Existing)
	}
	if len(conflict.Latest.Rounds) != 1 {
		t.Fatalf("%+v", conflict.Latest)
	}
	// 跳号：客户端以为下一局是第 5 局。
	if _, err := f.record(code, seats[1], 5, 2, 3); !errors.Is(err, games.ErrInvalidInput) {
		t.Fatalf("跳号应是输入不合法，实际 %v", err)
	}
}

// 四个人同时点「记下第 1 局」：只有一条写进去，其余三个都是 RoundConflict（§11.2：靠写事务串行）。
func TestScoreConcurrentRecord(t *testing.T) {
	f := newFixture(t)
	code, seats := f.fullGame()
	f.mustStart(code, seats[0])

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		ok, taken int
	)
	for i := range seats {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.record(code, seats[i], 1, i+1, 2)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, games.ErrRoundConflict):
				taken++
			default:
				t.Errorf("意外的错误: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if ok != 1 || taken != 3 {
		t.Fatalf("应 1 成功 3 冲突，实际 %d / %d", ok, taken)
	}
	if n := f.count(`SELECT count(*) FROM score_rounds`); n != 1 {
		t.Fatalf("应只有 1 局，实际 %d", n)
	}
	f.checkInvariants(code)
}

// 开始记分：重复开始原样返回、开局约定不重来；no-level 的局按胜局记；太早不能开始。
func TestScoreStart(t *testing.T) {
	f := newFixture(t)
	code, seats := f.fullGame()

	f.mustStart(code, seats[0])
	sc := f.mustStart(code, seats[2])
	if sc.StartedBy.ID != seats[0] || sc.Revision != 1 || sc.Settings.Kind != scoring.KindClassic {
		t.Fatalf("第二次开始不该改开局约定：%+v", sc)
	}

	// 开局前 30 分钟之前不能开始。
	f3 := newFixture(t)
	host := f3.newUser("一号")
	code3 := f3.newGame(host)
	for _, name := range []string{"二号", "三号", "四号"} {
		if _, err := f3.games.Join(context.Background(), code3, f3.newUser(name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f3.games.StartScore(context.Background(), code3, host, ""); !errors.Is(err, games.ErrScoreTooEarly) {
		t.Fatalf("开局前 2 小时应 too_early，实际 %v", err)
	}
	f3.fake.Advance(90 * time.Minute) // 正好开局前 30 分钟
	f3.mustStart(code3, host)
}

func TestScoreNoLevelGame(t *testing.T) {
	f := newFixture(t)
	host := f.newUser("一号")
	detail, err := f.games.Create(context.Background(), host, games.CreateInput{
		StartsAt: f.clk.Now().Add(time.Hour), Area: "SCBD", Venue: "Pacific Place", Kind: "no-level",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	code := detail.Game.ShortCode
	seats := []string{host}
	for _, name := range []string{"二号", "三号", "四号"} {
		u := f.newUser(name)
		seats = append(seats, u)
		if _, err := f.games.Join(context.Background(), code, u); err != nil {
			t.Fatal(err)
		}
	}
	f.fake.Advance(40 * time.Minute)
	sc := f.mustStart(code, host)
	if sc.Settings.Kind != scoring.KindNoLevel {
		t.Fatalf("%+v", sc.Settings)
	}
	f.mustRecord(code, host, 1, 1, 2)
	sc = f.mustRecord(code, seats[1], 2, 2, 4)
	if sc.State.Red.Wins != 1 || sc.State.Red.DoubleUps != 1 || sc.State.Black.Wins != 1 || sc.State.Red.Level != 2 {
		t.Fatalf("%+v", sc.State)
	}
}

// 权限和牌局状态（§7.6）：只有座位上的人能写；有人退出、局回到 open 时不能记；结束了不能记；
// 没开始不能记；改一局时带的原结果过期了要 409；改成一样的结果什么也不写。
func TestScoreWritePermissions(t *testing.T) {
	f, rec := newEventFixture(t)
	code, seats := f.fullGame()
	outsider := f.newUser("旁观")

	if _, err := f.record(code, seats[0], 1, 1, 2); !errors.Is(err, games.ErrScoreNotStarted) {
		t.Fatalf("没开始应 score_not_started，实际 %v", err)
	}
	if _, err := f.games.StartScore(context.Background(), code, outsider, ""); !errors.Is(err, games.ErrNotSeated) {
		t.Fatalf("不在座位上应 not_seated，实际 %v", err)
	}
	f.mustStart(code, seats[0])
	if _, err := f.record(code, outsider, 1, 1, 2); !errors.Is(err, games.ErrNotSeated) {
		t.Fatalf("不在座位上应 not_seated，实际 %v", err)
	}
	f.mustRecord(code, seats[0], 1, 1, 2)

	// 原结果过期：别人已经把第 1 局改成了 1/3，我手里还是 1/2。
	if _, err := f.games.EditRound(context.Background(), code, seats[1], 1,
		scoring.Round{HeadSeat: 1, PartnerPlace: 3}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := f.games.EditRound(context.Background(), code, seats[2], 1,
		scoring.Round{HeadSeat: 2, PartnerPlace: 2}, &scoring.Round{HeadSeat: 1, PartnerPlace: 2})
	var changed *games.RoundChanged
	if !errors.As(err, &changed) || changed.Latest.Rounds[0].PartnerPlace != 3 {
		t.Fatalf("应 RoundChanged 并带上最新的，实际 %v", err)
	}
	if _, err := f.games.EditRound(context.Background(), code, seats[2], 9,
		scoring.Round{HeadSeat: 2, PartnerPlace: 2}, nil); !errors.Is(err, games.ErrRoundNotFound) {
		t.Fatalf("没有第 9 局应 round_not_found，实际 %v", err)
	}
	// 改成一样的：不写、不发事件、revision 不变。
	rec.reset()
	sc, err := f.games.EditRound(context.Background(), code, seats[2], 1, scoring.Round{HeadSeat: 1, PartnerPlace: 3}, nil)
	if err != nil || sc.Revision != 3 || len(rec.all()) != 0 {
		t.Fatalf("原样改一遍不该有变化：%v %+v %d 条事件", err, sc, len(rec.all()))
	}
	if sc.Rounds[0].EditedBy == nil || sc.Rounds[0].EditedBy.ID != seats[1] {
		t.Fatalf("改的人应还是二号：%+v", sc.Rounds[0].EditedBy)
	}

	// 有人退出（没有候补）：局回到 open，不能记，但还能读。
	if _, err := f.games.Leave(context.Background(), code, seats[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.record(code, seats[0], 2, 1, 2); !errors.Is(err, games.ErrNotFull) {
		t.Fatalf("没坐满应 not_full，实际 %v", err)
	}
	if sc, err := f.games.Score(context.Background(), code, seats[0]); err != nil || len(sc.Rounds) != 1 || sc.CanRecord {
		t.Fatalf("没坐满时仍能读、但不能记：%v %+v", err, sc)
	}

	// 局结束了（清理任务标成 done）：不能记。
	f.fake.Advance(7 * time.Hour)
	var gameID string
	if err := f.st.Read().QueryRow(`SELECT id FROM games WHERE short_code = ?`, code).Scan(&gameID); err != nil {
		t.Fatal(err)
	}
	if err := f.games.End(context.Background(), gameID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.record(code, seats[0], 2, 1, 2); !errors.Is(err, games.ErrClosed) {
		t.Fatalf("结束了应 game_closed，实际 %v", err)
	}
	f.checkInvariants(code)
}

// 实时事件（§5.5、§5.6）：提交后推到 game:{code}，不带顶层 v，data.revision 递增；
// 冲突、失败的写不推。
func TestScoreEvents(t *testing.T) {
	f, rec := newEventFixture(t)
	code, seats := f.fullGame()
	rec.reset()

	f.mustStart(code, seats[0])
	f.mustRecord(code, seats[1], 1, 2, 2)
	if _, err := f.record(code, seats[2], 1, 1, 2); err == nil {
		t.Fatal("应冲突")
	}
	if _, err := f.games.EditRound(context.Background(), code, seats[2], 1, scoring.Round{HeadSeat: 1, PartnerPlace: 2}, nil); err != nil {
		t.Fatal(err)
	}

	evs := rec.all()
	wantTypes := []string{games.EventScoreStarted, games.EventScoreRound, games.EventScoreEdited}
	if len(evs) != len(wantTypes) {
		t.Fatalf("应有 %d 条事件，实际 %+v", len(wantTypes), evs)
	}
	for i, ev := range evs {
		if ev.Type != wantTypes[i] || ev.Topic != games.GameTopic(code) || ev.V != 0 {
			t.Fatalf("第 %d 条：%+v", i, ev)
		}
		data := ev.Data.(map[string]any)
		if data["revision"].(int64) != int64(i+1) {
			t.Fatalf("revision 应为 %d：%v", i+1, data["revision"])
		}
	}
	round := evs[1].Data.(map[string]any)
	if round["by"].(games.UserRef).ID != seats[1] {
		t.Fatalf("score.round 的 by 应是记的人：%v", round["by"])
	}
	r1 := round["round"].(map[string]any)
	if r1["round_no"].(float64) != 1 || r1["winner"] != "black" || r1["level_after"].(float64) != 5 {
		t.Fatalf("%v", r1)
	}
	edited := evs[2].Data.(map[string]any)
	if edited["by"].(games.UserRef).ID != seats[2] {
		t.Fatalf("score.edited 的 by 应是改的人：%v", edited["by"])
	}
	if st := edited["state"].(scoring.State); st.Red.Level != 5 || st.Black.Level != 2 {
		t.Fatalf("%+v", st)
	}
	// 计分不动牌局版本号（局头改时间不会被记分挤成 409）。
	var version int64
	if err := f.st.Read().QueryRow(`SELECT version FROM games WHERE short_code = ?`, code).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if _, err := f.games.Update(context.Background(), code, seats[0], games.Patch{Note: ptr("记分中")}, version); err != nil {
		t.Fatalf("记分后局头照常改局：%v", err)
	}
}

func ptr[T any](v T) *T { return &v }

// ---------------------------------------------------------------------------
// HTTP（cookie 和小游戏的 Bearer 都能用）
// ---------------------------------------------------------------------------

func TestScoreOverHTTP(t *testing.T) {
	f := newServer(t)
	f.ident.SetWx(identity.DevWxAppID, identity.DevWxExchanger{})

	// 1、2、3 号是 H5（cookie），4 号是小游戏（Bearer）。
	browsers := []*http.Client{f.http, f.newBrowser(), f.newBrowser()}
	f.signUp(browsers[0], "一号")
	code := f.createGame(browsers[0], nil)
	for i, b := range browsers[1:] {
		f.signUp(b, []string{"二号", "三号"}[i])
		if r := f.do(b, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
			t.Fatalf("报名：%d %s", r.status, r.raw)
		}
	}
	login := f.do(f.newBrowser(), http.MethodPost, "/api/wx/login", map[string]string{"code": "dev:open-4", "nickname": "四号"}, nil)
	mini := f.newBrowser()
	bearer := map[string]string{"Authorization": "Bearer " + login.str("device_token")}
	if r := f.do(mini, http.MethodPost, "/api/games/"+code+"/seat", nil, bearer); r.status != http.StatusOK {
		t.Fatalf("小游戏报名：%d %s", r.status, r.raw)
	}

	guest := f.newBrowser()
	path := "/api/games/" + code + "/score"
	r := f.do(guest, http.MethodGet, path, nil, nil)
	if r.status != http.StatusOK || r.body["started"] != false || r.body["can_record"] != false {
		t.Fatalf("没开始：%d %s", r.status, r.raw)
	}
	if r := f.do(browsers[0], http.MethodPost, path, map[string]any{}, nil); r.status != http.StatusConflict || r.errCode() != "too_early" {
		t.Fatalf("太早：%d %s", r.status, r.raw)
	}
	f.clk.Advance(90 * time.Minute)

	// 访客不能写（401），不在座位上的人 403。
	if r := f.do(guest, http.MethodPost, path, map[string]any{}, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("访客：%d", r.status)
	}
	outsider := f.newBrowser()
	f.signUp(outsider, "旁观")
	if r := f.do(outsider, http.MethodPost, path, map[string]any{}, nil); r.status != http.StatusForbidden || r.errCode() != "not_seated" {
		t.Fatalf("旁观：%d %s", r.status, r.raw)
	}

	// 旧客户端带 `a_fail_limit` 也收（#205：这个约定没有了），只在响应里再也看不到它。
	r = f.do(mini, http.MethodPost, path, map[string]any{"a_fail_limit": 0}, bearer)
	if r.status != http.StatusOK || r.body["started"] != true || r.body["can_record"] != true {
		t.Fatalf("开始：%d %s", r.status, r.raw)
	}
	if s := r.body["settings"].(map[string]any); s["kind"] != "classic" || len(s) != 1 {
		t.Fatalf("约定里只该有 kind：%v", s)
	}

	rounds := path + "/rounds"
	example := [][2]int{{1, 2}, {4, 3}, {3, 4}, {1, 2}, {2, 2}, {1, 3}}
	for i, rd := range example {
		body := map[string]any{"expected_round_no": i + 1, "head_seat": rd[0], "partner_place": rd[1]}
		if i == 5 {
			r = f.do(mini, http.MethodPost, rounds, body, bearer)
		} else {
			r = f.do(browsers[i%3], http.MethodPost, rounds, body, nil)
		}
		if r.status != http.StatusOK {
			t.Fatalf("记第 %d 局：%d %s", i+1, r.status, r.raw)
		}
	}
	state := r.body["state"].(map[string]any)
	if state["red"].(map[string]any)["level"].(float64) != 11 || state["playing"] != "red" || state["playing_level"].(float64) != 11 {
		t.Fatalf("%v", state)
	}

	// S12：同一局再记一次 → 409 round_taken，带上已经记下的那一局和全部计分。
	r = f.do(browsers[1], http.MethodPost, rounds, map[string]any{"expected_round_no": 6, "head_seat": 2, "partner_place": 2}, nil)
	if r.status != http.StatusConflict || r.errCode() != "round_taken" {
		t.Fatalf("同一局：%d %s", r.status, r.raw)
	}
	taken := r.body["round"].(map[string]any)
	if taken["head_seat"].(float64) != 1 || taken["recorded_by"].(map[string]any)["nickname"] != "四号" {
		t.Fatalf("%v", taken)
	}
	if len(r.body["rounds"].([]any)) != 6 {
		t.Fatalf("409 里要带全部计分：%s", r.raw)
	}

	// 幂等键：同一次提交重发只记一局。
	key := map[string]string{"Idempotency-Key": "k-7"}
	body := map[string]any{"expected_round_no": 7, "head_seat": 2, "partner_place": 4}
	for i := 0; i < 2; i++ {
		if r = f.do(browsers[1], http.MethodPost, rounds, body, key); r.status != http.StatusOK {
			t.Fatalf("第 %d 次：%d %s", i+1, r.status, r.raw)
		}
	}
	if n := len(r.body["rounds"].([]any)); n != 7 {
		t.Fatalf("应 7 局，实际 %d", n)
	}
	// 同一个键用到别的请求上。
	if r := f.do(browsers[1], http.MethodPost, path, map[string]any{}, key); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("键复用：%d %s", r.status, r.raw)
	}

	// S10：改第 4 局。
	r = f.do(browsers[2], http.MethodPut, rounds+"/4", map[string]any{
		"head_seat": 1, "partner_place": 3, "was": map[string]any{"head_seat": 1, "partner_place": 2},
	}, nil)
	if r.status != http.StatusOK {
		t.Fatalf("改一局：%d %s", r.status, r.raw)
	}
	list := r.body["rounds"].([]any)
	r4 := list[3].(map[string]any)
	if r4["edited_by"].(map[string]any)["nickname"] != "三号" || r4["level_after"].(float64) != 8 {
		t.Fatalf("%v", r4)
	}
	if r6 := list[5].(map[string]any); r6["level_after"].(float64) != 10 {
		t.Fatalf("%v", r6)
	}
	// 原结果过期 → 409 round_changed；局号不对 → 400；没有这一局 → 404。
	r = f.do(browsers[0], http.MethodPut, rounds+"/4", map[string]any{
		"head_seat": 2, "partner_place": 2, "was": map[string]any{"head_seat": 1, "partner_place": 2},
	}, nil)
	if r.status != http.StatusConflict || r.errCode() != "round_changed" || r.body["rounds"] == nil {
		t.Fatalf("过期：%d %s", r.status, r.raw)
	}
	if r := f.do(browsers[0], http.MethodPut, rounds+"/x", map[string]any{"head_seat": 1, "partner_place": 2}, nil); r.status != http.StatusBadRequest {
		t.Fatalf("局号：%d", r.status)
	}
	if r := f.do(browsers[0], http.MethodPut, rounds+"/99", map[string]any{"head_seat": 1, "partner_place": 2}, nil); r.status != http.StatusNotFound || r.errCode() != "round_not_found" {
		t.Fatalf("没有这一局：%d %s", r.status, r.raw)
	}
	if r := f.do(browsers[0], http.MethodPost, rounds, map[string]any{"expected_round_no": 8, "head_seat": 5, "partner_place": 2}, nil); r.status != http.StatusBadRequest {
		t.Fatalf("座号：%d %s", r.status, r.raw)
	}

	// 访客读：和座位上的人看到的一样，只是不能记。
	r = f.do(guest, http.MethodGet, path, nil, nil)
	if r.status != http.StatusOK || r.body["can_record"] != false || len(r.body["rounds"].([]any)) != 7 {
		t.Fatalf("访客读：%d %s", r.status, r.raw)
	}
	if r := f.do(guest, http.MethodGet, "/api/games/ZZZZZZ/score", nil, nil); r.status != http.StatusNotFound {
		t.Fatalf("不存在的局：%d", r.status)
	}
}
