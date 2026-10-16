package rules

import (
	"encoding/json"
	"fmt"
	"sort"
)

// 出牌状态机（#204 第 5 步）：一副牌从首圈领出到产生游数的全过程。
//
// 口径（2022《竞技掼蛋竞赛规则（试行）》）：
//   - 第八条 一、1：逆时针出牌。领出牌的人可以出任意牌型的一手牌，其余的人按序出更大的
//     一手牌压上去，或者过牌（第八条 一、2）。
//   - 第八条 一、3、第二条 8：其余在场的人都过牌，这一圈结束，最后出牌的人领出下一圈。
//   - 第九条（借风）：上游或二游打完最后一手牌、余下各家都不压时，由他的搭档领出下一圈。
//   - 第八条 一、4：三人出完，或者同一方拿了上游和二游，这一副结束。
//   - 第二条 10：游数按出完的先后排（上游、二游、三游、下游；同一方拿了上游、二游时对方双下）。
//
// 首圈谁领出由调用方给（和进贡有关，第十条；不在这一步）。能不能压上家用 Recognize / Pick /
// Compare 判断；出牌要带完整声明（牌型 + 牌面），引擎只校验，不替出牌的人挑（#204、#87 的口径）。
//
// 座位用 1–4：1 → 2 → 3 → 4 是逆时针，1、3 对家，2、4 对家（testdata/rules/README.md、
// server/internal/scoring 同一个口径）。
//
// 引擎不读时间、不读随机数：同样的初始局面和同样的动作序列，一定走到同样的局面（复盘、断线恢复）。

// Seats 是座位数。
const Seats = 4

// NextSeat 是逆时针的下一家。
func NextSeat(seat int) int { return seat%Seats + 1 }

// PartnerSeat 是对家。
func PartnerSeat(seat int) int { return (seat+1)%Seats + 1 }

func validSeat(seat int) bool { return seat >= 1 && seat <= Seats }

// Move 是一个动作：出一手牌，或者过牌。复盘就是从初始局面把 Moves 重放一遍。
type Move struct {
	Seat int
	Pass bool
	// 出牌时：打出去的牌和声明（牌型 + 牌面）。Combo 由引擎按声明认出来，不信调用方给的。
	Combo Combo
}

// Result 是一副牌的结果。
type Result struct {
	// Order 是出完的先后：Order[0] 是上游。双下时没出完的两家按座位号排在后面，同为下游。
	Order []int
	// HeadSeat 是上游的座位（1–4）。
	HeadSeat int
	// PartnerPlace 是上游的搭档第几个出完：2 二游（对方双下）、3 三游、4 下游。
	// 和 scoring.Round.PartnerPlace 是同一个口径（第二条 12：升三级、二级、一级）。
	PartnerPlace int
}

// Game 是一副牌打到一半的局面。零值不能用，用 NewGame 建。
type Game struct {
	level Rank
	hands [Seats + 1][]Card // 下标是座位号，0 不用
	turn  int               // 轮到谁（0 表示这一副已经结束）

	// 这一圈的情况。lastSeat 为 0 表示现在是领出（这一圈还没人出牌）。
	lastSeat  int
	lastCombo Combo
	passes    int // lastSeat 出牌之后，接连过牌的人数

	finished []int // 出完的先后
	moves    []Move
	// 首圈领出的人、初始手牌：复盘从这里开始。
	firstLeader int
	initial     [Seats + 1][]Card
}

// NewGame 开一副牌：hands[i] 是 i+1 号座的手牌，leader 是首圈领出的座位。
//
// 不要求每家正好 27 张（残局测试、教学关卡会用到少一些的牌），但同一张牌不能在两家手里
// 出现，每家至少一张。
func NewGame(level Rank, hands [Seats][]Card, leader int) (*Game, error) {
	if level < Two || level > Ace {
		return nil, fmt.Errorf("开局：级数 %d 超出 2–A", level)
	}
	if !validSeat(leader) {
		return nil, fmt.Errorf("开局：领出的座位 %d 不是 1–4", leader)
	}
	g := &Game{level: level, turn: leader, firstLeader: leader}
	seen := map[int]int{}
	for i, h := range hands {
		seat := i + 1
		if len(h) == 0 {
			return nil, fmt.Errorf("开局：%d 号座没有牌", seat)
		}
		for _, c := range h {
			if other, ok := seen[c.ID]; ok {
				return nil, fmt.Errorf("开局：%s 同时在 %d 号座和 %d 号座手里", c.Key(), other, seat)
			}
			seen[c.ID] = seat
		}
		g.hands[seat] = append([]Card{}, h...)
		g.initial[seat] = append([]Card{}, h...)
	}
	return g, nil
}

