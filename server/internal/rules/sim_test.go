package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
)

// 随机对局（#204 第 7 步）：带种子跑几千副牌，把「发牌 → 出牌状态机」整条链路走一遍。
//
// 驱动器是一个**随机 AI**：轮到谁就从 `Moves` 给出的合法出法里随机挑一手，跟牌时按
// 一定概率过牌（要不起才过的话，「压得住却过牌」「一圈全过、回到领出」这两条路径
// 几乎走不到）。AI 自己不做任何判断 —— 出法全部来自 `Moves`、合法性全部由状态机校验，
// 所以这一层查的是**两个模块合起来**对不对：Moves 给的东西局面认不认、局面给的结果
// Moves 认不认。
//
// 天生盲区：**Moves 漏了某个牌型**，sim 看不出来 —— 它用的就是 Moves 给的出法，
// 漏了的那一种压根不会出现在对局里。那种漏法由暴力枚举对拍守着（moves_test.go、
// moves_old_test.go：把全部子集认一遍，和 Moves 逐条比）。
//
// 查的四类性质：
//
//	守恒   —— 全程每张牌要么在某家手里、要么已经打出去，不重不漏，总数就是一副牌的张数；
//	         出牌只从自己手里拿、拿走的张数正好等于这一手；
//	合法   —— 每一步都走合法的（`Play` / `Pass` 不返回错），跟牌真的压得住上一手，
//	         领出时 `Moves` 一定非空（手里有牌就至少能出单张）；
//	必然结束 —— 每一副都在有限步内分出上游，不会卡住或者死循环；
//	可重放 —— 一副牌打完存成 JSON 再读回来，局面和结果逐项对得上；照着存档重放两遍
//	         结果一致（引擎不读时间、不读随机数）。
//
// **进贡/还贡、下一局的接续不在这里**（第 6 步，见 issue #204）：这一版一副牌打完就停，
// 接第 6 步时在这个驱动器外面套一层「上一副的结果 → 下一副谁领出」就行。

const (
	// simFullCheckSteps 是每一步都灌非法动作的步数上限、simCheckEvery 是之后的抽样间隔：
	// 非法动作每次都要给局面拍快照（gameText 跟动作数同长），3000 副每步都拍太贵；
	// 被改坏的那几种（不校验压不压得住、领出允许过牌……）每一步都错，抽样足够抓到。
	simFullCheckSteps = 3
	simCheckEvery     = 15

	// simPassPercent 是「明明压得住也过牌」的概率（百分比）：真实牌局里常见（留牌、
	// 让搭档先走），模拟里不主动制造的话这条路走不到。
	simPassPercent = 25

	// simMaxSteps 是「必然结束」的上界，**推出来的**，不是量出来的：
	// 每出一手，后面最多跟 3 手过牌（在场另外三家各过一次，第 4 次过就结束这一圈，
	// 第八条 一、3）；出牌手数最多 107 手 —— 一副 108 张、一手至少出一张，

)

// simMaxSteps 是「必然结束」的上界，**推出来的**，不是量出来的：
// 每出一手，后面最多跟 3 手过牌（在场另外三家各过一次，第 4 次过就结束这一圈，第八条 一、3）；
// 出牌手数最多 107 手 —— 一副牌 108 张、一手至少出一张，而分出上游时场上至少还有一家
// 拿着牌（第八条 一、4）。所以步数 < 4×108。写成 len(NewDeck())：牌堆张数变了它跟着变。
var simMaxSteps = 4 * len(NewDeck())

// randomPerm 生成一个发牌排列：引擎自己不掷骰子，随机源从外面注入，
// 同一个种子跑出来的牌局完全一样（第 1 步的口径）。
func randomPerm(rnd *rand.Rand, n int) []int {
	return rnd.Perm(n)
}

// dealFor 发一副牌，并把「首抓者起逆时针第几家」旋转成座位号：
// 首抓者坐 leader 号座，其余按逆时针排（Deal 的下标不是座位号）。
func dealFor(rnd *rand.Rand, leader int) ([Seats][]Card, error) {
	hands, err := Deal(PermShuffler(randomPerm(rnd, len(NewDeck()))))
	if err != nil {
		return [Seats][]Card{}, fmt.Errorf("发牌：%w", err)
	}
	var bySeat [Seats][]Card
	for i := range hands {
		bySeat[(i+leader-1)%Seats] = hands[i]
	}
	return bySeat, nil
}

