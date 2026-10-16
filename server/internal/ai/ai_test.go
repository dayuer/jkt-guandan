package ai

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/rules"
)

// 第一版 AI 的验收（#88 第 2 件）。两条主线：
//
//  1. **硬性的**：AI 出的每一手牌都必须被引擎接受 —— 用「四个座位全由 AI 打」跑几百副来压，
//     顺带把「不主动拆炸弹」「不用炸弹压队友」这两条在整局里统计出来（必须都是 0 次）。
//  2. **点名的局面**：队友领先该让牌、对手快走完该压、能一次走完该走完 —— 期望值**手写**
//     （按设计里那几条策略推的），不是拿 AI 自己的输出当期望。
//
// 天生盲区：AI 的出法全部来自 `rules.Moves`，所以 **`Moves` 漏了牌型这里看不出来** ——
// 那种漏法由 rules 包里的暴力枚举对拍守着。

// ───────────────────────────────────────────────────────────────────────────
// 驱动器
// ───────────────────────────────────────────────────────────────────────────

// aiStats 是整局统计出来的、几条性质要看的量。
type aiStats struct {
	steps         int
	illegal       int // 被引擎拒的次数（必须 0）
	brokeBomb     int // 主动拆炸弹的次数（必须 0）
	bombedPartner int // 用炸弹压队友的次数（必须 0）
}

// viewOf 从引擎里**只取牌桌上人人看得见的**信息拼出 View：自己那手牌 + 各家的张数。
//
// ⚠️ 这里刻意只读别家手牌的**长度**（张数是公开的），不把别家的牌放进 View —— View 里根本没有
// 那个字段（`view_test.go` 用反射钉着）。
func viewOf(g *rules.Game, seat int) View {
	v := View{
		Level:   g.Level(),
		Seat:    seat,
		Hand:    g.Hand(seat),
		Done:    g.Finished(),
		Leading: g.Leading(),
	}
	for s := 1; s <= rules.Seats; s++ {
		v.Counts[s] = len(g.Hand(s))
	}
	if lastSeat, combo, ok := g.LastPlay(); ok {
		v.LastSeat, v.LastCombo = lastSeat, combo
	}
	return v
}

// playWithAI 把一副牌打完，四个座位都交给 AI。
//
// 用四个 AI（而不是「一个真人 + 三个 AI」）是因为这样能走到更多局面 —— 练习局里只有一个真人，
// 但引擎那一层没有必要把另外三家固定成别的什么。
func playWithAI(rng *rand.Rand, level rules.Rank, hands [rules.Seats][]rules.Card, leader int, cfg Config) (*rules.Game, aiStats, error) {
	g, err := rules.NewGame(level, hands, leader)
	if err != nil {
		return nil, aiStats{}, err
	}
	var st aiStats
	const maxSteps = 4 * rules.Seats * 108 // 和 rules 的 sim 同一个上界口径
	for !g.Over() {
		if st.steps++; st.steps > maxSteps {
			return g, st, errors.New("这一副打不完（步数超上限）—— 引擎卡住了")
		}
		seat := g.Turn()
		v := viewOf(g, seat)
		d, err := Decide(v, cfg)
		if err != nil {
			return g, st, err
		}
		if d.Pass {
			if err := g.Pass(seat); err != nil {
				st.illegal++
				return g, st, fmt.Errorf("AI 过牌被拒：%w", err)
			}
			continue
		}
		// 先统计（统计的是 AI **打算**这么打），再让引擎判 —— 被拒了下面立刻返回。
		//
		// 「不主动拆炸弹」的口径是**不主动**：手里 4 张同点数、而候选里还有别的选择时，
		// 拆了才算数。跟牌时只有拆炸弹才压得住不算数 —— 那是「不得不」，不是「主动」。
		if splitsBombIntoSmall(v.Hand, level, d.Combo) && hasNonSplitting(v, level) {
			st.brokeBomb++
		}
		if isBomb(d.Combo) && v.LastFromPartner() {
			st.bombedPartner++
		}
		if err := g.Play(seat, d.Combo.Cards, d.Combo.Type, d.Combo.Rank); err != nil {
			st.illegal++
			return g, st, fmt.Errorf("AI 出的牌被拒（%s）：%w", d.Combo, err)
		}
	}
	return g, st, nil
}

