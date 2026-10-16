package scoring

import (
	"errors"
	"testing"
)

// r 是测试里写一局的简写：头游座号 + 搭档名次。
func r(head, partner int) Round { return Round{HeadSeat: head, PartnerPlace: partner} }

// 红方 = 1、3 号；黑方 = 2、4 号（README §7.1「分队」）。
var (
	redDouble   = r(1, 2) // 红方头游，搭档二游（双上）
	redThird    = r(1, 3) // 红方头游，搭档三游
	redLast     = r(3, 4) // 红方头游，搭档末游
	blackDouble = r(2, 2)
	blackThird  = r(4, 3)
	blackLast   = r(2, 4)
)

var classic = Settings{Kind: KindClassic}

func mustCompute(t *testing.T, s Settings, rounds ...Round) Result {
	t.Helper()
	res, err := Compute(s, rounds)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return res
}

func TestTeamOfSeat(t *testing.T) {
	for seat, want := range map[int]Team{1: Red, 2: Black, 3: Red, 4: Black} {
		if got := TeamOf(seat); got != want {
			t.Errorf("TeamOf(%d) = %q，应为 %q", seat, got, want)
		}
	}
}

// 还没记任何一局：两队都在 2，打 2，第 1 场。
func TestInitialState(t *testing.T) {
	res := mustCompute(t, classic)
	st := res.State
	if st.Match != 1 || st.Red.Level != 2 || st.Black.Level != 2 || st.Playing != None || st.PlayingLevel != 2 || st.MatchOver {
		t.Fatalf("初始状态不对：%+v", st)
	}
	if len(res.Rounds) != 0 {
		t.Fatalf("没有局却算出了 %d 局", len(res.Rounds))
	}
}