// Level 是这一副打几。
func (g *Game) Level() Rank { return g.level }

// Turn 是轮到谁（1–4）；这一副结束后是 0。
func (g *Game) Turn() int { return g.turn }

// Over 判断这一副是不是结束了。
func (g *Game) Over() bool { return g.turn == 0 }

// Hand 是这个座位手里还剩的牌（复制一份）。
func (g *Game) Hand(seat int) []Card {
	if !validSeat(seat) {
		return nil
	}
	return append([]Card{}, g.hands[seat]...)
}

// Leading 判断现在是不是领出（这一圈还没人出牌）：领出时不能过牌，什么牌型都能出。
func (g *Game) Leading() bool { return g.lastSeat == 0 }

// LastPlay 是这一圈最近的一手牌（被压的那一手）；领出时 ok 为 false。
func (g *Game) LastPlay() (seat int, combo Combo, ok bool) {
	if g.lastSeat == 0 {
		return 0, Combo{}, false
	}
	return g.lastSeat, g.lastCombo, true
}

// Finished 是已经出完的座位，按先后。
func (g *Game) Finished() []int { return append([]int{}, g.finished...) }

// Moves 是到现在为止的全部动作（复盘用）。
func (g *Game) Moves() []Move { return append([]Move{}, g.moves...) }

func (g *Game) done(seat int) bool { return len(g.hands[seat]) == 0 }

// Play 出一手牌：seat 出 cards，声明成牌型 t、牌面 rank（rank 为 0 表示「这个牌型只有一种解读」，
// 见 Pick）。成功时局面往前走；不合法时返回错误，局面不变。
func (g *Game) Play(seat int, cards []Card, t ComboType, rank Rank) error {
	if err := g.checkTurn(seat); err != nil {
		return err
	}
	if len(cards) == 0 {
		return fmt.Errorf("出牌：没有牌（不出请过牌）")
	}
	held := map[int]bool{}
	for _, c := range g.hands[seat] {
		held[c.ID] = true
	}
	for _, c := range cards {
		if !held[c.ID] {
			return fmt.Errorf("出牌：%d 号座手里没有 %s", seat, c.Key())
		}
	}
	combos, err := Recognize(cards, g.level)
	if err != nil {
		return fmt.Errorf("出牌：%w", err)
	}
	combo, ok := Pick(combos, t, rank)
	if !ok {
		if rank == 0 {
			return fmt.Errorf("出牌：这手牌当不成%s，或者有几种%s要说明牌面（能当成：%s）", t.label(), t.label(), ComboSummary(combos))
		}
		return fmt.Errorf("出牌：这手牌当不成%s %s（能当成：%s）", t.label(), rank.RankName(), ComboSummary(combos))
	}
	if g.lastSeat != 0 && !combo.Beats(g.lastCombo) {
		return fmt.Errorf("出牌：%s 压不住 %d 号座的 %s（第八条 一、1）", combo, g.lastSeat, g.lastCombo)
	}

	// 从手里拿掉。
	out := map[int]bool{}
	for _, c := range cards {
		out[c.ID] = true
	}
	rest := g.hands[seat][:0:0]
	for _, c := range g.hands[seat] {
		if !out[c.ID] {
			rest = append(rest, c)
		}
	}
	g.hands[seat] = rest
	g.moves = append(g.moves, Move{Seat: seat, Combo: combo})
	g.lastSeat, g.lastCombo, g.passes = seat, combo, 0

	if g.done(seat) {
		g.finished = append(g.finished, seat)
		if g.gameOver() {
			g.turn = 0
			return nil
		}
	}
	g.turn = g.nextActive(seat)
	return nil
}