// dealRandom 发一副牌，并把「首抓者起逆时针第几家」旋转成座位号。
func dealRandom(rng *rand.Rand, leader int) [rules.Seats][]rules.Card {
	perm := rng.Perm(len(rules.NewDeck()))
	hands, err := rules.Deal(rules.PermShuffler(perm))
	if err != nil {
		panic(err) // 排列是自己生成的，不可能不合法
	}
	var out [rules.Seats][]rules.Card
	for k, h := range hands {
		out[(leader-1+k)%rules.Seats] = h
	}
	return out
}

// hasNonSplitting 判断这个局面下「还有不拆成对子/单张的选择」。
func hasNonSplitting(v View, level rules.Rank) bool {
	cands, err := rules.Moves(v.Hand, level, leadOf(v))
	if err != nil {
		return false
	}
	return len(notSplittingBombs(v, cands)) > 0
}

// ───────────────────────────────────────────────────────────────────────────
// 1. 整局：出的牌永远合法 + 不主动拆炸弹 + 不炸队友
// ───────────────────────────────────────────────────────────────────────────

func TestAIPlaysWholeGamesLegally(t *testing.T) {
	rng := rand.New(rand.NewSource(20261016))
	cfg := DefaultConfig()
	var steps, brokeBomb, bombedPartner int
	const deals = 300

	for i := 0; i < deals; i++ {
		level := rules.Rank(2 + rng.Intn(13)) // 2–A
		leader := 1 + rng.Intn(rules.Seats)
		hands := dealRandom(rng, leader)
		_, st, err := playWithAI(rng, level, hands, leader, cfg)
		if err != nil {
			t.Fatalf("第 %d 副（打 %d，%d 号座领出）：%v", i, level, leader, err)
		}
		steps += st.steps
		brokeBomb += st.brokeBomb
		bombedPartner += st.bombedPartner
	}

	// 覆盖面：步数太少说明没跑够（「跑是跑了、什么局面都没走到」是这种测试最常见的空转）。
	if steps < 10_000 {
		t.Fatalf("一共才走了 %d 步 —— 语料不够，压不出什么", steps)
	}
	if brokeBomb != 0 {
		t.Fatalf("AI 主动拆炸弹 %d 次（策略说不拆）", brokeBomb)
	}
	if bombedPartner != 0 {
		t.Fatalf("AI 用炸弹压队友 %d 次（策略说绝不用炸弹压队友）", bombedPartner)
	}
	t.Logf("%d 副、%d 步；没拆过炸弹、没炸过队友", deals, steps)
}

// ───────────────────────────────────────────────────────────────────────────
// 2. 点名的局面：期望值手写（按设计里那几条策略推）
// ───────────────────────────────────────────────────────────────────────────

func mustCards(t *testing.T, keys string) []rules.Card {
	t.Helper()
	fields := strings.Fields(keys)
	out := make([]rules.Card, len(fields))
	for i, k := range fields {
		c, err := rules.ParseKey(k)
		if err != nil {
			t.Fatalf("牌 %q：%v", k, err)
		}
		out[i] = c
	}
	return out
}

// comboOf 认出一手牌当成 `kind` 打的那一种解读。
func comboOf(t *testing.T, level rules.Rank, kind rules.ComboType, keys string) rules.Combo {
	t.Helper()
	cards := mustCards(t, keys)
	combos, err := rules.Recognize(cards, level)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := rules.Pick(combos, kind, 0)
	if !ok {
		t.Fatalf("%q 当不成 %s（能当成：%s）", keys, kind, rules.ComboSummary(combos))
	}
	return c
}

