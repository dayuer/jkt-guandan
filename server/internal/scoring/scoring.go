// Package scoring 是牌桌计分的规则（design/guandan-minigame/README.md §7.1，#165）。
//
// 纯函数：不读数据库、不看时间、不用随机数。输入是开局约定和每局的出游事实（头游座号 +
// 搭档名次），输出是每局之后的状态。数据库只存事实，级数、打谁的级、A 没过几次、
// 第几场、是否结束，一律由这里从第 1 局重算；改任意一局 = 替换这一局再算一遍（S10）。
//
// 口径按 2022《竞技掼蛋竞赛规则（试行）》（中国棋院审定）—— 和学牌页 app/h5/src/learn.ts
// 已发布的文案一致（#205）。条款号写在下面每条规则旁边。不涉及金钱：没有分数、倍率、结算（#91）。
package scoring

import (
	"errors"
	"fmt"
	"strconv"
)

// Team 是队伍。座位决定分队：1、3 号红方，2、4 号黑方，不选对家。
type Team string

const (
	None  Team = ""      // 还没有：一场的第 1 局打 2，不算哪一队的级
	Red   Team = "red"   // 1、3 号
	Black Team = "black" // 2、4 号
)

// TeamOf 返回座号所在的队；座号不在 1–4 返回 None。
func TeamOf(seat int) Team {
	switch seat {
	case 1, 3:
		return Red
	case 2, 4:
		return Black
	}
	return None
}

// 级数：2–10 是牌面，J=11、Q=12、K=13、A=14。
const (
	Two = 2
	A   = 14
)

// LevelName 把级数写成牌面（11 → J，14 → A）。
func LevelName(level int) string {
	switch level {
	case 11:
		return "J"
	case 12:
		return "Q"
	case 13:
		return "K"
	case 14:
		return "A"
	}
	return strconv.Itoa(level)
}

// 玩法。只有这两种：牌局的 teaching（教学局）按 classic 记。
const (
	KindClassic = "classic"
	KindNoLevel = "no-level"
)

// ErrInvalid 是输入不合法：座号、名次或开局约定超出范围。
var ErrInvalid = errors.New("计分输入不合法")

// Settings 是开局约定（S2）。整局只定一次，「再来一场」沿用。
//
// 没有「打 A 几次不过回到 2」这一项（#205）：2022 竞赛规则里没有这条约定，第二条 13「A 级必打」——
// 打不过就一直打 A，打到过为止，所以没有什么可选的。
type Settings struct {
	Kind string `json:"kind"`
}

// Validate 检查开局约定。
func (s Settings) Validate() error {
	if s.Kind != KindClassic && s.Kind != KindNoLevel {
		return fmt.Errorf("%w：玩法只能是 %s 或 %s", ErrInvalid, KindClassic, KindNoLevel)
	}
	return nil
}

// Round 是一局的事实：谁头游、他的搭档第几个出完。
type Round struct {
	HeadSeat     int `json:"head_seat"`     // 1–4
	PartnerPlace int `json:"partner_place"` // 2 二游（双上）/ 3 三游 / 4 末游
}

// Validate 检查一局的事实。
func (r Round) Validate() error {
	if r.HeadSeat < 1 || r.HeadSeat > 4 {
		return fmt.Errorf("%w：头游座号只能是 1–4", ErrInvalid)
	}
	if r.PartnerPlace < 2 || r.PartnerPlace > 4 {
		return fmt.Errorf("%w：搭档名次只能是 2、3、4", ErrInvalid)
	}
	return nil
}

// upgrade 是头游所在的队这一局升几级：搭档二游 +3、三游 +2、末游 +1（第二条 12「升级」）。
func (r Round) upgrade() int { return 5 - r.PartnerPlace }

// TeamState 是一队在本场里的状态。
type TeamState struct {
	Level int `json:"level"` // 现在的级数
	// AFails 是打自己的 A 已经没过几次，只用来展示（第二条 13「A 级必打」：打不过就一直打 A，
	// 所以它只涨不清零）。开新一场时清零。
	AFails int `json:"a_fails"`
	// Wins、DoubleUps 是本场赢了几局、其中双上几次（S8、S13、S15 的统计）。开新一场时清零。
	Wins      int `json:"wins"`
	DoubleUps int `json:"double_ups"`
}

// State 是某一局记完之后的状态，也就是「下一局」要看的东西。
type State struct {
	Match       int `json:"match"`        // 第几场，从 1 开始
	MatchRounds int `json:"match_rounds"` // 本场已经打了几局
	// Playing / PlayingLevel：下一局打哪一队的级、打几。一场的第 1 局 Playing 为 None，打 2。
	// 本场已经结束时，这里是下一场第 1 局的值（None、2）。
	Playing      Team      `json:"playing"`
	PlayingLevel int       `json:"playing_level"`
	Red          TeamState `json:"red"`
	Black        TeamState `json:"black"`
	// MatchOver：最后记的一局过了 A，本场结束；Winner 是赢的一队。再记一局就是下一场。
	MatchOver bool `json:"match_over"`
	Winner    Team `json:"winner,omitempty"`
}