// playDeal 打完一副牌：从 leader 领出开始，一直走到分出上游。
// 返回结束后的局面、步数、借风次数（借风是第九条那条路径，得让调用方看得见它走到过）。
func playDeal(rnd *rand.Rand, level Rank, leader int) (*Game, int, int, error) {
	hands, err := dealFor(rnd, leader)
	if err != nil {
		return nil, 0, 0, err
	}
	g, err := NewGame(level, hands, leader)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("开一副牌：%w", err)
	}

	model := newSimModel(hands, leader)

	steps := 0
	for !g.Over() {
		steps++
		if steps > simMaxSteps {
			return nil, steps, model.borrowed, fmt.Errorf("打了 %d 步还没结束（上限 %d）—— 卡住了？局面：%s", steps, simMaxSteps, gameText(g))
		}
		seat := g.Turn()
		for _, done := range g.Finished() {
			if done == seat {
				return nil, steps, model.borrowed, fmt.Errorf("第 %d 步：轮到了已经出完的 %d 号座（出完的人不该再轮到）", steps, seat)
			}
		}

		lastSeat, _, wasFollowing := g.LastPlay()
		cards, pass, err := stepOnce(rnd, g, seat, level)
		if err != nil {
			return nil, steps, model.borrowed, fmt.Errorf("第 %d 步：%w", steps, err)
		}
		// 测试这边按规则独立记一遍，再和引擎逐步对（B1）。
		if pass {
			model.pass(seat)
		} else {
			model.play(seat, len(cards))
		}
		if err := model.check(g, steps, lastSeat, wasFollowing); err != nil {
			return nil, steps, model.borrowed, err
		}
		if err := checkHandsConserved(g); err != nil {
			return nil, steps, model.borrowed, fmt.Errorf("第 %d 步之后：%w", steps, err)
		}
		if steps <= simFullCheckSteps || steps%simCheckEvery == 0 {
			if err := checkIllegalActions(g, level); err != nil {
				return nil, steps, model.borrowed, fmt.Errorf("第 %d 步之后：%w", steps, err)
			}
		}
	}
	return g, steps, model.borrowed, nil
}

// stepOnce 是驱动器的一步：按 Moves 给的选择出牌或者过牌。
// 返回这一手打出去的牌（过牌是 nil）和「是不是过牌」，调用方拿去给 simModel 记账。
func stepOnce(rnd *rand.Rand, g *Game, seat int, level Rank) ([]Card, bool, error) {
	hand := g.Hand(seat)
	var lead *Combo
	if !g.Leading() {
		_, last, ok := g.LastPlay()
		if !ok {
			return nil, false, fmt.Errorf("%d 号座不是领出，却取不到上一手", seat)
		}
		lead = &last
	}

	moves, err := Moves(hand, level, lead)
	if err != nil {
		return nil, false, fmt.Errorf("算 %d 号座（手里 %s）的出法：%w", seat, cardsText(hand), err)
	}
	if g.Leading() && len(moves) == 0 {
		return nil, false, fmt.Errorf("领出时 %d 号座手里还有 %d 张牌，却一手都出不了（手里有牌至少能出单张）：%s",
			seat, len(hand), cardsText(hand))
	}
	if lead != nil {
		for _, m := range moves {
			if m.Compare(*lead) != Greater {
				return nil, false, fmt.Errorf("Moves 给了压不住上一手的出法：上一手 %s，给的是 %s", lead.String(), m.String())
			}
		}
	}

	if len(moves) == 0 || (lead != nil && rnd.Intn(100) < simPassPercent) {
		if err := g.Pass(seat); err != nil {
			return nil, false, fmt.Errorf("%d 号座过牌被拒（手里 %s，上一手 %s）：%w", seat, cardsText(hand), comboText(lead), err)
		}
		return nil, true, nil
	}

	m := moves[rnd.Intn(len(moves))]
	held := map[int]bool{}
	for _, c := range hand {
		held[c.ID] = true
	}
	for _, c := range m.Cards {
		if !held[c.ID] {
			return nil, false, fmt.Errorf("Moves 给的这一手用了不在 %d 号座手里的牌 %s，手牌 %s，这一手 %s",
				seat, c.Key(), cardsText(hand), m.String())
		}
	}
	if err := g.Play(seat, m.Cards, m.Type, m.Rank); err != nil {
		return nil, false, fmt.Errorf("局面不认 Moves 给的这一手：上一手 %s，Moves 给的 %s，局面说 %w",
			comboText(lead), m.String(), err)
	}
	return m.Cards, false, nil
}