// 队友刚出一手（3 号座是我的对家），我手里压得住 —— 策略说**让牌**。
func TestAIYieldsToPartner(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		// 手里**压得住**（一对 K）—— 不然「过牌」试不出来是让牌还是要不起（那是另一回事）。
		Hand:      mustCards(t, "♠K/0 ♥K/0"),
		Counts:    [rules.Seats + 1]int{0, 2, 20, 20, 20},
		Leading:   false,
		LastSeat:  3, // 对家
		LastCombo: comboOf(t, level, rules.TypePair, "♠10/0 ♥10/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Pass {
		t.Fatalf("队友领先时该让牌，它却出了 %s", d.Combo)
	}
}

// 同一个局面，但对手只剩 1 张了 —— 策略说**不让，能压就压**。
func TestAIPressesWhenOpponentAboutToWin(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠K/0 ♥K/0"),             // 手里压得住（一对 K）
		Counts:    [rules.Seats + 1]int{0, 2, 1, 20, 20}, // 2 号座（对手）只剩 1 张
		Leading:   false,
		LastSeat:  3, // 还是队友出的
		LastCombo: comboOf(t, level, rules.TypePair, "♠10/0 ♥10/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass {
		t.Fatal("对手只剩一张了，队友这一手该让位给「压住」")
	}
	if d.Combo.Type != rules.TypePair {
		t.Fatalf("该压的是一对 K，它出了 %s", d.Combo)
	}
}

// 手里能一次走完 —— 策略说**一定走完**（哪怕别的手也行）。
func TestAIFinishesInOneGo(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		Hand:    mustCards(t, "♠9/0 ♥9/0"),
		Counts:  [rules.Seats + 1]int{0, 2, 20, 20, 20},
		Leading: true,
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass {
		t.Fatal("领出且手里正好一对，该一次走完")
	}
	if d.Combo.Type != rules.TypePair || len(d.Combo.Cards) != 2 {
		t.Fatalf("该出一对 9 走完，它出了 %s", d.Combo)
	}
}

// 只剩炸弹能压、而且还不到该炸的时候 —— 策略说**过牌**（留着炸弹）。
func TestAIHoldsBombWhenNotNeeded(t *testing.T) {
	level := rules.Seven
	// 手里 4 张 10 是一个完整的炸弹；对家出了一对 9，只有拆炸弹才压得住。
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠10/0 ♥10/0 ♣10/0 ♦10/0 ♠3/0 ♠4/0 ♠5/0 ♠6/0 ♠7/0"),
		Counts:    [rules.Seats + 1]int{0, 9, 20, 20, 20},
		Leading:   false,
		LastSeat:  3,
		LastCombo: comboOf(t, level, rules.TypePair, "♠9/0 ♥9/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Pass {
		t.Fatalf("还不到该炸的时候（手里还多、对手也没快走完），该过牌，它却出了 %s", d.Combo)
	}
}

// 同一个局面，但对手只剩 2 张 —— 策略说该炸就炸。
func TestAIBombsWhenOpponentAboutToWin(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠10/0 ♥10/0 ♣10/0 ♦10/0 ♠3/0 ♠4/0 ♠5/0 ♠6/0 ♠7/0"),
		Counts:    [rules.Seats + 1]int{0, 9, 20, 20, 2}, // 4 号座（对手）只剩 2 张（3 号座是对家！）
		Leading:   false,
		LastSeat:  4, // 这一手是**对手**出的（3 号座才是对家 —— 对家出的那一手绝不用炸弹压）
		LastCombo: comboOf(t, level, rules.TypePair, "♠9/0 ♥9/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass {
		t.Fatal("对手快走完了，该用炸弹压住")
	}
	if !isBomb(d.Combo) {
		t.Fatalf("该出炸弹，它出了 %s", d.Combo)
	}
}

// 纯函数：同一个局面给同样的答案。
func TestAIDecideIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	level := rules.Rank(2 + rng.Intn(13))
	hands := dealRandom(rng, 1)
	g, err := rules.NewGame(level, hands, 1)
	if err != nil {
		t.Fatal(err)
	}
	v := viewOf(g, g.Turn())
	cfg := DefaultConfig()
	first, err := Decide(v, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Decide(v, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(again, first) {
			t.Fatalf("同一个局面给出了不同的答案：%+v vs %+v", first, again)
		}
	}
}
