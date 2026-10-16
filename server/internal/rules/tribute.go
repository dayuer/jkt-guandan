package rules

import (
	"fmt"

	"github.com/dayuer/jkt-guandan/server/internal/scoring"
)

// TributeReason 是「这一步不合规则」的**封闭取值**（和出牌那边的 RejectReason 一个套路）。
//
// 牌例只比取值、不比给人看的句子（第十条这边十个取值见下面那张表）：句子随时能改措辞，
// 取值改了就是改协议。取值名和 #204 评论 6213375545 第三节那张表逐字对应。
type TributeReason string

const (
	ReasonTributeResisted       TributeReason = "resisted"         // 抗贡了还要贡 / 还
	ReasonTributeNotAGiver      TributeReason = "not-a-giver"      // 不用进贡的人来贡
	ReasonTributeAlreadyGave    TributeReason = "already-gave"     // 同一家贡了两次
	ReasonTributeNotBiggest     TributeReason = "not-biggest"      // 贡的不是手里最大的一张
	ReasonTributeWildCannotGive TributeReason = "wild-cannot-give" // 拿红心级牌去贡
	ReasonTributeCardNotInHand  TributeReason = "card-not-in-hand" // 贡 / 还手里没有的牌
	ReasonTributeNotAllGiven    TributeReason = "not-all-given"    // 还有人没贡完就还牌
	ReasonTributeNotAReceiver   TributeReason = "not-a-receiver"   // 没收贡的人来还
	ReasonTributeAlreadyRet     TributeReason = "already-returned" // 同一家还了两次
	ReasonTributeCannotReturn   TributeReason = "cannot-return"    // 还的牌不合「不超过 10 / 全大于 10 还最小」
)

// TributeError 是「这一步不合规则」。Error() 是给人看的句子，Reason 是上面的封闭取值。
//
// 判别用 errors.Is，不用拿句子做字符串比对：
//
//	if errors.Is(err, rules.ErrTributeNotBiggest) { ... }
type TributeError struct {
	Reason  TributeReason
	Message string
}

func (e *TributeError) Error() string { return e.Message }

// Is 让 errors.Is 按 **Reason** 判：调用方拿 Reason 的哨兵去问，不关心句子的措辞。
func (e *TributeError) Is(target error) bool {
	t, ok := target.(*TributeError)
	return ok && t.Reason == e.Reason
}

// 每个原因一个哨兵，哨兵本身没有句子，只用来比 Reason。
var (
	ErrTributeResisted        = &TributeError{Reason: ReasonTributeResisted}
	ErrTributeNotAGiver       = &TributeError{Reason: ReasonTributeNotAGiver}
	ErrTributeAlreadyGave     = &TributeError{Reason: ReasonTributeAlreadyGave}
	ErrTributeNotBiggest      = &TributeError{Reason: ReasonTributeNotBiggest}
	ErrTributeWildCannotGive  = &TributeError{Reason: ReasonTributeWildCannotGive}
	ErrTributeCardNotInHand   = &TributeError{Reason: ReasonTributeCardNotInHand}
	ErrTributeNotAllGiven     = &TributeError{Reason: ReasonTributeNotAllGiven}
	ErrTributeNotAReceiver    = &TributeError{Reason: ReasonTributeNotAReceiver}
	ErrTributeAlreadyReturned = &TributeError{Reason: ReasonTributeAlreadyRet}
	ErrTributeCannotReturn    = &TributeError{Reason: ReasonTributeCannotReturn}
)

// tributeErr 造一个带原因码的规则错误。句子还是自由写的，牌例不比对它。
func tributeErr(sentinel *TributeError, format string, args ...any) error {
	return &TributeError{Reason: sentinel.Reason, Message: fmt.Sprintf(format, args...)}
}

