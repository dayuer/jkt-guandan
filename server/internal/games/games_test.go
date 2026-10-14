package games_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

type fixture struct {
	t     *testing.T
	st    *store.Store
	clk   clock.Clock
	fake  *clock.Fake
	ident *identity.Service
	games *games.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("执行迁移: %v", err)
	}

	fake := clock.NewFake(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	return &fixture{
		t:     t,
		st:    st,
		clk:   fake,
		fake:  fake,
		ident: identity.NewService(st, fake, true),
		games: games.NewService(st, fake, nil),
	}
}

// newUser 直接走身份服务建一个用户，省掉 HTTP 那一层。
func (f *fixture) newUser(nickname string) string {
	f.t.Helper()
	ident, _, err := f.ident.CreateOrGet(context.Background(), nil, nickname, "test-ua")
	if err != nil {
		f.t.Fatalf("建用户 %s: %v", nickname, err)
	}
	return ident.User.ID
}

// newGame 开一局，返回短码。局头自动坐 1 号座。
func (f *fixture) newGame(hostID string) string {
	f.t.Helper()
	detail, err := f.games.Create(context.Background(), hostID, games.CreateInput{
		StartsAt: f.clk.Now().Add(2 * time.Hour),
		Area:     "SCBD",
		Venue:    "Pacific Place",
		Kind:     "classic",
	}, "")
	if err != nil {
		f.t.Fatalf("开局: %v", err)
	}
	return detail.Game.ShortCode
}

// checkInvariants 显式跑一遍 §4 的数据不变量。
func (f *fixture) checkInvariants(code string) {
	f.t.Helper()

	var gameID string
	if err := f.st.Read().QueryRow(
		`SELECT id FROM games WHERE short_code = ?`, code).Scan(&gameID); err != nil {
		f.t.Fatalf("查牌局 %s: %v", code, err)
	}
	if err := store.CheckInvariants(context.Background(), f.st.Read(), gameID); err != nil {
		f.t.Fatalf("不变量被破坏：%v", err)
	}
}

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.st.Read().QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatalf("计数 %s: %v", query, err)
	}
	return n
}

// ---------------------------------------------------------------------------
// 报名与候补（§9）
// ---------------------------------------------------------------------------

// 50 个人抢 4 个座位：正好 4 人坐下、46 人进候补，候补不重不漏，
// 而且 full 提醒只产生一组（§9）。
func TestFiftyRaceForFourSeats(t *testing.T) {
	f := newFixture(t)

	host := f.newUser("局头")
	code := f.newGame(host)

	const racers = 49 // 加上已经在 1 号座的局头，一共 50 个人
	users := make([]string, racers)
	for i := range users {
		users[i] = f.newUser(fmt.Sprintf("牌友%02d", i))
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		gone = map[string]string{} // userID -> "seat" / "waitlist" / 错误
	)
	start := make(chan struct{})
	for _, u := range users {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			<-start // 尽量让大家同时冲
			detail, err := f.games.Join(context.Background(), code, u)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				gone[u] = "err:" + err.Error()
			default:
				gone[u] = detail.Me
			}
		}(u)
	}
	close(start)
	wg.Wait()

	seated, waitlisted := 0, 0
	for u, where := range gone {
		switch where {
		case "seat":
			seated++
		case "waitlist":
			waitlisted++
		default:
			t.Fatalf("用户 %s 报名失败：%s", u, where)
		}
	}

	if seated != 3 { // 局头已经占了 1 号座
		t.Fatalf("座位上一共应当 4 人（含局头），抢到的应当是 3 人，实际 %d 人", seated)
	}
	if waitlisted != racers-seated {
		t.Fatalf("候补应当 %d 人，实际 %d 人", racers-seated, waitlisted)
	}

	var gameID, status string
	if err := f.st.Read().QueryRow(
		`SELECT id, status FROM games WHERE short_code = ?`, code).Scan(&gameID, &status); err != nil {
		t.Fatalf("查牌局: %v", err)
	}
	if status != games.StatusFull {
		t.Fatalf("满员后状态应当是 full，实际 %s", status)
	}

	// 座位不重不漏。
	if n := f.count(`SELECT count(*) FROM seats WHERE game_id = ?`, gameID); n != 4 {
		t.Fatalf("应当正好 4 个座位，实际 %d", n)
	}
	if n := f.count(`SELECT count(DISTINCT user_id) FROM seats WHERE game_id = ?`, gameID); n != 4 {
		t.Fatalf("座位上不该有重复的人，实际 %d 个不同的人", n)
	}
	if n := f.count(`SELECT count(*) FROM waitlist WHERE game_id = ?`, gameID); n != 46 {
		t.Fatalf("候补应当 46 人，实际 %d", n)
	}
	if n := f.count(`SELECT count(DISTINCT user_id) FROM waitlist WHERE game_id = ?`, gameID); n != 46 {
		t.Fatalf("候补里不该有重复的人，实际 %d 个不同的人", n)
	}

	// 满员提醒只产生一组：4 个人各一条，不是每组一次。
	if n := f.count(
		`SELECT count(*) FROM outbox WHERE game_id = ? AND kind = ?`, gameID, games.ReminderFull); n != 4 {
		t.Fatalf("full 提醒应当正好 4 条，实际 %d", n)
	}

	f.checkInvariants(code)
}