// Pass 过牌（第八条 一、2）。领出时不能过。
func (g *Game) Pass(seat int) error {
	if err := g.checkTurn(seat); err != nil {
		return err
	}
	if g.lastSeat == 0 {
		return fmt.Errorf("过牌：%d 号座是领出，不能过牌（第八条 一、1）", seat)
	}
	g.moves = append(g.moves, Move{Seat: seat, Pass: true})
	g.passes++

	// 这一圈还有谁能接：除了出牌的那一家，在场的人都过了，这一圈就结束（第八条 一、3）。
	need := g.activeCount()
	if !g.done(g.lastSeat) {
		need-- // 出牌的那一家还在场，不用等他自己过
	}
	if g.passes < need {
		g.turn = g.nextActive(seat)
		return nil
	}
	// 这一圈结束：最后出牌的人领出；他已经出完了的话，由他的搭档领出（第九条，借风）。
	leader := g.lastSeat
	if g.done(leader) {
		leader = PartnerSeat(leader)
		if g.done(leader) {
			// 搭档也出完了：同一方拿了上游、二游，这一副在那时就该结束了，到不了这里。
			return fmt.Errorf("过牌：内部状态不对，%d 号座和搭档都出完了这一副却没结束", g.lastSeat)
		}
	}
	g.lastSeat, g.lastCombo, g.passes = 0, Combo{}, 0
	g.turn = leader
	return nil
}

func (g *Game) checkTurn(seat int) error {
	if g.turn == 0 {
		return fmt.Errorf("这一副已经结束")
	}
	if !validSeat(seat) {
		return fmt.Errorf("座位 %d 不是 1–4", seat)
	}
	if seat != g.turn {
		return fmt.Errorf("没轮到 %d 号座，现在轮到 %d 号座（第八条 一、1：逆时针按序出牌）", seat, g.turn)
	}
	return nil
}

func (g *Game) activeCount() int {
	n := 0
	for s := 1; s <= Seats; s++ {
		if !g.done(s) {
			n++
		}
	}
	return n
}

// nextActive 是 seat 之后逆时针第一个还没出完的座位（出完的人跳过）。
func (g *Game) nextActive(seat int) int {
	for s := NextSeat(seat); s != seat; s = NextSeat(s) {
		if !g.done(s) {
			return s
		}
	}
	return 0
}

// gameOver 判断这一副是不是结束了（第八条 一、4）。
func (g *Game) gameOver() bool {
	switch len(g.finished) {
	case 2:
		return PartnerSeat(g.finished[0]) == g.finished[1] // 同一方上游、二游，对方双下
	case 3:
		return true
	}
	return false
}

// Result 是这一副的结果；还没结束时 ok 为 false。
func (g *Game) Result() (Result, bool) {
	if !g.Over() {
		return Result{}, false
	}
	order := append([]int{}, g.finished...)
	var rest []int
	for s := 1; s <= Seats; s++ {
		if !g.done(s) {
			rest = append(rest, s)
		}
	}
	sort.Ints(rest)
	order = append(order, rest...)
	head := order[0]
	place := 0
	for i, s := range order {
		if s == PartnerSeat(head) {
			place = i + 1
		}
	}
	return Result{Order: order, HeadSeat: head, PartnerPlace: place}, true
}

// ---------------------------------------------------------------------------
// 序列化（断线恢复、复盘）
// ---------------------------------------------------------------------------
//
// 存的是「初始局面 + 动作序列」，读回来时把动作重放一遍得到当前局面：
//   - 存下来的东西最少，不会出现「当前手牌和动作对不上」这种自相矛盾的存档；
//   - 读回来的局面一定是引擎自己一步步走出来的，坏存档（动作不合法）会在读的时候报错。
// 牌一律按 Key 写（testdata/rules/README.md「还没定的」：不写 Go 的枚举序号）。
// 当前局面（轮到谁、这一圈的情况、游数）也写进去，方便别的端直接看；读的时候和重放的结果核对。