// 进贡、还贡、抗贡（#204 第 6 步），以及接 scoring 算下一副打几。
//
// 口径（2022《竞技掼蛋竞赛规则（试行）》第十条）：
//   - 二、单贡：从第二副牌开始、出牌前，上副牌的下游把全手牌里最大的一张（红心级牌除外）贡给
//     上游；上游选一张牌点不超过 10 的牌还给下游，全手牌都大于 10 时还最小的。首圈由下游领出。
//   - 三、双贡：上副牌双下时，双下方两人都向对方进贡。上游拿牌点大的那张，搭档拿小的，各自还给
//     贡给自己的人。两张一样大时按顺时针方向进贡。还牌后由贡给上游的那位领出首圈。
//   - 四、抗贡：下游（双下时是双下方两人合起来）在进贡前抓到两个大王，就抗贡，不再贡牌，
//     首圈由上游领出。
//
// 三处条文有歧义的地方，这里的读法（#204 第 6 步，已报 coord）：
//   - 「牌点不超过 10」按**牌面数字**算：2–10 都能还，打 7 时的 ♠7、♥7 牌面是 7 也能还。
//     「全都大于 10 时还最小的」按当前级数下的大小（第四条 一、1）。
//   - 「按顺时针方向进贡」：两张贡牌一样大时，每个进贡的人贡给自己顺时针方向的那一家。座位
//     1 → 2 → 3 → 4 是逆时针，所以 s 号座顺时针的邻座是 s−1（1 的是 4）；双下方两家的顺时针
//     邻座正好是对方两家，不会撞。
//   - 「最大的一张」按当前级数下的大小；有几张一样大（两张 ♠A、不同花色的级牌）时任选一张都对。
//
// 引擎不替人挑牌：要贡哪张、还哪张由玩家给，这里只校验合不合规（和出牌一样）。

// TributeKind 是这一副开打前的贡牌情形。
type TributeKind int

const (
	// TributeSingle：单贡（上副牌没有双下）。
	TributeSingle TributeKind = iota + 1
	// TributeDouble：双贡（上副牌双下）。
	TributeDouble
	// TributeResisted：抗贡，不贡牌，上游领出。
	TributeResisted
)

func (k TributeKind) String() string {
	switch k {
	case TributeSingle:
		return "single"
	case TributeDouble:
		return "double"
	case TributeResisted:
		return "resisted"
	}
	return fmt.Sprintf("TributeKind(%d)", int(k))
}

// ClockwiseSeat 是顺时针的下一家（NextSeat 的反方向）。
func ClockwiseSeat(seat int) int { return (seat+2)%Seats + 1 }

// Tribute 是一副牌开打前的贡牌、还牌过程。零值不能用，用 NewTribute 建。
type Tribute struct {
	level  Rank
	hands  [Seats + 1][]Card
	kind   TributeKind
	head   int   // 上副牌的上游
	givers []int // 要进贡的人（单贡 1 个、双贡 2 个），按座位号排

	gave     map[int]Card // 进贡的人 → 贡出的牌
	to       map[int]int  // 进贡的人 → 贡给谁（所有人都贡完之后才定得下来）
	returned map[int]Card // 收贡的人 → 还出的牌
	leader   int          // 首圈领出的人（定下来之后）
}

// NewTribute 按上副牌的结果开始贡牌：hands[i] 是这一副发到 i+1 号座的手牌，prev 是上副牌的结果。
//
// 每一场（轮）的第一副牌不进贡（第十条 二「从第二副牌开始」），调用方不要为它建 Tribute ——
// 要不要进贡看 Next(...).Tribute（本场打过至少一副、且本场还没结束）。抗贡在这里直接判定，
// 返回的 Tribute 已经结束。
func NewTribute(level Rank, hands [Seats][]Card, prev Result) (*Tribute, error) {
	if level < Two || level > Ace {
		return nil, fmt.Errorf("贡牌：级数 %d 超出 2–A", level)
	}
	if err := checkPrevResult(prev); err != nil {
		return nil, fmt.Errorf("贡牌：上副牌的结果不对：%w", err)
	}
	t := &Tribute{
		level: level, head: prev.HeadSeat,
		gave: map[int]Card{}, to: map[int]int{}, returned: map[int]Card{},
	}
	for i, h := range hands {
		t.hands[i+1] = append([]Card{}, h...)
	}

	if prev.PartnerPlace == 2 {
		// 双下：对方两家都贡（第十条 三）。
		a, b := NextSeat(prev.HeadSeat), PartnerSeat(NextSeat(prev.HeadSeat))
		if a > b {
			a, b = b, a
		}
		t.kind, t.givers = TributeDouble, []int{a, b}
	} else {
		// 单贡：下游贡（第十条 二）。
		t.kind, t.givers = TributeSingle, []int{prev.Order[Seats-1]}
	}

	// 抗贡：要进贡的一方（双下时两人合起来）抓到两个大王（第十条 四）。
	bigJokers := 0
	for _, g := range t.givers {
		for _, c := range t.hands[g] {
			if c.Rank == BigJoker {
				bigJokers++
			}
		}
	}
	if bigJokers >= 2 {
		t.kind, t.leader = TributeResisted, prev.HeadSeat
	}
	return t, nil
}