// 退出时候补自动补位：既不丢人也不重复，状态在补位后仍然是 full（§5.1）。
func TestLeavePromotesWaitlistHead(t *testing.T) {
	f := newFixture(t)

	host := f.newUser("局头")
	code := f.newGame(host)

	var seated []string
	for i := 0; i < 3; i++ {
		u := f.newUser(fmt.Sprintf("就座%d", i))
		if _, err := f.games.Join(context.Background(), code, u); err != nil {
			t.Fatalf("报名: %v", err)
		}
		seated = append(seated, u)
	}
	first := f.newUser("候补一")
	second := f.newUser("候补二")
	for _, u := range []string{first, second} {
		if _, err := f.games.Join(context.Background(), code, u); err != nil {
			t.Fatalf("进候补: %v", err)
		}
	}

	// 一个坐着的退出，队首应当补上来。
	if _, err := f.games.Leave(context.Background(), code, seated[0]); err != nil {
		t.Fatalf("退出: %v", err)
	}

	var gameID, status string
	if err := f.st.Read().QueryRow(
		`SELECT id, status FROM games WHERE short_code = ?`, code).Scan(&gameID, &status); err != nil {
		t.Fatalf("查牌局: %v", err)
	}
	if status != games.StatusFull {
		t.Fatalf("有人补位后状态应当保持 full，实际 %s", status)
	}

	var promotedSeat int
	if err := f.st.Read().QueryRow(
		`SELECT seat_no FROM seats WHERE game_id = ? AND user_id = ?`, gameID, first).Scan(&promotedSeat); err != nil {
		t.Fatalf("候补队首应当补上座位: %v", err)
	}
	if n := f.count(`SELECT count(*) FROM waitlist WHERE game_id = ? AND user_id = ?`, gameID, first); n != 0 {
		t.Fatal("补位之后不该还留在候补里")
	}
	if n := f.count(`SELECT count(*) FROM seats WHERE game_id = ?`, gameID); n != 4 {
		t.Fatalf("座位上应当还是 4 人，实际 %d", n)
	}
	if n := f.count(`SELECT count(*) FROM outbox WHERE game_id = ? AND kind = ? AND user_id = ?`,
		gameID, games.ReminderPromoted, first); n != 1 {
		t.Fatalf("补位的人应当收到一条 promoted 提醒，实际 %d 条", n)
	}
	f.checkInvariants(code)
}

// 局头退出：先把局头转给座位上最早进来的人（§5.1）。
func TestHostLeaveTransfersHost(t *testing.T) {
	f := newFixture(t)

	host := f.newUser("局头")
	code := f.newGame(host)

	early := f.newUser("早来的")
	if _, err := f.games.Join(context.Background(), code, early); err != nil {
		t.Fatalf("报名: %v", err)
	}
	f.fake.Advance(time.Minute)
	late := f.newUser("晚来的")
	if _, err := f.games.Join(context.Background(), code, late); err != nil {
		t.Fatalf("报名: %v", err)
	}

	if _, err := f.games.Leave(context.Background(), code, host); err != nil {
		t.Fatalf("局头退出: %v", err)
	}

	var gameID, newHost, status string
	if err := f.st.Read().QueryRow(
		`SELECT id, host_id, status FROM games WHERE short_code = ?`, code).Scan(&gameID, &newHost, &status); err != nil {
		t.Fatalf("查牌局: %v", err)
	}
	if newHost != early {
		t.Fatalf("局头应当转给最早进来的 %s，实际 %s", early, newHost)
	}
	if status != games.StatusOpen {
		t.Fatalf("只剩 2 个人时状态应当是 open，实际 %s", status)
	}
	if n := f.count(`SELECT count(*) FROM outbox WHERE game_id = ? AND kind = ? AND user_id = ?`,
		gameID, games.ReminderHostTransfer, early); n != 1 {
		t.Fatalf("新局头应当收到一条 host_transferred，实际 %d 条", n)
	}
	f.checkInvariants(code)
}