type wireMove struct {
	Seat  int      `json:"seat"`
	Pass  bool     `json:"pass,omitempty"`
	Cards []string `json:"cards,omitempty"`
	Type  string   `json:"type,omitempty"`
	Rank  string   `json:"rank,omitempty"` // 四大天王没有牌面，不写
}

type wireGame struct {
	Level    string     `json:"level"`
	Leader   int        `json:"leader"`
	Hands    [][]string `json:"hands"` // hands[i] 是 i+1 号座的初始手牌
	Moves    []wireMove `json:"moves"`
	Turn     int        `json:"turn"`
	Finished []int      `json:"finished"`
}

func keys(cards []Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Key()
	}
	return out
}

func parseKeys(ks []string) ([]Card, error) {
	out := make([]Card, len(ks))
	for i, k := range ks {
		c, err := ParseKey(k)
		if err != nil {
			return nil, err
		}
		out[i] = c
	}
	return out, nil
}

// MarshalJSON 写出「初始局面 + 动作序列」，以及当前轮到谁、谁出完了（核对用）。
func (g *Game) MarshalJSON() ([]byte, error) {
	w := wireGame{
		Level:    g.level.RankName(),
		Leader:   g.firstLeader,
		Hands:    make([][]string, Seats),
		Moves:    make([]wireMove, len(g.moves)),
		Turn:     g.turn,
		Finished: append([]int{}, g.finished...),
	}
	for s := 1; s <= Seats; s++ {
		w.Hands[s-1] = keys(g.initial[s])
	}
	for i, m := range g.moves {
		wm := wireMove{Seat: m.Seat, Pass: m.Pass}
		if !m.Pass {
			wm.Cards = keys(m.Combo.Cards)
			wm.Type = m.Combo.Type.String()
			if m.Combo.Type != TypeJokerBomb {
				wm.Rank = m.Combo.Rank.RankName()
			}
		}
		w.Moves[i] = wm
	}
	return json.Marshal(w)
}

// UnmarshalJSON 读回存档：按初始局面开局，把动作一步步重放；任何一步不合法、或者重放出来的
// 当前局面和存档里写的对不上，都返回错误（坏存档要立刻知道，不能悄悄接着打）。
func (g *Game) UnmarshalJSON(data []byte) error {
	var w wireGame
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("读局面：%w", err)
	}
	level, err := ParseRank(w.Level)
	if err != nil {
		return fmt.Errorf("读局面：级数：%w", err)
	}
	if len(w.Hands) != Seats {
		return fmt.Errorf("读局面：要 %d 家的手牌，存档里是 %d 家", Seats, len(w.Hands))
	}
	var hands [Seats][]Card
	for i, h := range w.Hands {
		if hands[i], err = parseKeys(h); err != nil {
			return fmt.Errorf("读局面：%d 号座的手牌：%w", i+1, err)
		}
	}
	fresh, err := NewGame(level, hands, w.Leader)
	if err != nil {
		return fmt.Errorf("读局面：%w", err)
	}
	for i, m := range w.Moves {
		if m.Pass {
			err = fresh.Pass(m.Seat)
		} else {
			var cards []Card
			var t ComboType
			var r Rank
			if cards, err = parseKeys(m.Cards); err == nil {
				if t, err = ParseComboType(m.Type); err == nil && m.Rank != "" {
					r, err = ParseRank(m.Rank)
				}
			}
			if err == nil {
				err = fresh.Play(m.Seat, cards, t, r)
			}
		}
		if err != nil {
			return fmt.Errorf("读局面：第 %d 步重放不过去：%w", i+1, err)
		}
	}
	if fresh.turn != w.Turn {
		return fmt.Errorf("读局面：重放之后轮到 %d 号座，存档里写的是 %d 号座", fresh.turn, w.Turn)
	}
	if fmt.Sprint(fresh.finished) != fmt.Sprint(nonNilInts(w.Finished)) {
		return fmt.Errorf("读局面：重放之后出完的是 %v，存档里写的是 %v", fresh.finished, w.Finished)
	}
	*g = *fresh
	return nil
}

func nonNilInts(s []int) []int {
	if s == nil {
		return []int{}
	}
	return s
}