// checkPrevResult 查上副牌的结果自洽：名次是 1–4 的一个排列、头游就是第一个出完的、
// 搭档的名次和 PartnerPlace 对得上。
//
// 为什么查这么细：贡牌完全按这三样推算「谁该贡、贡给谁、谁领出」（下游的座位、双下是哪两家、
// 上游是谁），结果不自洽的话轻则算出「上游贡给自己」，重则下标越界 panic。
// auditor-249 的三个探针：Order=[1 2 3 7]、Order=[1 2 3 0]、Order=[2 3 4 1] 配 HeadSeat=1。
//
// 正常来源（Game.Result()）天然满足这三条：名次就是出完的先后接上没出完的、头游取第一个、
// 搭档名次是扫出来的。
func checkPrevResult(prev Result) error {
	if len(prev.Order) != Seats {
		return fmt.Errorf("名次该有 %d 家，得到 %v", Seats, prev.Order)
	}
	seen := map[int]bool{}
	for _, s := range prev.Order {
		if !validSeat(s) {
			return fmt.Errorf("名次里出现了 %d，座位号该是 1–%d：%v", s, Seats, prev.Order)
		}
		if seen[s] {
			return fmt.Errorf("名次里 %d 出现了两次：%v", s, prev.Order)
		}
		seen[s] = true
	}
	if !validSeat(prev.HeadSeat) || prev.Order[0] != prev.HeadSeat {
		return fmt.Errorf("头游该是第一个出完的：HeadSeat=%d，名次 %v", prev.HeadSeat, prev.Order)
	}
	if prev.PartnerPlace < 2 || prev.PartnerPlace > Seats {
		return fmt.Errorf("搭档的名次该在 2–%d 之间，得到 %d", Seats, prev.PartnerPlace)
	}
	if got := prev.Order[prev.PartnerPlace-1]; got != PartnerSeat(prev.HeadSeat) {
		return fmt.Errorf("搭档的名次对不上：%d 号座的搭档是 %d 号座，可名次表第 %d 名是 %d 号座",
			prev.HeadSeat, PartnerSeat(prev.HeadSeat), prev.PartnerPlace, got)
	}
	return nil
}

// Kind 是贡牌的情形。
func (t *Tribute) Kind() TributeKind { return t.kind }

// Givers 是要进贡的人（抗贡时也照样给出，界面要显示是谁抗的贡）。
func (t *Tribute) Givers() []int { return append([]int{}, t.givers...) }

// Receiver 是 giver 贡给谁；还没定下来（双贡要等两人都贡完）时 ok 为 false。
func (t *Tribute) Receiver(giver int) (seat int, ok bool) {
	seat, ok = t.to[giver]
	return
}

// Done 判断贡牌、还牌是不是都完了（抗贡一开始就完了）。
func (t *Tribute) Done() bool {
	return t.kind == TributeResisted || len(t.returned) == len(t.givers)
}

// Leader 是首圈领出的人；还没完时 ok 为 false。
func (t *Tribute) Leader() (seat int, ok bool) {
	if !t.Done() {
		return 0, false
	}
	return t.leader, true
}