// 规则表（README §7.1）：每一条一行。
func TestRules(t *testing.T) {
	// up 把一队从 2 升到 level：用对方不影响的若干局凑出来不方便，所以直接用前缀局。
	// 红方连续双上：2→5→8→J→A。
	redToA := []Round{redDouble, redDouble, redDouble, redDouble}

	type check func(t *testing.T, res Result)
	cases := []struct {
		name   string
		s      Settings
		rounds []Round
		check  check
	}{
		{
			name: "升级：搭档二游 +3",
			s:    classic, rounds: []Round{redDouble},
			check: func(t *testing.T, res Result) {
				rr := res.Rounds[0]
				if rr.Winner != Red || rr.Upgrade != 3 || rr.LevelBefore != 2 || rr.LevelAfter != 5 || !rr.DoubleUp {
					t.Fatalf("%+v", rr)
				}
			},
		},
		{
			name: "升级：搭档三游 +2",
			s:    classic, rounds: []Round{blackThird},
			check: func(t *testing.T, res Result) {
				if res.State.Black.Level != 4 || res.State.Red.Level != 2 || res.Rounds[0].DoubleUp {
					t.Fatalf("%+v", res.State)
				}
			},
		},
		{
			name: "升级：搭档末游 +1；输方不动",
			s:    classic, rounds: []Round{redDouble, blackLast},
			check: func(t *testing.T, res Result) {
				if res.State.Black.Level != 3 || res.State.Red.Level != 5 {
					t.Fatalf("%+v", res.State)
				}
			},
		},
		{
			name: "打谁的级：第 1 局打 2，之后打上一局赢的那一队的级",
			s:    classic, rounds: []Round{redDouble, blackThird, redLast},
			check: func(t *testing.T, res Result) {
				want := []struct {
					team  Team
					level int
				}{{None, 2}, {Red, 5}, {Black, 4}}
				for i, w := range want {
					if res.Rounds[i].Playing != w.team || res.Rounds[i].PlayingLevel != w.level {
						t.Fatalf("第 %d 局打 %q %d，应为 %q %d", i+1,
							res.Rounds[i].Playing, res.Rounds[i].PlayingLevel, w.team, w.level)
					}
				}
				if res.State.Playing != Red || res.State.PlayingLevel != 6 {
					t.Fatalf("下一局应打红方 6：%+v", res.State)
				}
			},
		},
		{
			name: "到 A 封顶",
			s:    classic, rounds: []Round{redDouble, redDouble, redDouble, redThird},
			check: func(t *testing.T, res Result) {
				// 2→5→8→J，J + 2 = K；再来一次双上 K + 3 越过 A 停在 A。
				res2 := mustCompute(t, classic, redDouble, redDouble, redDouble, redThird, blackLast, redDouble)
				if res.State.Red.Level != 13 || res2.State.Red.Level != A {
					t.Fatalf("应为 K 和 A：%d %d", res.State.Red.Level, res2.State.Red.Level)
				}
				last := res2.Rounds[5]
				if last.LevelBefore != 13 || last.LevelAfter != A || last.Upgrade != 3 {
					t.Fatalf("%+v", last)
				}
			},
		},
		{
			name: "过 A：打本队的 A，本队头游、搭档不是末游 → 本场结束，本队赢",
			s:    classic, rounds: append(append([]Round{}, redToA...), redThird),
			check: func(t *testing.T, res Result) {
				rr := res.Rounds[4]
				if rr.Playing != Red || rr.PlayingLevel != A || !rr.PassedA || rr.Match != 1 {
					t.Fatalf("%+v", rr)
				}
				if !res.State.MatchOver || res.State.Winner != Red || res.State.Match != 1 {
					t.Fatalf("%+v", res.State)
				}
			},
		},
		{
			name: "过 A：双上也算过",
			s:    classic, rounds: append(append([]Round{}, redToA...), redDouble),
			check: func(t *testing.T, res Result) {
				if !res.Rounds[4].PassedA || res.State.Winner != Red {
					t.Fatalf("%+v", res.Rounds[4])
				}
			},
		},
		{
			name: "打本队的 A，本队头游但搭档末游 → 没过，计一次",
			s:    classic, rounds: append(append([]Round{}, redToA...), redLast),
			check: func(t *testing.T, res Result) {
				rr := res.Rounds[4]
				if rr.PassedA || rr.FailedA != Red || rr.AFails != 1 || res.State.MatchOver {
					t.Fatalf("%+v", rr)
				}
				if res.State.Red.Level != A || res.State.Red.AFails != 1 {
					t.Fatalf("%+v", res.State)
				}
				// 赢的还是红方，下一局还打红方的 A。
				if res.State.Playing != Red || res.State.PlayingLevel != A {
					t.Fatalf("%+v", res.State)
				}
			},
		},
		{
			name: "对方头游算本队这次没过",
			s:    classic, rounds: append(append([]Round{}, redToA...), blackThird),
			check: func(t *testing.T, res Result) {
				rr := res.Rounds[4]
				if rr.FailedA != Red || rr.AFails != 1 || rr.Winner != Black {
					t.Fatalf("%+v", rr)
				}
				if res.State.Red.AFails != 1 || res.State.Black.Level != 4 || res.State.Playing != Black {
					t.Fatalf("%+v", res.State)
				}
			},
		},
		{
			name: "打对方的级时在 A 赢了不算过，只升级",
			// 红方到 A；黑方赢一局（红方第 1 次没过），下一局打黑方的 4；红方再双上：
			// 红方本来就在 A，封顶不动，不算过 A，也不算没过。
			s: classic, rounds: append(append([]Round{}, redToA...), blackThird, redDouble),
			check: func(t *testing.T, res Result) {
				rr := res.Rounds[5]
				if rr.Playing != Black || rr.PlayingLevel != 4 {
					t.Fatalf("应打黑方的 4：%+v", rr)
				}
				if rr.PassedA || rr.FailedA != None || res.State.MatchOver {
					t.Fatalf("打对方的级时赢了不能算过 A：%+v", rr)
				}
				if res.State.Red.Level != A || res.State.Red.AFails != 1 {
					t.Fatalf("%+v", res.State)
				}
				// 下一局打红方自己的 A。
				if res.State.Playing != Red || res.State.PlayingLevel != A {
					t.Fatalf("%+v", res.State)
				}
			},
		},
		{
			name: "打 A 没过：只计本队打自己 A 的局，打对方的级时输了不计",
			// 红方到 A，黑方赢（红 1 次），接着打黑方的 4，黑方又赢：这局打的是黑方的级，红方不计。
			s: classic, rounds: append(append([]Round{}, redToA...), blackThird, blackThird),
			check: func(t *testing.T, res Result) {
				if res.Rounds[5].FailedA != None || res.State.Red.AFails != 1 {
					t.Fatalf("%+v / %+v", res.Rounds[5], res.State)
				}
			},
		},
		{
			name: "A 必打：没过就一直打 A，没过几次也不回到 2（第二条 13）",
			s:    classic,
			rounds: append(append([]Round{}, redToA...),
				redLast, redLast, redLast, redLast, redLast),
			check: func(t *testing.T, res Result) {
				// 第 5 局起连续 5 次「头游＋末游」没过：红方一直在 A，次数 1…5 一直累加。
				for i, rr := range res.Rounds[4:] {
					if rr.PassedA || rr.FailedA != Red || rr.AFails != i+1 {
						t.Fatalf("第 %d 次没过：%+v", i+1, rr)
					}
					if rr.LevelBefore != A || rr.LevelAfter != A {
						t.Fatalf("没过不该动级数：%+v", rr)
					}
				}
				st := res.State
				if st.Red.Level != A || st.Red.AFails != 5 || st.MatchOver {
					t.Fatalf("%+v", st)
				}
				if st.Playing != Red || st.PlayingLevel != A {
					t.Fatalf("下一局还打红方的 A：%+v", st)
				}
			},
		},
		{
			name: "没过时对方照常升级，本队一直打 A",
			s:    classic, rounds: append(append([]Round{}, redToA...), blackLast, redLast, blackLast, redLast, blackLast),
			check: func(t *testing.T, res Result) {
				// 第 5 局黑头（红 1 次），第 6 局打黑 3 红赢 → 不计；第 7 局打红 A 黑赢（红 2 次）；
				// 第 8 局打黑 4，红赢 → 红方已在 A，封顶；第 9 局打红 A 黑赢（红 3 次，还在 A）。
				if res.State.Red.Level != A || res.State.Red.AFails != 3 || res.Rounds[8].FailedA != Red {
					t.Fatalf("%+v / %+v", res.State, res.Rounds[8])
				}
				if res.State.Black.Level != 5 || res.State.Playing != Black || res.State.PlayingLevel != 5 {
					t.Fatalf("%+v", res.State)
				}
			},
		},
		{
			name: "一场结束后再来一场：两队都从 2 开始，第 1 局打 2，A 没过次数清零，局号接着编",
			s:    classic,
			rounds: append(append([]Round{}, redToA...),
				redLast,  // 第 5 局：红没过 1 次
				redThird, // 第 6 局：过 A，第 1 场结束
				blackDouble),
			check: func(t *testing.T, res Result) {
				if res.Rounds[5].Match != 1 || !res.Rounds[5].PassedA {
					t.Fatalf("%+v", res.Rounds[5])
				}
				rr := res.Rounds[6]
				if rr.No != 7 || rr.Match != 2 || rr.Playing != None || rr.PlayingLevel != 2 || rr.LevelBefore != 2 || rr.LevelAfter != 5 {
					t.Fatalf("新一场第 1 局不对：%+v", rr)
				}
				st := res.State
				if st.Match != 2 || st.MatchOver || st.Red.Level != 2 || st.Red.AFails != 0 || st.Black.Level != 5 {
					t.Fatalf("%+v", st)
				}
				if st.MatchRounds != 1 || st.Red.Wins != 0 || st.Black.Wins != 1 || st.Black.DoubleUps != 1 {
					t.Fatalf("新一场的统计要清零：%+v", st)
				}
			},
		},
		{
			name: "一场结束时的状态：下一局还没记，场次不变，赢家和统计都在",
			s:    classic, rounds: append(append([]Round{}, redToA...), blackThird, redLast, redDouble),
			check: func(t *testing.T, res Result) {
				// 第 5 局黑赢（红 1 次），第 6 局打黑 4 红赢（不计），第 7 局打红 A 双上 → 过 A。
				st := res.State
				if !st.MatchOver || st.Winner != Red || st.Match != 1 || st.MatchRounds != 7 {
					t.Fatalf("%+v", st)
				}
				if st.Red.Wins != 6 || st.Red.DoubleUps != 5 || st.Black.Wins != 1 || st.Black.DoubleUps != 0 {
					t.Fatalf("%+v", st)
				}
				if st.Red.Level != A || st.Black.Level != 4 {
					t.Fatalf("停在哪一级要保留：%+v", st)
				}
			},
		},
		{
			name: "不升级玩法：只记每队赢了几局、双上几次，级数不动，也不会结束",
			s:    Settings{Kind: KindNoLevel},
			rounds: []Round{redDouble, redDouble, redDouble, redDouble, redDouble, redDouble,
				blackThird, redLast},
			check: func(t *testing.T, res Result) {
				st := res.State
				if st.Red.Wins != 7 || st.Red.DoubleUps != 6 || st.Black.Wins != 1 || st.Black.DoubleUps != 0 {
					t.Fatalf("%+v", st)
				}
				if st.MatchOver || st.Match != 1 || st.Red.Level != 2 || st.Black.Level != 2 {
					t.Fatalf("%+v", st)
				}
				for _, rr := range res.Rounds {
					if rr.Upgrade != 0 || rr.PassedA || rr.FailedA != None {
						t.Fatalf("不升级玩法不该有升级：%+v", rr)
					}
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, mustCompute(t, tc.s, tc.rounds...))
		})
	}
}