// ───────────────────────────────────────────────────────────────────────────
// B1：测试自己按规则推一遍「该轮到谁、这一圈结束没有、这一副结束没有」，再和引擎对
// ───────────────────────────────────────────────────────────────────────────

// simModel 只吃**动作序列**（谁出了几张、谁过了），不看引擎的内部字段：引擎自己算一遍、
// 这里独立算一遍、逐步对比。下一家算错、借风给成别人、该结束不结束，这里都会立刻对不上
// —— auditor-246 的反证就是：把那几处改坏，原来这套 sim 全绿。
//
// 规则依据：第八条 一、1（逆时针按序出牌，出完的人跳过）、第八条 一、2（其余人可过牌）、
// 第八条 一、3（其余在场的人都过了，这一圈结束，最后出牌的人领出）、
// 第九条（领出的人已经出完时由搭档领出，借风）、第八条 一、4（三人出完，或者同一方
// 拿了上游和二游，这一副结束）。
type simModel struct {
	turn     int   // 该轮到谁；0 表示这一副已经结束
	lead     bool  // 现在是领出（这一圈还没人出牌）
	last     int   // 这一圈最后一个出牌的人；0 表示这一圈还没人出
	passes   int   // last 之后连着过了几家
	left     []int // 下标是座位号：手里还剩几张
	done     []int // 出完的先后
	borrowed int   // 借风发生了几次（第九条那条路走到过没有，要看得见）
}

func newSimModel(hands [Seats][]Card, leader int) *simModel {
	m := &simModel{turn: leader, lead: true, left: make([]int, Seats+1)}
	for i := range hands {
		m.left[i+1] = len(hands[i])
	}
	return m
}

func (m *simModel) active(seat int) bool { return m.left[seat] > 0 }

// next 是从 seat 逆时针数第一个还有牌的人。
func (m *simModel) next(seat int) int {
	for s := NextSeat(seat); s != seat; s = NextSeat(s) {
		if m.active(s) {
			return s
		}
	}
	return 0
}

// over 是「这一副结束了吗」：三人出完，或者同一方拿了上游和二游。
func (m *simModel) over() bool {
	if len(m.done) >= 3 {
		return true
	}
	return len(m.done) == 2 && PartnerSeat(m.done[0]) == m.done[1]
}

// play 记一手出牌：扣牌、记最后一个出牌的人、出完了记名次，然后定下一家。
func (m *simModel) play(seat, n int) {
	m.left[seat] -= n
	m.last, m.passes = seat, 0
	if m.left[seat] == 0 {
		m.done = append(m.done, seat)
	}
	m.lead = false
	if m.over() {
		m.turn = 0
		return
	}
	m.turn = m.next(seat)
}

// pass 记一次过牌：过到「在场的人都过了」这一圈就结束，由最后出牌的人领出。
func (m *simModel) pass(seat int) {
	m.passes++
	need := 0
	for s := 1; s <= Seats; s++ {
		if m.active(s) {
			need++
		}
	}
	if m.active(m.last) {
		need-- // 出牌的那一家不用自己过
	}
	if m.passes < need {
		m.turn = m.next(seat)
		return
	}
	leader := m.last
	if !m.active(leader) {
		// 出牌的人自己已经出完了：由搭档领出（第九条，借风）。
		leader = PartnerSeat(leader)
		m.borrowed++
	}
	m.last, m.passes, m.lead = 0, 0, true
	m.turn = leader
}