// 局头退出而且座位空了：这一局直接取消，候补也收到通知（§5.1）。
func TestHostLeaveWithEmptySeatsCancels(t *testing.T) {
	f := newFixture(t)

	host := f.newUser("局头")
	code := f.newGame(host)
	for i := 0; i < 3; i++ {
		if _, err := f.games.Join(context.Background(), code, f.newUser(fmt.Sprintf("就座%d", i))); err != nil {
			t.Fatalf("报名: %v", err)
		}
	}
	waiter := f.newUser("候补")
	if _, err := f.games.Join(context.Background(), code, waiter); err != nil {
		t.Fatalf("进候补: %v", err)
	}

	// 先把其他人都退出，只剩局头一个人。
	for i := 0; i < 3; i++ {
		var u string
		if err := f.st.Read().QueryRow(
			`SELECT s.user_id FROM seats s JOIN games g ON g.id = s.game_id
			  WHERE g.short_code = ? AND s.user_id <> ? LIMIT 1`, code, host).Scan(&u); err != nil {
			t.Fatalf("取座位: %v", err)
		}
		// 每次退出都会补位，所以要把新补上来的人再退掉——循环几次直到只剩局头。
		if _, err := f.games.Leave(context.Background(), code, u); err != nil {
			t.Fatalf("退出: %v", err)
		}
		f.fake.Advance(time.Minute)
	}
	// 可能还有补位上来的，继续退。
	for {
		var u string
		err := f.st.Read().QueryRow(
			`SELECT s.user_id FROM seats s JOIN games g ON g.id = s.game_id
			  WHERE g.short_code = ? AND s.user_id <> ? LIMIT 1`, code, host).Scan(&u)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			t.Fatalf("取座位: %v", err)
		}
		if _, err := f.games.Leave(context.Background(), code, u); err != nil {
			t.Fatalf("退出: %v", err)
		}
	}

	// 现在只剩局头，他自己退出。
	if _, err := f.games.Leave(context.Background(), code, host); err != nil {
		t.Fatalf("局头退出: %v", err)
	}

	var status string
	if err := f.st.Read().QueryRow(
		`SELECT status FROM games WHERE short_code = ?`, code).Scan(&status); err != nil {
		t.Fatalf("查牌局: %v", err)
	}
	if status != games.StatusCancelled {
		t.Fatalf("座位空了应当取消这一局，实际 %s", status)
	}
	f.checkInvariants(code)
}

// ---------------------------------------------------------------------------
// 幂等与乐观锁（§9）
// ---------------------------------------------------------------------------

// 同一个幂等键并发提交两次：只开一次局，两次拿到同一局。
func TestIdempotencyKeyIsHonouredUnderConcurrency(t *testing.T) {
	f := newFixture(t)
	host := f.newUser("局头")

	in := games.CreateInput{
		StartsAt: f.clk.Now().Add(2 * time.Hour),
		Area:     "SCBD",
		Venue:    "Pacific Place",
		Kind:     "classic",
	}

	const key = "same-key-twice"
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		codes []string
		errs  []error
	)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			detail, err := f.games.Create(context.Background(), host, in, key)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			codes = append(codes, detail.Game.ShortCode)
		}()
	}
	close(start)
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("两次提交都应当成功（第二次回放），实际出错：%v", errs)
	}
	if len(codes) != 2 || codes[0] != codes[1] {
		t.Fatalf("同一个幂等键应当只开一局，实际拿到 %v", codes)
	}
	if n := f.count(`SELECT count(*) FROM games WHERE host_id = ?`, host); n != 1 {
		t.Fatalf("只应当有一局，实际 %d 局", n)
	}
}

// 两台设备同时改局：一个成功，另一个拿到 409 和最新数据（§9、§11.3）。
func TestUpdateVersionConflict(t *testing.T) {
	f := newFixture(t)
	host := f.newUser("局头")
	code := f.newGame(host)

	detail, err := f.games.Get(context.Background(), code, host)
	if err != nil {
		t.Fatalf("读牌局: %v", err)
	}
	version := detail.Game.Version

	newVenue := "Grand Indonesia"
	if _, err := f.games.Update(context.Background(), code, host,
		games.Patch{Venue: &newVenue}, version); err != nil {
		t.Fatalf("第一次修改应当成功: %v", err)
	}

	// 第二台设备还拿着旧版本号。
	another := "PIK Avenue"
	_, err = f.games.Update(context.Background(), code, host, games.Patch{Venue: &another}, version)
	var conflict *games.VersionConflict
	if err == nil {
		t.Fatal("拿旧版本号修改应当失败")
	}
	if !asVersionConflict(err, &conflict) {
		t.Fatalf("应当是 version_conflict，实际 %v", err)
	}
	if conflict.Latest.Game.Version == version {
		t.Fatal("409 里应当带上最新的版本号")
	}
}