// 设计稿里的例子（README §7 / S9、S10）：红 = 1、3 号，黑 = 2、4 号。
//
//	① 打 2   红双上   红 2→5
//	② 打红 5 黑头+三  黑 2→4
//	③ 打黑 4 红头+末  红 5→6
//	④ 打红 6 红双上   红 6→9
//	⑤ 打红 9 黑双上   黑 4→7
//	⑥ 打黑 7 红头+三  红 9→J
func TestDesignExample(t *testing.T) {
	rounds := []Round{r(1, 2), r(4, 3), r(3, 4), r(1, 2), r(2, 2), r(1, 3)}
	type row struct {
		playing       Team
		playingLevel  int
		winner        Team
		before, after int
	}
	want := []row{
		{None, 2, Red, 2, 5},
		{Red, 5, Black, 2, 4},
		{Black, 4, Red, 5, 6},
		{Red, 6, Red, 6, 9},
		{Red, 9, Black, 4, 7},
		{Black, 7, Red, 9, 11},
	}
	res := mustCompute(t, classic, rounds...)
	for i, w := range want {
		got := res.Rounds[i]
		if got.No != i+1 || got.Playing != w.playing || got.PlayingLevel != w.playingLevel ||
			got.Winner != w.winner || got.LevelBefore != w.before || got.LevelAfter != w.after {
			t.Fatalf("第 %d 局：%+v，应为 %+v", i+1, got, w)
		}
	}
	if res.State.Red.Level != 11 || res.State.Black.Level != 7 || res.State.Playing != Red || res.State.PlayingLevel != 11 {
		t.Fatalf("%+v", res.State)
	}

	// 改第 4 局为「头游 + 三游」（S10）：第 4 局 6→8，第 6 局随之 8→10，红方现在 10，下一局打 10。
	edited := append([]Round{}, rounds...)
	edited[3] = r(1, 3)
	res = mustCompute(t, classic, edited...)
	if got := res.Rounds[3]; got.LevelBefore != 6 || got.LevelAfter != 8 {
		t.Fatalf("改后第 4 局：%+v", got)
	}
	if got := res.Rounds[4]; got.PlayingLevel != 8 || got.LevelBefore != 4 || got.LevelAfter != 7 {
		t.Fatalf("改后第 5 局：%+v", got)
	}
	if got := res.Rounds[5]; got.LevelBefore != 8 || got.LevelAfter != 10 {
		t.Fatalf("改后第 6 局：%+v", got)
	}
	if res.State.Red.Level != 10 || res.State.Playing != Red || res.State.PlayingLevel != 10 {
		t.Fatalf("%+v", res.State)
	}
}