func (s *State) team(t Team) *TeamState {
	if t == Red {
		return &s.Red
	}
	return &s.Black
}

// RoundResult 是一局事实加上这一局推出来的东西。
type RoundResult struct {
	No    int `json:"round_no"` // 局号，从 1 开始，跨场接着编
	Match int `json:"match"`
	Round
	// 这一局打的是哪一队的级、打几（第二条 14：牌点与上一局上游所升至级数相同的牌为级牌）。
	Playing      Team `json:"playing"`
	PlayingLevel int  `json:"playing_level"`
	Winner       Team `json:"winner"` // 头游所在的队
	DoubleUp     bool `json:"double_up"`
	// Upgrade 是按名次该升几级（3 / 2 / 1）；LevelBefore → LevelAfter 是赢的一队实际的级数变化，
	// 到 A 封顶（第二条 13）已经算进去。不升级玩法里三项都是 0。
	Upgrade     int `json:"upgrade"`
	LevelBefore int `json:"level_before"`
	LevelAfter  int `json:"level_after"`
	// PassedA：这一局过了 A，本场结束（第二条 13、第十二条：打 A 级的一方一家为上游、其搭档不是
	// 下游即过 A，一方过 A 比赛提前结束）。
	PassedA bool `json:"passed_a"`
	// FailedA：这一局是哪一队打自己的 A 没过（None 表示不是这种局）；AFails 是它记完后的次数。
	// 没过就一直打 A（第二条 13），只影响展示。
	FailedA Team `json:"failed_a,omitempty"`
	AFails  int  `json:"a_fails,omitempty"`
}

// Result 是 Compute 的输出。
type Result struct {
	Rounds []RoundResult `json:"rounds"`
	State  State         `json:"state"`
}

// newMatch 是第 match 场开打之前的状态。
func newMatch(match int) State {
	return State{
		Match:        match,
		PlayingLevel: Two,
		Red:          TeamState{Level: Two},
		Black:        TeamState{Level: Two},
	}
}

// Compute 从第 1 局重算到最后一局。rounds 按局号排好，第 i 个是第 i+1 局。
func Compute(s Settings, rounds []Round) (Result, error) {
	if err := s.Validate(); err != nil {
		return Result{}, err
	}
	st := newMatch(1)
	out := make([]RoundResult, 0, len(rounds))
	for i, rd := range rounds {
		if err := rd.Validate(); err != nil {
			return Result{}, fmt.Errorf("第 %d 局：%w", i+1, err)
		}
		// 上一局过了 A：这一局是新一场，两队都从 2 开始。
		if st.MatchOver {
			st = newMatch(st.Match + 1)
		}
		var rr RoundResult
		rr, st = apply(s, st, rd)
		rr.No = i + 1
		out = append(out, rr)
	}
	return Result{Rounds: out, State: st}, nil
}

// apply 记一局：返回这一局的结果和记完后的状态。st 是值，不改调用方的。
func apply(s Settings, st State, rd Round) (RoundResult, State) {
	winner := TeamOf(rd.HeadSeat)
	rr := RoundResult{
		Match:        st.Match,
		Round:        rd,
		Playing:      st.Playing,
		PlayingLevel: st.PlayingLevel,
		Winner:       winner,
		DoubleUp:     rd.PartnerPlace == 2,
	}
	st.MatchRounds++
	w := st.team(winner)
	w.Wins++
	if rr.DoubleUp {
		w.DoubleUps++
	}

	// 不升级玩法：只记哪队赢、双上几次，级数不动，也没有「一场结束」。
	if s.Kind == KindNoLevel {
		return rr, st
	}

	rr.Upgrade = rd.upgrade()
	rr.LevelBefore = w.Level

	// 只有打**本队的** A 才有「过」和「没过」（第二条 13「打 A 级的一方」）；打对方的级时
	// 赢了只升级（封顶在 A）。
	playingOwnA := st.Playing != None && st.team(st.Playing).Level == A
	// 过 A：打 A 级的一方一家为上游，其搭档不是下游（第二条 13）。过了整场就结束（第十二条）。
	if playingOwnA && winner == st.Playing && rd.PartnerPlace != 4 {
		rr.PassedA = true
		rr.LevelAfter = A
		st.MatchOver = true
		st.Winner = winner
		// 下一局是新一场：打 2，不算哪一队的。
		st.Playing = None
		st.PlayingLevel = Two
		return rr, st
	}

	w.Level = min(w.Level+rr.Upgrade, A)

	if playingOwnA {
		// 打自己的 A 没过：对方拿了头游，或者本队头游但搭档末游。A 级必打（第二条 13），
		// 没过就一直打 A —— 只把次数记上去给界面展示，级数不动。
		p := st.team(st.Playing)
		p.AFails++
		rr.FailedA = st.Playing
		rr.AFails = p.AFails
	}

	rr.LevelAfter = w.Level
	st.Playing = winner
	st.PlayingLevel = w.Level
	return rr, st
}