// Hands 是贡完、还完之后各家的手牌（下标是座位号减 1），可以直接交给 NewGame。还没完时报错。
func (t *Tribute) Hands() ([Seats][]Card, error) {
	var out [Seats][]Card
	if !t.Done() {
		return out, fmt.Errorf("贡牌还没完")
	}
	for s := 1; s <= Seats; s++ {
		out[s-1] = append([]Card{}, t.hands[s]...)
	}
	return out, nil
}

// Start 是贡完之后开打：用贡牌后的手牌和定下来的领出人开一副 Game。
func (t *Tribute) Start() (*Game, error) {
	hands, err := t.Hands()
	if err != nil {
		return nil, err
	}
	leader, _ := t.Leader()
	return NewGame(t.level, hands, leader)
}

func (t *Tribute) isGiver(seat int) bool {
	for _, g := range t.givers {
		if g == seat {
			return true
		}
	}
	return false
}

func holds(hand []Card, c Card) bool {
	for _, h := range hand {
		if h.ID == c.ID {
			return true
		}
	}
	return false
}

func without(hand []Card, c Card) []Card {
	out := make([]Card, 0, len(hand))
	for _, h := range hand {
		if h.ID != c.ID {
			out = append(out, h)
		}
	}
	return out
}

// Give 进贡：seat 把 card 贡出去。card 必须是他手里最大的一张（红心级牌除外）。
func (t *Tribute) Give(seat int, card Card) error {
	if t.kind == TributeResisted {
		return tributeErr(ErrTributeResisted, "进贡：这一副抗贡了，不贡牌（第十条 四）")
	}
	if !t.isGiver(seat) {
		return tributeErr(ErrTributeNotAGiver, "进贡：%d 号座不用进贡（要进贡的是 %v）", seat, t.givers)
	}
	if _, ok := t.gave[seat]; ok {
		return tributeErr(ErrTributeAlreadyGave, "进贡：%d 号座已经贡过了", seat)
	}
	hand := t.hands[seat]
	if !holds(hand, card) {
		return tributeErr(ErrTributeCardNotInHand, "进贡：%d 号座手里没有 %s", seat, card.Key())
	}
	if card.IsWild(t.level) {
		return tributeErr(ErrTributeWildCannotGive, "进贡：红心级牌 %s 不用贡（第十条 二）", card.Key())
	}
	max := -1
	for _, c := range hand {
		if !c.IsWild(t.level) && c.Power(t.level) > max {
			max = c.Power(t.level)
		}
	}
	if card.Power(t.level) != max {
		return tributeErr(ErrTributeNotBiggest, "进贡：要贡手里最大的一张（红心级牌除外），%s 不是（第十条 二）", card.Key())
	}

	t.hands[seat] = without(hand, card)
	t.gave[seat] = card
	if len(t.gave) == len(t.givers) {
		t.assign()
	}
	return nil
}

// assign 在所有人都贡完之后定下「谁贡给谁」，把贡牌放进收贡的人手里，并定下首圈领出的人。
func (t *Tribute) assign() {
	if t.kind == TributeSingle {
		g := t.givers[0]
		t.to[g] = t.head
		t.leader = g // 首圈由下游领出（第十条 二）
	} else {
		a, b := t.givers[0], t.givers[1]
		pa, pb := t.gave[a].Power(t.level), t.gave[b].Power(t.level)
		switch {
		case pa > pb:
			t.to[a], t.to[b] = t.head, PartnerSeat(t.head) // 大的给上游，小的给搭档（第十条 三）
		case pa < pb:
			t.to[b], t.to[a] = t.head, PartnerSeat(t.head)
		default:
			t.to[a], t.to[b] = ClockwiseSeat(a), ClockwiseSeat(b) // 一样大：按顺时针方向进贡
		}
		for g, r := range t.to {
			if r == t.head {
				t.leader = g // 还牌后由贡给上游的那位领出首圈（第十条 三）
			}
		}
	}
	for g, r := range t.to {
		t.hands[r] = append(t.hands[r], t.gave[g])
	}
}