// check 拿引擎的局面和推出来的对：结束没有、出完先后、轮到谁、领出判定，外加借风那条。
func (m *simModel) check(g *Game, step int, lastSeat int, wasFollowing bool) error {
	if g.Over() != m.over() {
		return fmt.Errorf("第 %d 步：引擎说这一副结束了=%v，按规则推的是 %v（引擎出完 %v，手里 %s）",
			step, g.Over(), m.over(), g.Finished(), handsText(g))
	}
	if got, want := g.Finished(), m.done; !sameSeats(got, want) {
		return fmt.Errorf("第 %d 步：出完的先后对不上：引擎 %v，按规则推的是 %v", step, got, want)
	}
	if g.Over() {
		return nil
	}
	if g.Turn() != m.turn {
		return fmt.Errorf("第 %d 步：轮到谁对不上：引擎 %d 号座，按规则推的是 %d 号座（手里 %s）",
			step, g.Turn(), m.turn, handsText(g))
	}
	if g.Leading() != m.lead {
		return fmt.Errorf("第 %d 步：领出判定对不上：引擎 %v，按规则推的是 %v（手里 %s）",
			step, g.Leading(), m.lead, handsText(g))
	}
	// 一圈刚结束、领出的不是最后出牌的那个人 → 那是借风（第九条）：必须给搭档，而且
	// 他自己确实已经出完了。
	if wasFollowing && g.Leading() && g.Turn() != lastSeat {
		if g.Turn() != PartnerSeat(lastSeat) {
			return fmt.Errorf("第 %d 步：一圈结束后由 %d 号座领出，最后出牌的是 %d 号座 —— 不是搭档，不算借风",
				step, g.Turn(), lastSeat)
		}
		if len(g.Hand(lastSeat)) != 0 {
			return fmt.Errorf("第 %d 步：%d 号座手里还有 %d 张牌，却让别人领出（这不是借风）",
				step, lastSeat, len(g.Hand(lastSeat)))
		}
	}
	return nil
}

// ───────────────────────────────────────────────────────────────────────────
// B2：往局面里灌非法动作
// ───────────────────────────────────────────────────────────────────────────

// rejected 灌一个该被拒的动作进去，查三件事：被拒了、错误码是封闭错误码里的那一个、
// **局面一个字都没变**。只查「返回了错」不够 —— 改坏成「先改局面再报错」照样漏过去。
// before 是调用方在灌之前拍的快照（一批检查共用一张，省掉重复的地方 ——
// 抽样是每步都跑，gameText 跟动作数同长，重复拍 7 次就是 7 倍的字符串开销）。
func rejected(what string, want error, before string, g *Game, act func() error) error {
	err := act()
	if err == nil {
		return fmt.Errorf("%s：这个动作不合规则，却被接受了", what)
	}
	var rule *RuleError
	if !errors.As(err, &rule) {
		return fmt.Errorf("%s：报的不是规则错误（封闭错误码那一族）：%v", what, err)
	}
	if want != nil && !errors.Is(err, want) {
		return fmt.Errorf("%s：错误码不对，想要 %s，拿到 %v", what, reasonOf(want), err)
	}
	if after := gameText(g); after != before {
		return fmt.Errorf("%s：被拒之后局面变了\n之前：%s\n之后：%s", what, before, after)
	}
	return nil
}

// reasonOf 取哨兵的原因码（哨兵本身没有句子，直接印出来是空的）。
func reasonOf(err error) string {
	var rule *RuleError
	if errors.As(err, &rule) {
		return string(rule.Reason)
	}
	return fmt.Sprintf("%v", err)
}