func asVersionConflict(err error, out **games.VersionConflict) bool {
	for err != nil {
		if vc, ok := err.(*games.VersionConflict); ok {
			*out = vc
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// 只有局头能改、能取消、能移出。
func TestOnlyHostCanManage(t *testing.T) {
	f := newFixture(t)
	host := f.newUser("局头")
	code := f.newGame(host)
	other := f.newUser("别人")
	if _, err := f.games.Join(context.Background(), code, other); err != nil {
		t.Fatalf("报名: %v", err)
	}

	venue := "Grand Indonesia"
	if _, err := f.games.Update(context.Background(), code, other, games.Patch{Venue: &venue}, 1); err != games.ErrNotHost {
		t.Fatalf("非局头修改应当被拒，实际 %v", err)
	}
	if err := f.games.Cancel(context.Background(), code, other); err != games.ErrNotHost {
		t.Fatalf("非局头取消应当被拒，实际 %v", err)
	}
	if _, err := f.games.Kick(context.Background(), code, other, host); err != games.ErrNotHost {
		t.Fatalf("非局头移出应当被拒，实际 %v", err)
	}
	if _, err := f.games.Kick(context.Background(), code, host, host); err != games.ErrHostCantKickSelf {
		t.Fatalf("局头不能移出自己，实际 %v", err)
	}
}

// 被移出的人不能再报这一局（§5.1）。
func TestKickedUserIsBanned(t *testing.T) {
	f := newFixture(t)
	host := f.newUser("局头")
	code := f.newGame(host)
	target := f.newUser("捣乱的")
	if _, err := f.games.Join(context.Background(), code, target); err != nil {
		t.Fatalf("报名: %v", err)
	}

	if _, err := f.games.Kick(context.Background(), code, host, target); err != nil {
		t.Fatalf("移出: %v", err)
	}
	if _, err := f.games.Join(context.Background(), code, target); err != games.ErrBanned {
		t.Fatalf("被移出的人再报名应当返回 banned，实际 %v", err)
	}
	f.checkInvariants(code)
}

// ---------------------------------------------------------------------------
// 配额（§10.8）
// ---------------------------------------------------------------------------

func TestConcurrentHoldQuota(t *testing.T) {
	f := newFixture(t)
	host := f.newUser("局头")
	user := f.newUser("占座的")

	// 同时最多占 5 个未开始局的座位或候补位。
	for i := 0; i < games.MaxConcurrentHolds; i++ {
		code := f.newGame(host)
		if _, err := f.games.Join(context.Background(), code, user); err != nil {
			t.Fatalf("第 %d 次报名应当成功: %v", i+1, err)
		}
	}
	code := f.newGame(host)
	if _, err := f.games.Join(context.Background(), code, user); err == nil {
		t.Fatal("超过 5 个位置之后应当被配额拦下")
	}
}

func TestDailyGameQuota(t *testing.T) {
	f := newFixture(t)
	host := f.newUser("局头")

	for i := 0; i < games.MaxGamesPerDay; i++ {
		f.newGame(host)
	}
	in := games.CreateInput{
		StartsAt: f.clk.Now().Add(2 * time.Hour),
		Area:     "SCBD", Venue: "Pacific Place", Kind: "classic",
	}
	if _, err := f.games.Create(context.Background(), host, in, ""); err == nil {
		t.Fatal("超过每天 10 局之后应当被配额拦下")
	}

	// 第二天又可以开了。
	f.fake.Advance(24 * time.Hour)
	in.StartsAt = f.clk.Now().Add(2 * time.Hour)
	if _, err := f.games.Create(context.Background(), host, in, ""); err != nil {
		t.Fatalf("第二天应当可以再开: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 随机操作序列（属性测试，§9）
// ---------------------------------------------------------------------------

// 10 个用户、3 个局上随机跑 1000 步，每一步之后都检查全部不变量。
// 失败时把随机种子打出来，方便复现。
func TestRandomOperationsKeepInvariants(t *testing.T) {
	f := newFixture(t)

	const (
		users  = 10
		gamesN = 3
		steps  = 1000
		seed   = 20261014
	)

	ids := make([]string, users)
	for i := range ids {
		ids[i] = f.newUser(fmt.Sprintf("牌友%02d", i))
	}

	active := make([]string, 0, gamesN)
	for i := 0; i < gamesN; i++ {
		active = append(active, f.newGame(ids[i]))
	}

	rnd := rand.New(rand.NewSource(seed))
	ctx := context.Background()

	for step := 0; step < steps; step++ {
		idx := rnd.Intn(len(active))
		code := active[idx]
		user := ids[rnd.Intn(len(ids))]

		var err error
		switch rnd.Intn(6) {
		case 0:
			_, err = f.games.Join(ctx, code, user)
		case 1:
			_, err = f.games.Leave(ctx, code, user)
		case 2:
			_, err = f.games.Kick(ctx, code, hostOf(t, f, code), user)
		case 3:
			venue := fmt.Sprintf("地点%d", step)
			_, err = f.games.Update(ctx, code, hostOf(t, f, code), games.Patch{Venue: &venue},
				versionOf(t, f, code))
		case 4:
			err = f.games.Cancel(ctx, code, hostOf(t, f, code))
		case 5:
			_, err = f.games.Get(ctx, code, user)
		}

		// 业务上被拒绝是允许的（配额、被移出、已取消……），但不能是数据库或逻辑错误。
		if err != nil && !isExpectedBusinessError(err) {
			t.Fatalf("第 %d 步出错（seed=%d）：%v", step, seed, err)
		}

		f.checkInvariants(code)

		// 进入终态的局：再打几次，断言它真的不再变（审计要求 done/cancelled 是终态）。
		if err == nil && wasCancelled(t, f, code) {
			assertTerminal(t, f, code, ids)
			active = append(active[:idx], active[idx+1:]...)
			active = append(active, f.newGameForTest(ids[rnd.Intn(len(ids))]))
		}

		// 让时间往前走一点，创建时间和候补顺序才有区分度。
		if step%50 == 0 {
			f.fake.Advance(time.Minute)
		}
	}
}

func hostOf(t *testing.T, f *fixture, code string) string {
	t.Helper()
	var host string
	if err := f.st.Read().QueryRow(
		`SELECT host_id FROM games WHERE short_code = ?`, code).Scan(&host); err != nil {
		t.Fatalf("取局头: %v", err)
	}
	return host
}

func versionOf(t *testing.T, f *fixture, code string) int64 {
	t.Helper()
	var v int64
	if err := f.st.Read().QueryRow(
		`SELECT version FROM games WHERE short_code = ?`, code).Scan(&v); err != nil {
		t.Fatalf("取版本: %v", err)
	}
	return v
}

func wasCancelled(t *testing.T, f *fixture, code string) bool {
	t.Helper()
	var status string
	if err := f.st.Read().QueryRow(
		`SELECT status FROM games WHERE short_code = ?`, code).Scan(&status); err != nil {
		t.Fatalf("取状态: %v", err)
	}
	return status != games.StatusOpen && status != games.StatusFull
}

// isExpectedBusinessError 判断这个错误是业务上正常的拒绝，而不是 bug。
func isExpectedBusinessError(err error) bool {
	for _, want := range []error{
		games.ErrClosed, games.ErrBanned, games.ErrNotHost, games.ErrHostCantKickSelf,
		games.ErrQuota, games.ErrRateLimited, games.ErrNotFound, games.ErrInvalidInput,
		games.ErrNotInGame, games.ErrStarted,
	} {
		if errors.Is(err, want) {
			return true
		}
	}
	var conflict *games.VersionConflict
	return errors.As(err, &conflict)
}

// newGameForTest 开一局；碰到「每天最多 10 局」就把时钟往前推一天再试。
// 属性测试里会不断补新局，撞上日常配额是正常的，不是被测逻辑出了问题。
func (f *fixture) newGameForTest(hostID string) string {
	f.t.Helper()
	for attempt := 0; attempt < 5; attempt++ {
		detail, err := f.games.Create(context.Background(), hostID, games.CreateInput{
			StartsAt: f.clk.Now().Add(2 * time.Hour),
			Area:     "SCBD", Venue: "Pacific Place", Kind: "classic",
		}, "")
		switch {
		case err == nil:
			return detail.Game.ShortCode
		case errors.Is(err, games.ErrQuota):
			f.fake.Advance(24 * time.Hour)
		default:
			f.t.Fatalf("开局: %v", err)
		}
	}
	f.t.Fatal("连续被每日开局配额拦下")
	return ""
}