// canReturn 判断 card 能不能当还牌：手里有牌面不超过 10 的牌时只能还这种；
// 全都大于 10 时只能还最小的（第十条 二）。
func (t *Tribute) canReturn(hand []Card, card Card) bool {
	small := func(c Card) bool { return !c.Rank.IsJoker() && c.Rank <= Ten }
	for _, c := range hand {
		if small(c) {
			return small(card)
		}
	}
	min := -1
	for _, c := range hand {
		if p := c.Power(t.level); min < 0 || p < min {
			min = p
		}
	}
	return card.Power(t.level) == min
}

// Return 还牌：收了贡的 seat 把 card 还给贡给他的人。
func (t *Tribute) Return(seat int, card Card) error {
	if t.kind == TributeResisted {
		return tributeErr(ErrTributeResisted, "还牌：这一副抗贡了，不还牌（第十条 四）")
	}
	if len(t.gave) < len(t.givers) {
		return tributeErr(ErrTributeNotAllGiven, "还牌：还有人没贡完")
	}
	giver := 0
	for g, r := range t.to {
		if r == seat {
			giver = g
		}
	}
	if giver == 0 {
		return tributeErr(ErrTributeNotAReceiver, "还牌：%d 号座没收贡，不用还", seat)
	}
	if _, ok := t.returned[seat]; ok {
		return tributeErr(ErrTributeAlreadyReturned, "还牌：%d 号座已经还过了", seat)
	}
	hand := t.hands[seat]
	if !holds(hand, card) {
		return tributeErr(ErrTributeCardNotInHand, "还牌：%d 号座手里没有 %s", seat, card.Key())
	}
	if !t.canReturn(hand, card) {
		return tributeErr(ErrTributeCannotReturn, "还牌：%s 不能还 —— 有牌面不超过 10 的牌时还那种，全都大于 10 时还最小的（第十条 二）", card.Key())
	}
	t.hands[seat] = without(hand, card)
	t.hands[giver] = append(t.hands[giver], card)
	t.returned[seat] = card
	return nil
}

// ---------------------------------------------------------------------------
// 接 scoring：升级、过 A、下一副打几（#204：「升级 / 过 A 复用 scoring」）
// ---------------------------------------------------------------------------

// ScoringRound 把一副的结果换成 scoring 用的一局事实（头游座位 + 搭档名次，口径相同）。
func (r Result) ScoringRound() scoring.Round {
	return scoring.Round{HeadSeat: r.HeadSeat, PartnerPlace: r.PartnerPlace}
}

// NextHand 是「接下来这一副」要知道的：打几、要不要进贡。
type NextHand struct {
	Level Rank
	// Tribute：要进贡（这一场已经打过至少一副；新一场的第一副不贡，第十条 二）。
	Tribute bool
	// Score 是 scoring 算出来的完整状态（两队级数、过 A、本场是否结束等），界面直接用。
	Score scoring.State
}

// Next 按本场到现在为止每一副的结果，算下一副打几、要不要进贡。升级、到 A 封顶、过 A、
// 打谁的级全由 scoring 算（第二条 12–14、第十二条），这里不另写一份。
//
// 玩法现在写死经典（scoring.KindClassic）；以后接不升级之类的玩法时，把 scoring.Settings
// 当参数传进来就行 —— 这里只透传，没有别的判断。
//
// results 是从第一副起的全部结果（跨场接着排，scoring 自己会在过 A 之后开新一场）。
func Next(results []Result) (NextHand, error) {
	rounds := make([]scoring.Round, len(results))
	for i, r := range results {
		rounds[i] = r.ScoringRound()
	}
	res, err := scoring.Compute(scoring.Settings{Kind: scoring.KindClassic}, rounds)
	if err != nil {
		return NextHand{}, fmt.Errorf("算升级：%w", err)
	}
	st := res.State
	return NextHand{
		Level:   Rank(st.PlayingLevel),
		Tribute: st.MatchRounds > 0 && !st.MatchOver,
		Score:   st,
	}, nil
}