// checkIllegalActions 往当前局面里灌几种非法动作（#246 复审 B2）。
//
// 为什么非要灌：只跑合法动作查不出「引擎根本不校验」这类毛病 —— auditor-246 把
// 「Play 不校验压不压得住」「领出允许过牌」「Pass 不推进 turn」改坏之后，原来这套 sim 全绿。
func checkIllegalActions(g *Game, level Rank) error {
	seat := g.Turn()
	if g.Over() || seat == 0 {
		return nil
	}
	hand := g.Hand(seat)
	other := NextSeat(seat)
	otherHand := g.Hand(other)
	before := gameText(g)

	// 没轮到这一家（第八条 一、1）：给的是别人手里的牌，也该先在「轮到谁」这一步被拒。
	var otherCard []Card
	if len(otherHand) > 0 {
		otherCard = otherHand[:1]
	}
	if err := rejected("没轮到这一家", ErrNotYourTurn, before, g, func() error {
		return g.Play(other, otherCard, TypeSingle, 0)
	}); err != nil {
		return err
	}

	// 领出的时候不能过牌（第八条 一、1）。
	if g.Leading() {
		if err := rejected("领出的时候过牌", ErrLeadingCannotPass, before, g, func() error {
			return g.Pass(seat)
		}); err != nil {
			return err
		}
	}

	// 出牌时一张牌都没给。
	if err := rejected("出牌时一张牌都没给", ErrNoCards, before, g, func() error {
		return g.Play(seat, nil, TypeSingle, 0)
	}); err != nil {
		return err
	}

	// 打手里没有的牌：一张别人的牌。
	if len(otherCard) > 0 {
		if err := rejected("打手里没有的牌", ErrCardsNotInHand, before, g, func() error {
			return g.Play(seat, otherCard, TypeSingle, 0)
		}); err != nil {
			return err
		}
		// 一手牌里混进别人的牌（张数不变）：自己的一张 + 别人的一张当对子。
		if len(hand) > 0 {
			mixed := []Card{hand[0], otherCard[0]}
			if err := rejected("一手里混进别人的牌", ErrCardsNotInHand, before, g, func() error {
				return g.Play(seat, mixed, TypePair, 0)
			}); err != nil {
				return err
			}
		}
	}

	// 声明的那一种解读不成立：两张点数不同的**实牌**当对子打。
	if a, b, ok := twoDifferent(hand, level); ok {
		if err := rejected("牌型不成立", ErrBadCombo, before, g, func() error {
			return g.Play(seat, []Card{a, b}, TypePair, 0)
		}); err != nil {
			return err
		}
	}

	// 压不住上一手：拿一张牌当单张打 —— 上一手不是单张时单张一定压不住（牌型不同，
	// 比不了大小）；上一手是单张时挑一张点数不比它大的。
	//
	// 这里刻意**不去调 Moves**：抽样是每步都跑，而 Moves 在满手牌 + 两张逢人配时是毫秒级，
	// 一趟下来要多花十几秒（#246 复审那轮试过）。这张牌一定压不住，不需要枚举。
	if !g.Leading() {
		if _, lead, ok := g.LastPlay(); ok {
			if c, ok := aCardNotBeating(hand, lead, level); ok {
				what := fmt.Sprintf("压不住上一手（上一手 %s，打单张 %s）", lead.String(), c.Key())
				if err := rejected(what, ErrDoesNotBeat, before, g, func() error {
					return g.Play(seat, []Card{c}, TypeSingle, 0)
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// twoDifferent 从手里挑两张**点数不同、而且都不是逢人配**的牌：这样的两张凑不成
// 任何一种牌型（逢人配在场的话，任意一张 + 逢人配都能凑成对子，那就不是非法动作了）。
func twoDifferent(hand []Card, level Rank) (Card, Card, bool) {
	for i := range hand {
		for j := i + 1; j < len(hand); j++ {
			if hand[i].IsWild(level) || hand[j].IsWild(level) {
				continue
			}
			if hand[i].Rank != hand[j].Rank {
				return hand[i], hand[j], true
			}
		}
	}
	return Card{}, Card{}, false
}

// aCardNotBeating 挑一张「当单张打一定压不住上一手」的牌。
//
// 判「压不压得住」用的是引擎自己的比大小（Combo.Compare），**不能拿 Rank 直接比**：
// 牌力看 power.json，级牌比 A 还大 —— 打 7 的时候一张 ♦7 当单张是压得住单张 J 的
// （#246 复审这一轮就是被这个绊了一次）。挑不到就返回 false，那一步跳过这一项检查。
func aCardNotBeating(hand []Card, lead Combo, level Rank) (Card, bool) {
	for _, c := range hand {
		// Cards 要带上：比大小先看张数，不填的话这一手是「0 张」，跟上一手比不出大小。
		single := Combo{
			Type: TypeSingle, Rank: c.Rank, Cards: []Card{c},
			Value: comboValue(TypeSingle, c.Rank, level),
		}
		if single.Compare(lead) != Greater {
			return c, true
		}
	}
	return Card{}, false
}

// handsText 把四家的剩牌写成一行，报错用。
func handsText(g *Game) string {
	out := ""
	for seat := 1; seat <= Seats; seat++ {
		if out != "" {
			out += " "
		}
		out += fmt.Sprintf("%d号:%d张", seat, len(g.Hand(seat)))
	}
	return out
}

// sameSeats 比两个座位序列（出完的先后）。
func sameSeats(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkHandsConserved 查「手里 + 已出 = 一开始发的那些」，每一步都查：
// 出牌只从自己手里拿、拿走的张数正好是这一手，别人手里的牌一张没动。
func checkHandsConserved(g *Game) error {
	played := map[int]int{} // 牌 ID → 谁打的
	for _, mv := range g.Moves() {
		if mv.Pass {
			continue
		}
		for _, c := range mv.Combo.Cards {
			if who, ok := played[c.ID]; ok {
				return fmt.Errorf("%s 被打了两次（%d 号座、%d 号座）", c.Key(), who, mv.Seat)
			}
			played[c.ID] = mv.Seat
		}
	}
	total := 0
	seen := map[int]string{}
	for seat := 1; seat <= Seats; seat++ {
		for _, c := range g.Hand(seat) {
			if _, dup := seen[c.ID]; dup {
				return fmt.Errorf("%s 同时在两家手里", c.Key())
			}
			if who, ok := played[c.ID]; ok {
				return fmt.Errorf("%s 已经由 %d 号座打出去了，还在 %d 号座手里", c.Key(), who, seat)
			}
			seen[c.ID] = c.Key()
			total++
		}
	}
	if total+len(played) != len(NewDeck()) {
		return fmt.Errorf("手里 %d 张 + 已出 %d 张 = %d，一副牌该有 %d 张",
			total, len(played), total+len(played), len(NewDeck()))
	}
	return nil
}

// checkFinishedDeal 查一副打完的牌局：结果自洽、守恒、能存档重放。
func checkFinishedDeal(g *Game) error {
	if !g.Over() {
		return fmt.Errorf("这一副没打完就检查结果了")
	}
	res, ok, err := g.Result()
	if err != nil {
		return fmt.Errorf("取结果：%w", err)
	}
	if !ok {
		return fmt.Errorf("打完了却取不到结果")
	}

	// 结果自洽：四家各出现一次、上游就是第一个出完的、搭档的名次是 2–4。
	if len(res.Order) != Seats {
		return fmt.Errorf("名次里该有四家，得到 %v", res.Order)
	}
	seenSeat := map[int]bool{}
	for _, s := range res.Order {
		if !validSeat(s) || seenSeat[s] {
			return fmt.Errorf("名次里有重复或越界的座位：%v", res.Order)
		}
		seenSeat[s] = true
	}
	fin := g.Finished()
	if len(fin) == 0 || res.HeadSeat != fin[0] {
		return fmt.Errorf("上游 %d 不是第一个出完的 %v", res.HeadSeat, fin)
	}
	if res.PartnerPlace < 2 || res.PartnerPlace > Seats {
		return fmt.Errorf("上游搭档的名次是 %d，应该在 2–%d 之间", res.PartnerPlace, Seats)
	}
	if res.Order[res.PartnerPlace-1] != PartnerSeat(res.HeadSeat) {
		return fmt.Errorf("搭档的名次对不上：名次表 %v，上游 %d，搭档名次 %d",
			res.Order, res.HeadSeat, res.PartnerPlace)
	}
	for seat := 1; seat <= Seats; seat++ {
		empty := len(g.Hand(seat)) == 0
		doneSeat := false
		for _, s := range fin {
			if s == seat {
				doneSeat = true
			}
		}
		if empty != doneSeat {
			return fmt.Errorf("%d 号座手里 %d 张牌，出完名单 %v —— 对不上", seat, len(g.Hand(seat)), fin)
		}
	}

	// 这一副结束了还在动：出牌、过牌都得报「这一副已经结束」。
	for _, act := range []struct {
		what string
		do   func() error
	}{
		{"结束后还出牌", func() error { return g.Play(1, nil, TypeSingle, 0) }},
		{"结束后还过牌", func() error { return g.Pass(1) }},
	} {
		if err := rejected(act.what, ErrGameOver, gameText(g), g, act.do); err != nil {
			return err
		}
	}

	if err := checkHandsConserved(g); err != nil {
		return err
	}
	return checkReplay(g)
}

// checkReplay 查「存档可重放」：存成 JSON 再读回来，局面、动作序列、结果逐项对得上；
// 同样的存档读两遍也完全一样（引擎不读时间、不读随机数）。
func checkReplay(g *Game) error {
	data, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("存档：%w", err)
	}

	var once, twice Game
	if err := json.Unmarshal(data, &once); err != nil {
		return fmt.Errorf("读回存档：%w", err)
	}
	if err := json.Unmarshal(data, &twice); err != nil {
		return fmt.Errorf("同一份存档读第二遍：%w", err)
	}
	if gameText(&once) != gameText(&twice) {
		return fmt.Errorf("同一份存档读两遍结果不一样：第一次 %s，第二次 %s", gameText(&once), gameText(&twice))
	}
	if got, want := gameText(&once), gameText(g); got != want {
		return fmt.Errorf("读回来的局面和存档前不一样：存档前 %s，读回来 %s", want, got)
	}
	if !once.Over() {
		return fmt.Errorf("读回来的局面应该是打完了的")
	}
	gotRes, ok1, err1 := once.Result()
	wantRes, ok2, err2 := g.Result()
	if err1 != nil || err2 != nil || !ok1 || !ok2 || !sameResult(gotRes, wantRes) {
		return fmt.Errorf("读回来的结果不一样：%v / %v（%v / %v）", gotRes, wantRes, err1, err2)
	}
	return nil
}

// gameText 把局面写成一行，用来比「读回来和原来的局面一不一样」。
func gameText(g *Game) string {
	out := ""
	for seat := 1; seat <= Seats; seat++ {
		out += cardsText(g.Hand(seat)) + "|"
	}
	out += "轮到" + string(rune('0'+g.Turn()))
	out += "|出完"
	for _, s := range g.Finished() {
		out += string(rune('0' + s))
	}
	out += "|动作"
	for _, mv := range g.Moves() {
		if mv.Pass {
			out += string(rune('0'+mv.Seat)) + "过;"
			continue
		}
		out += string(rune('0'+mv.Seat)) + mv.Combo.String() + ";"
	}
	return out
}

func comboText(c *Combo) string {
	if c == nil {
		return "（领出）"
	}
	return c.String()
}

func sameResult(a, b Result) bool {
	if a.HeadSeat != b.HeadSeat || a.PartnerPlace != b.PartnerPlace || len(a.Order) != len(b.Order) {
		return false
	}
	for i := range a.Order {
		if a.Order[i] != b.Order[i] {
			return false
		}
	}
	return true
}

// 跑几副牌由 simDeals 决定（两个 build tag 文件里各一份：不带 -race 是 3000 副，
// 带 -race 是 150 副 —— 本机常有几个会话同时跑 CI，跑满太费时间）。
// 一副牌要算上百次 Moves（领出那一档最重），所以按 CPU 数分给几个 goroutine ——
// 每一副牌自己一个随机源、自己的局面，互不相干，第 i 副的种子只跟 i 有关
// （**跟调度顺序无关**），红了照样能原样复现。

// simDeal 是第 i 副牌：种子由下标推出来，跟谁先跑、跑在哪个核上都没关系。
//
// 外面套一层 recover：一副牌把引擎跑 panic 了（比如局面进入自相矛盾的状态再取结果），
// 也只算**这一副**红 —— 不然整个测试二进制会挂在第一个 panic 上，其它副的结果全丢，
// 想复现还得先猜是哪一副（种子写死就是为了不用猜）。
func simDeal(i int) (steps, borrowed int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("引擎 panic 了：%v", r)
		}
	}()
	levels := []Rank{Two, Five, Seven, Ten, Ace}
	rnd := rand.New(rand.NewSource(int64(simSeedBase) + int64(i)))
	level := levels[i%len(levels)]
	leader := 1 + rnd.Intn(Seats)

	g, steps, borrowed, err := playDeal(rnd, level, leader)
	if err != nil {
		return steps, 0, fmt.Errorf("第 %d 副（打 %s，%d 号座领出）：%w", i+1, level.RankName(), leader, err)
	}
	if err := checkFinishedDeal(g); err != nil {
		return steps, 0, fmt.Errorf("第 %d 副（打 %s，%d 号座领出）：%w", i+1, level.RankName(), leader, err)
	}
	return steps, borrowed, nil
}

const simSeedBase = 20407

// TestSimManyDeals 跑几千副牌。种子写死：红了能原样再跑一遍（第 1 步的口径）。
func TestSimManyDeals(t *testing.T) {
	deals := simDeals
	if testing.Short() {
		deals = 200
	}

	stepsOf := make([]int, deals)
	borrowedOf := make([]int, deals)
	errs := make([]error, deals)
	workers := runtime.GOMAXPROCS(0)
	if workers > deals {
		workers = deals
	}
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				stepsOf[i], borrowedOf[i], errs[i] = simDeal(i)
			}
		}()
	}
	for i := 0; i < deals; i++ {
		next <- i
	}
	close(next)
	wg.Wait()

	stepsTotal, longest, failed, borrowed := 0, 0, 0, 0
	firstErr := error(nil)
	for i, err := range errs {
		stepsTotal += stepsOf[i]
		borrowed += borrowedOf[i]
		if stepsOf[i] > longest {
			longest = stepsOf[i]
		}
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		t.Fatalf("%d/%d 副牌没过，第一副：%v", failed, deals, firstErr)
	}
	t.Logf("跑了 %d 副牌（%d 个 goroutine）：一共 %d 步，最长一副 %d 步（上限 %d），借风 %d 次",
		deals, workers, stepsTotal, longest, simMaxSteps, borrowed)
}

// TestSimReplayIsDeterministic 单独钉「同一副牌重放两遍结果一样」：
// 同一个种子发两次牌、按同样的选择打，局面和结果必须一致。
func TestSimReplayIsDeterministic(t *testing.T) {
	for i := 0; i < 50; i++ {
		level := []Rank{Two, Seven, Ace}[i%3]
		leader := 1 + i%Seats

		first, steps, _, err := playDeal(rand.New(rand.NewSource(int64(1000+i))), level, leader)
		if err != nil {
			t.Fatalf("第 %d 副（打 %s，%d 号座领出）：%v", i+1, level.RankName(), leader, err)
		}
		second, steps2, _, err := playDeal(rand.New(rand.NewSource(int64(1000+i))), level, leader)
		if err != nil {
			t.Fatalf("第 %d 副重跑：%v", i+1, err)
		}
		if steps != steps2 {
			t.Fatalf("同一个种子跑出来步数不一样：%d / %d", steps, steps2)
		}
		if gameText(first) != gameText(second) {
			t.Fatalf("同一个种子跑出来局面不一样：\n%s\n%s", gameText(first), gameText(second))
		}
	}
}

// TestSimReplayRejectsTamperedArchive 反着查一次：把存档里的动作改掉，读回时必须报错
// （不然「能重放」就成了「读什么都当对」）。
func TestSimReplayRejectsTamperedArchive(t *testing.T) {
	g, _, _, err := playDeal(rand.New(rand.NewSource(20408)), Seven, 2)
	if err != nil {
		t.Fatalf("先打一副：%v", err)
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("存档：%v", err)
	}

	var archive map[string]any
	if err := json.Unmarshal(data, &archive); err != nil {
		t.Fatalf("拆存档：%v", err)
	}
	rawMoves, ok := archive["moves"].([]any)
	if !ok || len(rawMoves) == 0 {
		t.Fatalf("存档里没有动作序列：%s", data)
	}
	tampered := 0
	for _, mv := range rawMoves {
		one, ok := mv.(map[string]any)
		if !ok {
			continue
		}
		if cards, ok := one["cards"].([]any); ok && len(cards) > 0 {
			one["cards"] = append(cards, deckCardKey(t, 0)) // 多塞一张不该在这儿的牌
			tampered++
			break
		}
	}
	if tampered == 0 {
		t.Fatalf("没找到能动手的步骤：%s", data)
	}

	bad, err := json.Marshal(archive)
	if err != nil {
		t.Fatalf("改完再序列化：%v", err)
	}
	var back Game
	err = json.Unmarshal(bad, &back)
	if err == nil {
		t.Fatalf("改过的存档（多塞了一张牌）竟然读回来了")
	}
	var rule *RuleError
	if !errors.As(err, &rule) {
		t.Fatalf("改过的存档读回来报的不是规则错误（七个错误码之一），而是：%v", err)
	}
}

// deckCardKey 是牌堆里第 i 张的 key，用来往存档里塞一张不该在那儿的牌。
func deckCardKey(t *testing.T, i int) string {
	t.Helper()
	deck := NewDeck()
	if i < 0 || i >= len(deck) {
		t.Fatalf("牌堆里没有第 %d 张", i)
	}
	return deck[i].Key()
}