// 改中间一局可以让后面的「过 A」消失：重算后后面的局回到同一场里。
func TestEditMiddleRoundUndoesMatchEnd(t *testing.T) {
	rounds := []Round{redDouble, redDouble, redDouble, redDouble, redThird, blackDouble}
	res := mustCompute(t, classic, rounds...)
	if res.Rounds[5].Match != 2 {
		t.Fatalf("改之前第 6 局应在第 2 场：%+v", res.Rounds[5])
	}
	rounds[0] = blackDouble // 第 1 局改成黑方双上：红方到不了 A
	res = mustCompute(t, classic, rounds...)
	for _, rr := range res.Rounds {
		if rr.Match != 1 || rr.PassedA {
			t.Fatalf("改了第 1 局后不该有过 A：%+v", rr)
		}
	}
}

// 纯函数：同样的输入，同样的输出；不改调用方的切片。
func TestComputeIsPure(t *testing.T) {
	rounds := []Round{redDouble, blackThird, redLast}
	a := mustCompute(t, classic, rounds...)
	b := mustCompute(t, classic, rounds...)
	if a.State != b.State || len(a.Rounds) != len(b.Rounds) {
		t.Fatal("两次结果不同")
	}
	if rounds[0] != redDouble {
		t.Fatal("改了输入")
	}
}

func TestValidate(t *testing.T) {
	bad := []Round{r(0, 2), r(5, 2), r(1, 1), r(1, 5), r(-1, 3)}
	for _, rd := range bad {
		if _, err := Compute(classic, []Round{rd}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v 应当不合法，得到 %v", rd, err)
		}
	}
	for _, s := range []Settings{{Kind: "teaching"}, {Kind: ""}} {
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v 应当不合法，得到 %v", s, err)
		}
	}
	for _, s := range []Settings{classic, {Kind: KindClassic}, {Kind: KindNoLevel}} {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v 应当合法：%v", s, err)
		}
	}
}

func TestLevelName(t *testing.T) {
	for lv, want := range map[int]string{2: "2", 10: "10", 11: "J", 12: "Q", 13: "K", 14: "A"} {
		if got := LevelName(lv); got != want {
			t.Errorf("LevelName(%d) = %q", lv, got)
		}
	}
}
