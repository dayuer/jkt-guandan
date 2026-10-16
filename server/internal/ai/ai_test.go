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
		if splitsBombIndependently(v.Hand, level, d.Combo) && hasOtherChoice(v, level) {
			st.brokeBomb++
		}
		// 见 bombsPartner：排除「这一手正好走完」那种（口径：能走完 > 不炸队友，头游最重要）。
		if bombsPartner(v, d) {
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

// splitsBombIndependently 是「这一手是不是把炸弹拆成对子/单张」的**独立**判法（整局统计用）。
//
// 不调生产代码里的 `splitsBombIntoSmall` 是有意的：统计要是和实现共用同一个判断，
// 判断被改坏时两边一起变，谁都发现不了（#87 那边 `refClockwise` / `refTeam` 是同一条道理）。
func splitsBombIndependently(hand []rules.Card, level rules.Rank, c rules.Combo) bool {
	if c.Type != rules.TypePair && c.Type != rules.TypeSingle {
		return false
	}
	// 手里、这一手各自用了几张同点数的牌（逢人配不算：它凑出来的不算手里原有的炸弹）。
	// 这里**按这一手用到的点数去查手里有几张**，和那边「遍历手里的点数」是两条写法。
	count := func(cards []rules.Card) map[rules.Rank]int {
		m := make(map[rules.Rank]int)
		for _, card := range cards {
			if card.IsWild(level) {
				continue
			}
			m[card.Rank]++
		}
		return m
	}
	have := count(hand)
	for rank, used := range count(c.Cards) {
		left := have[rank] - used
		if have[rank] >= 4 && left > 0 && left < 4 {
			return true
		}
	}
	return false
}

// bombsPartner 判断这一手算不算整局统计里的「用炸弹压队友」。
//
// 三个条件：队友出的那一手、我出的也是炸弹、**而且不是「这一手正好走完」**。
// 最后那条是口径（#259 复审定）：能一次走完 > 绝不炸队友（头游最重要）——
// 手里正好一手走光时出炸弹不算「压队友」，见 ai.go 包里那段说明。
//
// 单拎出来是因为整局语料**碰不到**这一种组合（要正好「队友出的那一手 + 我这一手走光 + 是炸弹」），
// 混在驱动器里就只能靠运气；单拎出来两个分支都能点着测（下面 TestBombsPartner...）。
func bombsPartner(v View, d Decision) bool {
	return isBomb(d.Combo) && v.LastFromPartner() && len(d.Combo.Cards) < len(v.Hand)
}

// hasOtherChoice 判断这个局面下还有「不拆炸弹」的候选。
func hasOtherChoice(v View, level rules.Rank) bool {
	cands, err := rules.Moves(v.Hand, level, leadOf(v))
	if err != nil {
		return false
	}
	for _, c := range cands {
		if !splitsBombIndependently(v.Hand, level, c) {
			return true
		}
	}
	return false
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

// 让牌阈值（Config.YieldMinPower，默认 10）的第一个点：队友出一对 **10**，牌力**正好到阈值** —— 让。
//
// 「正好到」是有意的：`>=` 改成 `>`、或者默认值改成 11，这条就红（#259 复审 B2）。
func TestAIYieldsToPartner(t *testing.T) {
	level := rules.Seven
	// 手里压得住（一对 A），但**走不光**（3 张）—— 不然「能一次走完」会先接走这一手。
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠A/0 ♥A/0 ♠3/0"),
		Counts:    [rules.Seats + 1]int{0, 3, 20, 20, 20},
		Leading:   false,
		LastSeat:  3, // 对家
		LastCombo: comboOf(t, level, rules.TypePair, "♠10/0 ♥10/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Pass {
		t.Fatalf("队友出的是一对 10（牌力正好到阈值 10），该让牌，它却出了 %s", d.Combo)
	}
}

// 阈值的第三个点：**按当前级数算大小**，不是按原始点数 —— 打 3 时一对 3 是级牌（牌力 15）—— 让。
//
// 这一条钉的是「按级数算」：改成按原始点数（3 < 10）它就红。
func TestAIYieldsWhenPartnerPlaysTheLevelPair(t *testing.T) {
	level := rules.Three
	v := View{
		Level: level, Seat: 1,
		// 一对小王压得住任何对子（级牌对也压得住），而且**走不光**（3 张）。
		Hand:      mustCards(t, "小王/0 小王/1 ♠A/0"),
		Counts:    [rules.Seats + 1]int{0, 3, 20, 20, 20},
		Leading:   false,
		LastSeat:  3, // 对家
		LastCombo: comboOf(t, level, rules.TypePair, "♠3/0 ♦3/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Pass {
		t.Fatalf("打 3 时一对 3 是级牌（牌力 15，不小），该让牌，它却出了 %s", d.Combo)
	}
}

// 阈值的第二个点：队友出一对 **9**（牌力 9，差一点）—— 策略说**不让**（阈值默认 10 的下侧）。
//
// 口径是审计 #259 定的：队友出一手小牌，对手随手就压过去了，让牌等于把这一圈白送，
// 不如自己接过来。（阈值在 Config.YieldMinPower，默认 10。）
func TestAIDoesNotYieldToSmallPartnerPlay(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠A/0 ♥A/0 ♠3/0"),
		Counts:    [rules.Seats + 1]int{0, 3, 20, 20, 20},
		Leading:   false,
		LastSeat:  3, // 对家出的
		LastCombo: comboOf(t, level, rules.TypePair, "♠9/0 ♥9/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass {
		t.Fatal("队友出的是一手小牌（对手随手就能压过去），该自己接过来，它却让了")
	}
	if d.Combo.Type != rules.TypePair {
		t.Fatalf("该用一对 A 接过来，它出了 %s", d.Combo)
	}
}

// 口径（#259 复审定）：**能一次走完 > 绝不炸队友** —— 队友出一对 5，我手里正好 4 张 K，
// 该出炸弹走完（头游最重要），这不是「用炸弹压队友」那种被禁的用法。
func TestAIFinishesOverPartnerBombRule(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠K/0 ♥K/0 ♣K/0 ♦K/0"), // 正好一手走光，而且是炸弹
		Counts:    [rules.Seats + 1]int{0, 4, 20, 20, 20},
		Leading:   false,
		LastSeat:  3, // 队友出的
		LastCombo: comboOf(t, level, rules.TypePair, "♠5/0 ♥5/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass {
		t.Fatal("手里 4 张 K 能一次走完，走完优先于「绝不用炸弹压队友」，它却让了")
	}
	if !isBomb(d.Combo) || len(d.Combo.Cards) != 4 {
		t.Fatalf("该出炸弹走完，它出了 %s", d.Combo)
	}
}

// 队友领先、那一手也不小，但**自己这一手能走完** —— 走完优先（口径由 #259 复审定）。
func TestAIFinishesEvenWhenPartnerLeads(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠A/0 ♥A/0"), // 一对 A，正好走光
		Counts:    [rules.Seats + 1]int{0, 2, 20, 20, 20},
		Leading:   false,
		LastSeat:  3,
		LastCombo: comboOf(t, level, rules.TypePair, "♠K/0 ♥K/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass {
		t.Fatal("手里一对 A 能一次走完，走完优先于让牌，它却让了")
	}
	if d.Combo.Type != rules.TypePair || len(d.Combo.Cards) != 2 {
		t.Fatalf("该出一对 A 走完，它出了 %s", d.Combo)
	}
}

// 同一个局面，但对手只剩 1 张了 —— 策略说**不让，能压就压**。
func TestAIPressesWhenOpponentAboutToWin(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		// 手里压得住（一对 K）、而且走不光（3 张）—— 不然「走完优先」会先接走这一手。
		Hand:      mustCards(t, "♠K/0 ♥K/0 ♠3/0"),
		Counts:    [rules.Seats + 1]int{0, 3, 1, 20, 20}, // 2 号座（对手）只剩 1 张
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

// 手里能一次走完 —— 策略说**一定走完**（哪怕别的手也行，哪怕那一手是炸弹）。
//
// 后三条是审计 #259 给的反例：它们以前会被「炸弹扣分」顶掉 —— 4 张 K 出三张 K、
// 6 张 K 出三张 K、四大天王出一对小王。「走完那一手张数最多，② 就把它挑出来了」
// 这句话**只在不扣分时成立**，所以「一次走完」必须单独做成一条规则。
func TestAIFinishesInOneGo(t *testing.T) {
	level := rules.Seven
	for _, tc := range []struct {
		name string
		keys string
		bomb bool
	}{
		{"一对 9", "♠9/0 ♥9/0", false},
		{"4 张 K（炸弹）", "♠K/0 ♥K/0 ♣K/0 ♦K/0", true},
		{"6 张 K（两副牌，炸弹）", "♠K/0 ♥K/0 ♣K/0 ♦K/0 ♠K/1 ♥K/1", true},
		{"四大天王（两张大王两张小王）", "大王/0 大王/1 小王/0 小王/1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hand := mustCards(t, tc.keys)
			v := View{
				Level: level, Seat: 1,
				Hand:    hand,
				Counts:  [rules.Seats + 1]int{0, len(hand), 20, 20, 20},
				Leading: true,
			}
			d, err := Decide(v, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			if d.Pass {
				t.Fatalf("领出且手里正好一手走光，该一次走完，它却过了")
			}
			if len(d.Combo.Cards) != len(hand) {
				t.Fatalf("该把 %d 张一次走光，它出了 %s（%d 张）", len(hand), d.Combo, len(d.Combo.Cards))
			}
			if isBomb(d.Combo) != tc.bomb {
				t.Fatalf("这一手走光的牌是%s炸弹，判定不对：%s", map[bool]string{true: "", false: "不"}[tc.bomb], d.Combo)
			}
		})
	}
}

// 只剩炸弹能压、而且还不到该炸的时候 —— 策略说**过牌**（留着炸弹）。
//
// ⚠️ 上一手必须是**对手**出的（1 号座的对家是 3 号座）：这条原来写的是 3 号座，
// 于是走的是「让牌」那条路 —— 过牌是让出来的，根本没走到「平时不炸」（审计 #259 第 2 条：
// 把「不到时候」那段改成 `if false` 它照样绿）。
// 现在用两个参数把这条分支**夹**住：同一个局面，只把 `BombWhenOwnLeft` 放宽，答案就必须反过来。
func TestAIHoldsBombWhenNotNeeded(t *testing.T) {
	level := rules.Seven
	// 手里 4 张 10 是一个完整的炸弹，一共 9 张；上一手是对手出的一对 9 —— 只有炸弹压得住。
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠10/0 ♥10/0 ♣10/0 ♦10/0 ♠3/0 ♠4/0 ♠5/0 ♠6/0 ♠7/0"),
		Counts:    [rules.Seats + 1]int{0, 9, 20, 20, 20}, // 没人快走完
		Leading:   false,
		LastSeat:  4, // 这一手是**对手**出的（3 号座才是对家）
		LastCombo: comboOf(t, level, rules.TypePair, "♠9/0 ♥9/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Pass {
		t.Fatalf("还不到该炸的时候（自己还剩 9 张 > BombWhenOwnLeft=6，对手也没快走完），该过牌，它却出了 %s", d.Combo)
	}
	// 反过来夹：同一个局面，只把「该炸的时候」放宽到自己剩 9 张以内 —— 就该出炸弹了。
	loose := DefaultConfig()
	loose.BombWhenOwnLeft = 9
	d2, err := Decide(v, loose)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Pass || !isBomb(d2.Combo) {
		t.Fatalf("BombWhenOwnLeft 放宽到 9（自己还剩 9 张）之后就该炸，它却给了 %+v", d2)
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
		LastSeat:  4, // 这一手是**对手**出的（对家出的那一手绝不用炸弹压）
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

// 领出④「牌不多了先出小的」：按**当前级数下的大小**算 —— 打 7 时 7 是级牌（比 K 大），所以先出 K。
func TestAILeadsSmallWhenFewCards(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		Hand:    mustCards(t, "♠K/0 ♠7/0"),
		Counts:  [rules.Seats + 1]int{0, 2, 20, 20, 20},
		Leading: true,
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass || d.Combo.Type != rules.TypeSingle || d.Combo.Cards[0].Key() != "♠K/0" {
		t.Fatalf("牌不多了该先出小的（打 7 时 7 是级牌、比 K 大），它出了 %s", d.Combo)
	}

	// 边界正好 6 张（≤6）：还生效 —— 打 7 时这一手牌里 7 是级牌（牌力最高），
	// 所以「先出小的」出的是 8（牌力最小的单张），不是点数最小的 7。
	// 这条把「≤6」这个数钉住：改成 5 就红。
	v6 := View{
		Level: level, Seat: 1,
		Hand:    mustCards(t, "♠7/0 ♠8/0 ♠9/0 ♠10/0 ♠Q/0 ♠K/0"), // 6 张，没有对子也没有顺子（8-9-10-J-Q 缺 J）
		Counts:  [rules.Seats + 1]int{0, 6, 20, 20, 20},
		Leading: true,
	}
	d6, err := Decide(v6, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d6.Pass || d6.Combo.Type != rules.TypeSingle || d6.Combo.Cards[0].Rank != rules.Eight {
		t.Fatalf("6 张时 ④ 还生效（先出牌力最小的单张 8），它出了 %s", d6.Combo)
	}

	// 张数一多（>6）这条就不生效：这时同分，保留 `Moves` 先给的那个 —— 这里先给的是对 7。
	// 这条把「④ 只在 ≤6 张时生效」这个边界钉住（边界一改它就红的：那时会改出对 K）。
	// 手牌里**不放红心**：打 7 时 ♥7 是逢人配，会把候选搅成一堆配出来的牌型，试不出想试的东西。
	v2 := View{
		Level: level, Seat: 1,
		Hand:    mustCards(t, "♠7/0 ♣7/1 ♠K/0 ♥K/0 ♠3/0 ♠4/0 ♠5/0"), // 7 张
		Counts:  [rules.Seats + 1]int{0, 7, 20, 20, 20},
		Leading: true,
	}
	d2, err := Decide(v2, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d2.Pass || d2.Combo.Type != rules.TypePair || d2.Combo.Cards[0].Rank != rules.Seven {
		t.Fatalf("7 张时 ④ 不该生效（同分保留 Moves 先给的：一对 7），它出了 %s", d2.Combo)
	}
}

// 跟牌①「用最小的能压住」：对手出一张 5，手里 6 和 A 都压得住 —— 出 6。
func TestAIFollowsWithSmallest(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠6/0 ♠A/0"),
		Counts:    [rules.Seats + 1]int{0, 2, 20, 20, 20},
		Leading:   false,
		LastSeat:  2, // 对手
		LastCombo: comboOf(t, level, rules.TypeSingle, "♠5/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass {
		t.Fatal("手里两张都压得住，不该过牌")
	}
	if d.Combo.Type != rules.TypeSingle || d.Combo.Cards[0].Key() != "♠6/0" {
		t.Fatalf("该用最小的一手压（单张 ♠6/0），它出了 %s", d.Combo)
	}
}

// 跟牌①：**先在非炸弹里挑** —— 手里 3333 是个炸弹、AA 压得住一对 K，出的该是 AA。
//
// #259 复审 B1 的实测反例：原来按点数比大小，3 的牌点最小，于是它会甩 3 炸。
func TestAIFollowPrefersPlainPlayOverBomb(t *testing.T) {
	level := rules.Seven
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠3/0 ♥3/0 ♣3/0 ♦3/0 ♠A/0 ♥A/0"),
		Counts:    [rules.Seats + 1]int{0, 6, 20, 20, 20},
		Leading:   false,
		LastSeat:  2, // 对手
		LastCombo: comboOf(t, level, rules.TypePair, "♠K/0 ♥K/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass {
		t.Fatal("一对 A 压得住一对 K，不该过牌")
	}
	if isBomb(d.Combo) {
		t.Fatalf("非炸弹压得住就别动炸弹（手里有一对 A），它出了 %s", d.Combo)
	}
	if d.Combo.Type != rules.TypePair || d.Combo.Cards[0].Rank != rules.Ace {
		t.Fatalf("该出一对 A，它出了 %s", d.Combo)
	}
}

// 跟牌①：**炸弹之间按引擎的规则比**（第四条 二、（一）3 的档次），不是按点数 ——
// 手里 4 张 K 的炸弹和一手同花顺（3-4-5-6-7），该出的是**四张的炸弹**（档次最小的那种），
// 而不是点数看着更小的同花顺。按点数比的话会选同花顺（顶张 7 < K）。
func TestAIFollowsWithSmallestBomb(t *testing.T) {
	level := rules.Two
	v := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠K/0 ♥K/0 ♣K/0 ♦K/0 ♠3/0 ♠4/0 ♠5/0 ♠6/0 ♠7/0"),
		Counts:    [rules.Seats + 1]int{0, 9, 20, 20, 1}, // 4 号座（对手）只剩 1 张 → 到该炸的时候
		Leading:   false,
		LastSeat:  2, // 对手
		LastCombo: comboOf(t, level, rules.TypeBomb, "♠8/0 ♥8/0 ♣8/0 ♦8/0"),
	}
	d, err := Decide(v, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d.Pass {
		t.Fatal("手里有两个炸弹压得住，不该过牌")
	}
	if d.Combo.Type != rules.TypeBomb || len(d.Combo.Cards) != 4 || d.Combo.Cards[0].Rank != rules.King {
		t.Fatalf("炸弹之间按档次比（四张 < 同花顺），该出 4 张 K 的炸弹，它出了 %s", d.Combo)
	}
}

// 整局统计那条口径的两个分支：同样「队友出的那一手 + 我出炸弹」——
// **这一手走光**不算压队友，没走光才算。（见 bombsPartner 的注释。）
func TestBombsPartnerStatisticExcludesFinish(t *testing.T) {
	level := rules.Seven
	partnerLead := comboOf(t, level, rules.TypePair, "♠5/0 ♥5/0")

	// 走光：手里正好 4 张 K。
	finish := View{
		Level: level, Seat: 1,
		Hand:      mustCards(t, "♠K/0 ♥K/0 ♣K/0 ♦K/0"),
		Counts:    [rules.Seats + 1]int{0, 4, 20, 20, 20},
		Leading:   false,
		LastSeat:  3,
		LastCombo: partnerLead,
	}
	d, err := Decide(finish, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if bombsPartner(finish, d) {
		t.Fatalf("这一手走光了，不算「用炸弹压队友」，它却被算进去了：%s", d.Combo)
	}

	// 没走光的炸弹压在队友那一手上：**直接喂一个合成的一手** —— 正确代码里这条分支走不到
	// （队友出的那一手一律过牌，见 pickFollow），它只在「绝不用炸弹压队友」被改坏时才会亮，
	// 是那条规则的哨兵；单独喂进来是要把这条判定的**两个分支**都钉住。
	notFinish := finish
	notFinish.Hand = mustCards(t, "♠10/0 ♥10/0 ♣10/0 ♦10/0 ♠3/0 ♠4/0")
	overPartner := Decision{Combo: comboOf(t, level, rules.TypeBomb, "♠10/0 ♥10/0 ♣10/0 ♦10/0")}
	if !bombsPartner(notFinish, overPartner) {
		t.Fatal("没走光的炸弹压在队友那一手上，该算「用炸弹压队友」")
	}
	// 顺便把那条哨兵的另一半钉住：真跑一遍这个局面，它该过牌（队友出的那一手一律过牌），
	// 所以统计里那个 0 次是**规则在起作用**，不是语料碰不到。
	real, err := Decide(notFinish, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !real.Pass {
		t.Fatalf("队友出的那一手该过牌（除非能一次走完），它却出了 %s", real.Combo)
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
