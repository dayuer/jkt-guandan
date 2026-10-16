package rules

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// 进贡、还贡、抗贡（#204 第 6 步）的测试。
//
// 两条主线：
//   - 按第十条一条一条钉：单贡贡最大的一张（红心级牌除外）、双贡大的给上游/小的给搭档/一样大按
//     顺时针、抗贡两人合计两张大王、还牌不超过 10（全都大于 10 时还最小的）、首圈谁领出；
//   - **被拒的动作不许改局面**：进贡/还牌和出牌一样是玩家给的牌，引擎只校验 —— 校验失败必须
//     哪都没动（和 #246 复审给随机对局加的那条一个道理）。
//
// 「不超过 10」按**牌面数字**算（读法 A，仓库所有者还没拍板，见 #204 里那条 needs-user 评论
// 以及下面单列的 TestTributeReturnUnderTenPending）：打 7 时 ♦7/♠7、♥7 牌面是 7，能还。

// prevResult 造一个「上副牌的结果」：order 是出完的先后、按座位号写。
func prevResult(order ...int) Result {
	res := Result{Order: order, HeadSeat: order[0]}
	for i, s := range order {
		if s == PartnerSeat(order[0]) {
			res.PartnerPlace = i + 1
		}
	}
	return res
}

// tributeHands 造四家的手牌：每家一串空格分隔的牌面写法（1 号座在前）。
func tributeHands(t *testing.T, seat1, seat2, seat3, seat4 string) [Seats][]Card {
	t.Helper()
	var out [Seats][]Card
	for i, s := range []string{seat1, seat2, seat3, seat4} {
		out[i] = cardsOf(t, strings.Fields(s))
	}
	return out
}

// handsNow 取 Tribute 现在的四家手牌，**不管贡完没贡完** —— 同一包内直接读，
// 用来查「被拒的动作有没有动到局面」。
func handsNow(tr *Tribute) [Seats][]Card {
	var out [Seats][]Card
	for s := 1; s <= Seats; s++ {
		out[s-1] = append([]Card{}, tr.hands[s]...)
	}
	return out
}

// handsText4 把四家手牌写成一串，用来比「局面变没变」。
func handsText4(hands [Seats][]Card) string {
	out := ""
	for i, h := range hands {
		out += fmt.Sprintf("%d号:%s ", i+1, cardsText(h))
	}
	return out
}

// cardIn 从手里按写法取一张牌。
func cardIn(t *testing.T, hand []Card, key string) Card {
	t.Helper()
	for _, c := range hand {
		if c.Key() == key {
			return c
		}
	}
	t.Fatalf("手里没有 %s（手牌：%s）", key, cardsText(hand))
	return Card{}
}

// cardCounts 数总张数 + 每张牌出现几次（守恒用）。
func cardCounts(hands [Seats][]Card) (int, string) {
	n := 0
	seen := map[int]int{}
	var ids []int
	for _, h := range hands {
		for _, c := range h {
			n++
			if seen[c.ID] == 0 {
				ids = append(ids, c.ID)
			}
			seen[c.ID]++
		}
	}
	sort.Ints(ids) // 按 ID 排好再写出来：牌挪了位置顺序会变，不排就对不上了
	out := ""
	for _, id := range ids {
		out += fmt.Sprintf("%d×%d ", id, seen[id])
	}
	return n, out
}

// reject 灌一个该被拒的动作，查三件事：确实被拒、**错误码就是那一个**、局面一个字都没动。
//
// 比错误码不只是好看：牌例（jkt-2 起草的 tribute.json）只比取值不比句子，取值改了就是改协议；
// 而且「同一个动作报错但报成了别的码」也是一种改坏 —— 只查 err != nil 是查不出来的。
func reject(what string, want error, before string, tr *Tribute, act func() error) error {
	err := act()
	if err == nil {
		return fmt.Errorf("%s：这个动作不合规，却被接受了", what)
	}
	var te *TributeError
	if !errors.As(err, &te) {
		return fmt.Errorf("%s：报的不是贡牌的规则错误（封闭取值那一族）：%v", what, err)
	}
	if !errors.Is(err, want) {
		return fmt.Errorf("%s：错误码不对，想要 %v，拿到 %q（%v）", what, reasonOfTribute(want), te.Reason, err)
	}
	if after := handsText4(handsNow(tr)); after != before {
		return fmt.Errorf("%s：被拒之后手牌变了\n之前：%s\n之后：%s", what, before, after)
	}
	return nil
}

// reasonOfTribute 取哨兵的取值（哨兵本身没有句子）。
func reasonOfTribute(err error) string {
	var te *TributeError
	if errors.As(err, &te) {
		return string(te.Reason)
	}
	return fmt.Sprintf("%v", err)
}

func TestTributeSingleGiveMustBeBiggestAndNotWild(t *testing.T) {
	// 单贡：1 号座头游、搭档第 3（不是双下），下游是 4 号座。
	prev := prevResult(1, 2, 3, 4)
	hands := tributeHands(t,
		"♠K/0 ♥3/0 ♠3/0 ♠5/0",
		"♠2/0 ♥2/0 ♣3/0 ♦5/0",
		"♠4/0 ♥4/0 ♣4/0 ♦4/0",
		"♠A/0 ♥7/0 ♦5/1 ♣6/0",
	)
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind() != TributeSingle {
		t.Fatalf("该是单贡，得到 %v", tr.Kind())
	}
	if g := tr.Givers(); len(g) != 1 || g[0] != 4 {
		t.Fatalf("该由下游 4 号座进贡，得到 %v", g)
	}

	before := handsText4(handsNow(tr))
	// 红心级牌（逢人配）不能贡（第十条 二）。
	if err := reject("贡红心级牌", ErrTributeWildCannotGive, before, tr, func() error {
		return tr.Give(4, cardIn(t, hands[3], "♥7/0"))
	}); err != nil {
		t.Fatal(err)
	}
	// 不是最大的一张也不能贡：手里最大的是 ♠A。
	for _, key := range []string{"♦5/1", "♣6/0"} {
		key := key
		if err := reject("贡非最大的 "+key, ErrTributeNotBiggest, before, tr, func() error {
			return tr.Give(4, cardIn(t, hands[3], key))
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 不用进贡的人也不能贡。
	if err := reject("不用进贡的人贡牌", ErrTributeNotAGiver, before, tr, func() error {
		return tr.Give(2, cardIn(t, hands[1], "♠2/0"))
	}); err != nil {
		t.Fatal(err)
	}
	// 贡别人手里的牌。
	if err := reject("贡别人手里的牌", ErrTributeCardNotInHand, before, tr, func() error {
		return tr.Give(4, cardIn(t, hands[2], "♠4/0"))
	}); err != nil {
		t.Fatal(err)
	}

	if err := tr.Give(4, cardIn(t, hands[3], "♠A/0")); err != nil {
		t.Fatalf("贡最大的一张：%v", err)
	}
	if tr.Done() {
		t.Fatal("还没还牌，不该算完")
	}
	if _, ok := tr.Leader(); ok {
		t.Fatal("还没还牌，首圈领出的人不该定下来")
	}
	if seat, ok := tr.Receiver(4); !ok || seat != 1 {
		t.Fatalf("单贡该贡给上游 1 号座，得到 %d（ok=%v）", seat, ok)
	}
}

// TestTributeWildIsNeverTributeEvenIfBiggest 钉的是「红心级牌除外」这一条**单独**成立：
// 手里只有逢人配和一张小牌时，逢人配按当前级数下的牌力是最大的 —— 但仍然不能贡。
// （上一条测试里手里有 ♠A，最大那张轮不到逢人配，那条查不出这一条被改坏。）
func TestTributeWildIsNeverTributeEvenIfBiggest(t *testing.T) {
	// 手里还有一张 ♦7/0：它不是逢人配（只有红心级牌才是），但牌力和逢人配一样大 ——
	// 少了「红心级牌除外」这一条，贡它就会以「和最大那张一样大」的理由被放过去。
	hands := tributeHands(t, "♠K/0", "♠2/0", "♠4/0", "♥7/0 ♦7/0 ♠5/0")
	tr, err := NewTribute(Seven, hands, prevResult(1, 2, 3, 4))
	if err != nil {
		t.Fatal(err)
	}
	before := handsText4(handsNow(tr))
	if err := reject("手里最大的就是逢人配，也不能贡", ErrTributeWildCannotGive, before, tr, func() error {
		return tr.Give(4, cardIn(t, hands[3], "♥7/0"))
	}); err != nil {
		t.Fatal(err)
	}
	// 实牌里最大的是 ♦7/0（和逢人配一样大，但它是实牌），贡它没问题。
	if err := tr.Give(4, cardIn(t, hands[3], "♦7/0")); err != nil {
		t.Fatalf("实牌里最大的 ♦7/0 该能贡：%v", err)
	}
}

func TestTributeSingleReturnUnderTenAndLeader(t *testing.T) {
	prev := prevResult(1, 2, 3, 4)
	hands := tributeHands(t, "♠K/0 ♠3/0 ♥7/0", "♠2/0 ♥A/0", "♠4/0", "♠A/0 ♦9/0")
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Give(4, cardIn(t, hands[3], "♠A/0")); err != nil {
		t.Fatal(err)
	}
	before := handsText4(handsNow(tr))
	// 1 号座手里有牌面不超过 10 的牌（♠3、♥7），只能还这种 —— ♠K 不能还。
	if err := reject("还 ♠K", ErrTributeCannotReturn, before, tr, func() error {
		return tr.Return(1, cardIn(t, hands[0], "♠K/0"))
	}); err != nil {
		t.Fatal(err)
	}
	// 没收贡的人不能还。
	if err := reject("没收贡的人还牌", ErrTributeNotAReceiver, before, tr, func() error {
		return tr.Return(2, cardIn(t, hands[1], "♠2/0"))
	}); err != nil {
		t.Fatal(err)
	}
	// 手里没有的牌不能还（拿 2 号座手里的牌试；贡进来的 ♠A 已经在 1 号座手里了，
	// 拿它试会落到下面「不能还」那一条上 —— 这是写这条用例时自己绊的一次）。
	if err := reject("还手里没有的牌", ErrTributeCardNotInHand, before, tr, func() error {
		return tr.Return(1, cardIn(t, hands[1], "♠2/0"))
	}); err != nil {
		t.Fatal(err)
	}
	// 再来一张**手里没有、而且按 canReturn 也不该还**的大牌：这样「手里有没有」这一段
	// 就是唯一拦得住它的规则 —— 少了 holds，报出来的会是 cannot-return 而不是 card-not-in-hand。
	if err := reject("还手里没有的大牌", ErrTributeCardNotInHand, before, tr, func() error {
		return tr.Return(1, cardIn(t, hands[1], "♥A/0"))
	}); err != nil {
		t.Fatal(err)
	}

	// 牌面是 7 的 ♥7（逢人配）按读法 A 也算「不超过 10」，能还。
	if err := tr.Return(1, cardIn(t, hands[0], "♥7/0")); err != nil {
		t.Fatalf("读法 A 下牌面 7 的牌该能还：%v", err)
	}
	if err := tr.Return(1, cardIn(t, hands[0], "♠3/0")); !errors.Is(err, ErrTributeAlreadyReturned) {
		t.Fatalf("同一家不该能还两次，该报 already-returned：%v", err)
	}
	if !tr.Done() {
		t.Fatal("贡完还完该算完了")
	}
	if seat, ok := tr.Leader(); !ok || seat != 4 {
		t.Fatalf("单贡后该由进贡的 4 号座领出（第十条 二），得到 %d（ok=%v）", seat, ok)
	}
	g, err := tr.Start()
	if err != nil {
		t.Fatalf("开打：%v", err)
	}
	if g.Turn() != 4 {
		t.Fatalf("开打后该轮到 4 号座，得到 %d", g.Turn())
	}
	if h := g.Hand(1); !hasCard(h, "♠A/0") {
		t.Fatalf("1 号座该拿到贡来的 ♠A，手里是 %s", cardsText(h))
	}
	if h := g.Hand(4); !hasCard(h, "♥7/0") {
		t.Fatalf("4 号座该拿回还来的 ♥7，手里是 %s", cardsText(h))
	}
}

func TestTributeSingleReturnSmallestWhenAllAboveTen(t *testing.T) {
	prev := prevResult(1, 2, 3, 4)
	hands := tributeHands(t, "♠K/0 ♥A/0 ♦J/0", "♠2/0", "♠4/0", "♠A/0") // 1 号座全大于 10
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Give(4, cardIn(t, hands[3], "♠A/0")); err != nil {
		t.Fatal(err)
	}
	before := handsText4(handsNow(tr))
	// 全都大于 10：只能还最小的（按当前级数下的大小）—— J 最小，K、A 不能还。
	for _, key := range []string{"♠K/0", "♥A/0"} {
		key := key
		if err := reject("还 "+key, ErrTributeCannotReturn, before, tr, func() error {
			return tr.Return(1, cardIn(t, hands[0], key))
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Return(1, cardIn(t, hands[0], "♦J/0")); err != nil {
		t.Fatalf("还最小的 ♦J：%v", err)
	}
}

func TestTributeDoubleGiveAndReturn(t *testing.T) {
	// 双下：1、3 一方拿了头游和二游 → 2、4 两家都进贡。
	prev := prevResult(1, 3, 2, 4)
	hands := tributeHands(t,
		"♠3/0 ♦4/0",
		"♠K/0 ♦6/0", // 2 号座：贡 ♠K（比 4 号座的小）
		"♠5/0 ♦7/0",
		"♠A/0 ♦8/0", // 4 号座：贡 ♠A（大）
	)
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind() != TributeDouble {
		t.Fatalf("该是双贡，得到 %v", tr.Kind())
	}
	if g := tr.Givers(); len(g) != 2 || g[0] != 2 || g[1] != 4 {
		t.Fatalf("该由 2、4 号座进贡（双下两家），得到 %v", g)
	}
	if err := tr.Give(2, cardIn(t, hands[1], "♠K/0")); err != nil {
		t.Fatal(err)
	}
	if _, ok := tr.Receiver(2); ok {
		t.Fatal("还有人没贡完，收贡的人不该定下来")
	}
	if err := tr.Give(4, cardIn(t, hands[3], "♠A/0")); err != nil {
		t.Fatal(err)
	}
	if seat, ok := tr.Receiver(4); !ok || seat != 1 {
		t.Fatalf("♠A 大，该贡给上游 1 号座，得到 %d", seat)
	}
	if seat, ok := tr.Receiver(2); !ok || seat != 3 {
		t.Fatalf("♠K 小，该贡给上游的搭档 3 号座，得到 %d", seat)
	}
	if err := tr.Return(1, cardIn(t, hands[0], "♠3/0")); err != nil {
		t.Fatal(err)
	}
	if _, ok := tr.Leader(); ok {
		t.Fatal("还有人没收完，领出的人不该定下来")
	}
	if err := tr.Return(3, cardIn(t, hands[2], "♠5/0")); err != nil {
		t.Fatal(err)
	}
	// 还牌后由贡给上游的那位（4 号座）领出首圈（第十条 三）。
	if seat, ok := tr.Leader(); !ok || seat != 4 {
		t.Fatalf("双贡后该由贡给上游的 4 号座领出，得到 %d（ok=%v）", seat, ok)
	}
	// 守恒：进贡还贡只是把牌挪了个位置，四家的牌一张不多一张不少。
	gotN, gotText := cardCounts(handsNow(tr))
	wantN, wantText := cardCounts(hands)
	if gotN != wantN {
		t.Fatalf("进贡还贡之后总张数变了：%d → %d", wantN, gotN)
	}
	if gotText != wantText {
		t.Fatalf("牌没守恒：\n之前：%s\n之后：%s", wantText, gotText)
	}
}

func TestTributeDoubleFirstGiverIsBigger(t *testing.T) {
	// 双贡里 **givers[0]（2 号座）贡的更大** 那一支：大的给上游、小的给搭档。
	// 上面那条用例是 4 号座贡的更大，走的是另一支 —— auditor-249 指出这条分支没被走到。
	prev := prevResult(1, 3, 2, 4)
	hands := tributeHands(t,
		"♠3/0 ♦4/0",
		"♠A/0 ♦6/0", // 2 号座：贡 ♠A（大）
		"♠5/0 ♦7/0",
		"♠K/0 ♦8/0", // 4 号座：贡 ♠K（小）
	)
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Give(2, cardIn(t, hands[1], "♠A/0")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Give(4, cardIn(t, hands[3], "♠K/0")); err != nil {
		t.Fatal(err)
	}
	if seat, ok := tr.Receiver(2); !ok || seat != 1 {
		t.Fatalf("♠A 大，该贡给上游 1 号座，得到 %d", seat)
	}
	if seat, ok := tr.Receiver(4); !ok || seat != 3 {
		t.Fatalf("♠K 小，该贡给上游的搭档 3 号座，得到 %d", seat)
	}
	if err := tr.Return(1, cardIn(t, hands[0], "♠3/0")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Return(3, cardIn(t, hands[2], "♠5/0")); err != nil {
		t.Fatal(err)
	}
	if seat, ok := tr.Leader(); !ok || seat != 2 {
		t.Fatalf("该由贡给上游的 2 号座领出，得到 %d（ok=%v）", seat, ok)
	}
}

func TestTributeDoubleEqualCardsGoClockwise(t *testing.T) {
	// 双贡、两张贡牌一样大：各给自己顺时针的邻座（第十条 三）。
	prev := prevResult(1, 3, 2, 4)
	hands := tributeHands(t, "♠3/0", "♠A/0", "♠5/0", "♥A/0")
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Give(2, cardIn(t, hands[1], "♠A/0")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Give(4, cardIn(t, hands[3], "♥A/0")); err != nil {
		t.Fatal(err)
	}
	// 座位 1→2→3→4 是逆时针，所以顺时针的邻座是 s−1：2 号座给 1 号座、4 号座给 3 号座。
	if seat, _ := tr.Receiver(2); seat != ClockwiseSeat(2) || seat != 1 {
		t.Fatalf("一样大时 2 号座该贡给自己顺时针的邻座 1，得到 %d", seat)
	}
	if seat, _ := tr.Receiver(4); seat != ClockwiseSeat(4) || seat != 3 {
		t.Fatalf("一样大时 4 号座该贡给自己顺时针的邻座 3，得到 %d", seat)
	}
	if err := tr.Return(1, cardIn(t, hands[0], "♠3/0")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Return(3, cardIn(t, hands[2], "♠5/0")); err != nil {
		t.Fatal(err)
	}
	if seat, ok := tr.Leader(); !ok || seat != 2 {
		t.Fatalf("一样大时该由贡给上游的 2 号座领出，得到 %d（ok=%v）", seat, ok)
	}
}

func TestTributeResisted(t *testing.T) {
	prev := prevResult(1, 3, 2, 4) // 双下：2、4 要进贡
	hands := tributeHands(t,
		"♠3/0",
		"大王/0 ♦6/0", // 2 号座一个大王
		"♠5/0",
		"大王/1 ♦8/0", // 4 号座一个大王 —— 合计两个，抗贡
	)
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind() != TributeResisted {
		t.Fatalf("两人合计两个大王该抗贡，得到 %v", tr.Kind())
	}
	if !tr.Done() {
		t.Fatal("抗贡不用贡也不用还，一上来就该算完")
	}
	if seat, ok := tr.Leader(); !ok || seat != 1 {
		t.Fatalf("抗贡时首圈由上游 1 号座领出（第十条 四），得到 %d（ok=%v）", seat, ok)
	}
	before := handsText4(handsNow(tr))
	if err := reject("抗贡了还进贡", ErrTributeResisted, before, tr, func() error {
		return tr.Give(2, cardIn(t, hands[1], "♦6/0"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := reject("抗贡了还还牌", ErrTributeResisted, before, tr, func() error {
		return tr.Return(1, cardIn(t, hands[0], "♠3/0"))
	}); err != nil {
		t.Fatal(err)
	}
	if g, err := tr.Start(); err != nil || g.Turn() != 1 {
		t.Fatalf("抗贡后开打该轮到 1 号座：%v（%v）", g, err)
	}
	// 抗贡也要给出是谁抗的（界面要显示）。
	if g := tr.Givers(); len(g) != 2 {
		t.Fatalf("抗贡也要给出要进贡的是谁，得到 %v", g)
	}
}

func TestTributeOneBigJokerIsNotResisted(t *testing.T) {
	// 单贡时下游只有一张大王：不抗贡（第十条 四说的是两个大王）。
	prev := prevResult(1, 2, 3, 4)
	hands := tributeHands(t, "♠3/0", "♠2/0", "♠4/0", "大王/0 ♦6/0")
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind() != TributeSingle {
		t.Fatalf("只有一张大王不该抗贡，得到 %v", tr.Kind())
	}
	if err := tr.Give(4, cardIn(t, hands[3], "大王/0")); err != nil {
		t.Fatalf("大王该是手里最大的一张，能贡：%v", err)
	}
}

func TestTributeDoubleResistedCountsBoth(t *testing.T) {
	// 一个人手里两个大王、另一个人没有：合计两个，也算抗贡。
	prev := prevResult(1, 3, 2, 4)
	hands := tributeHands(t, "♠3/0", "大王/0 大王/1", "♠5/0", "♦8/0")
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind() != TributeResisted {
		t.Fatalf("一个人有两个大王也该抗贡（双下两家合计），得到 %v", tr.Kind())
	}
}

// TestTributeRejectsInconsistentPrevResult 钉「上副牌的结果必须自洽」（auditor-249 的三条探针）：
// 不自洽的话贡牌会算出荒唐结果 —— 上游贡给自己、0 号座进贡、甚至下标越界。
func TestTributeRejectsInconsistentPrevResult(t *testing.T) {
	hands := tributeHands(t, "♠3/0", "♠2/0", "♠4/0", "♠5/0")
	for _, tc := range []struct {
		name string
		prev Result
	}{
		{"名次里有 7 号座", Result{Order: []int{1, 2, 3, 7}, HeadSeat: 1, PartnerPlace: 3}},
		{"名次里有 0 号座", Result{Order: []int{1, 2, 3, 0}, HeadSeat: 1, PartnerPlace: 3}},
		// 这条 [1 2 2 4] 其实是被下面「搭档名次对不上」拦住的（Order[2]=3 才对得上）；
		// 要**只**由「名次里有重复」那一段拦住，得用 [1 2 3 3]：它同时缺 4 号座，
		// 但排列检查里 validSeat 那一半查不出来，只有查重那一半能拦。
		{"名次里有重复", Result{Order: []int{1, 2, 2, 4}, HeadSeat: 1, PartnerPlace: 3}},
		{"名次里有重复（只由查重拦得住）", Result{Order: []int{1, 2, 3, 3}, HeadSeat: 1, PartnerPlace: 3}},
		// PartnerPlace 写成 2（3 号座确实在第 2 名），所以只有「头游就是第一个出完的」这一条
		// 能拦住它 —— 不然后面会按 HeadSeat=1 算出「上游自己贡给自己」（auditor-249 的探针）。
		{"头游不是第一个出完的", Result{Order: []int{2, 3, 4, 1}, HeadSeat: 1, PartnerPlace: 2}},
		{"搭档名次和名次表对不上", Result{Order: []int{1, 2, 3, 4}, HeadSeat: 1, PartnerPlace: 4}},
		{"头游越界", Result{Order: []int{1, 2, 3, 4}, HeadSeat: 7, PartnerPlace: 3}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("该返回错误，却 panic 了：%v", r)
				}
			}()
			if _, err := NewTribute(Seven, hands, tc.prev); err == nil {
				t.Fatalf("结果不自洽该报错：%+v", tc.prev)
			}
		})
	}
}

func TestTributeRejectsBadInput(t *testing.T) {
	hands := tributeHands(t, "♠3/0", "♠2/0", "♠4/0", "♠5/0")
	for _, prev := range []Result{
		{Order: []int{1, 2}, HeadSeat: 1, PartnerPlace: 2},       // 名次不全
		{Order: []int{1, 2, 3, 4}, HeadSeat: 5, PartnerPlace: 2}, // 上游不是 1–4
		{Order: []int{1, 2, 3, 4}, HeadSeat: 1, PartnerPlace: 1}, // 搭档名次不对（1 是上游自己）
		{Order: []int{1, 2, 3, 4}, HeadSeat: 1, PartnerPlace: 5}, // 搭档名次越界
	} {
		if _, err := NewTribute(Seven, hands, prev); err == nil {
			t.Fatalf("上副牌结果不完整该报错：%+v", prev)
		}
	}
	if _, err := NewTribute(Rank(99), hands, prevResult(1, 2, 3, 4)); err == nil {
		t.Fatal("级数不合法该报错")
	}
}

func TestTributeDoubleReturnBeforeAllGiven(t *testing.T) {
	// 双贡：只有一家贡完就想还牌 → not-all-given（第十条 三：两家都要贡）。
	hands := tributeHands(t, "♠3/0 ♦4/0", "♠K/0 ♦6/0", "♠5/0 ♦7/0", "♠A/0 ♦8/0")
	tr, err := NewTribute(Seven, hands, prevResult(1, 3, 2, 4))
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Give(2, cardIn(t, hands[1], "♠K/0")); err != nil {
		t.Fatal(err)
	}
	before := handsText4(handsNow(tr))
	if err := reject("还有人没贡完就还牌", ErrTributeNotAllGiven, before, tr, func() error {
		return tr.Return(1, cardIn(t, hands[0], "♠3/0"))
	}); err != nil {
		t.Fatal(err)
	}
}

// TestTributeReasonValuesPinTheProtocol 把十个取值逐字钉住：牌例（tribute.json）只比取值，
// 取值改了就是改协议。这张表和 #204 评论 6213375545 第三节那张表逐字对应。
func TestTributeReasonValuesPinTheProtocol(t *testing.T) {
	for _, tc := range []struct {
		sentinel error
		want     string
	}{
		{ErrTributeResisted, "resisted"},
		{ErrTributeNotAGiver, "not-a-giver"},
		{ErrTributeAlreadyGave, "already-gave"},
		{ErrTributeNotBiggest, "not-biggest"},
		{ErrTributeWildCannotGive, "wild-cannot-give"},
		{ErrTributeCardNotInHand, "card-not-in-hand"},
		{ErrTributeNotAllGiven, "not-all-given"},
		{ErrTributeNotAReceiver, "not-a-receiver"},
		{ErrTributeAlreadyReturned, "already-returned"},
		{ErrTributeCannotReturn, "cannot-return"},
	} {
		if got := reasonOfTribute(tc.sentinel); got != tc.want {
			t.Fatalf("取值对不上：想要 %q，得到 %q", tc.want, got)
		}
		// 报错句子换一种措辞，errors.Is 照样判得出来；换一个取值就判不出来。
		e := tributeErr(tc.sentinel.(*TributeError), "句子随便写")
		if !errors.Is(e, tc.sentinel) {
			t.Fatalf("%q 的哨兵 errors.Is 判不出来", tc.want)
		}
		for _, other := range []error{ErrTributeResisted, ErrTributeCannotReturn} {
			if other == tc.sentinel {
				continue
			}
			if errors.Is(e, other) && reasonOfTribute(other) != tc.want {
				t.Fatalf("%q 的错被判成了 %q", tc.want, reasonOfTribute(other))
			}
		}
	}
}

func TestTributeHandsAndStartRequireDone(t *testing.T) {
	prev := prevResult(1, 2, 3, 4)
	hands := tributeHands(t, "♠3/0", "♠2/0", "♠4/0", "♠A/0")
	tr, err := NewTribute(Seven, hands, prev)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Hands(); err == nil {
		t.Fatal("还没贡完，不该能取手牌")
	}
	if _, err := tr.Start(); err == nil {
		t.Fatal("还没贡完，不该能开打")
	}
	if err := tr.Give(4, cardIn(t, hands[3], "♠A/0")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Give(4, cardIn(t, hands[3], "♠A/0")); !errors.Is(err, ErrTributeAlreadyGave) {
		t.Fatalf("同一家不该能贡两次，该报 already-gave：%v", err)
	}
	if err := tr.Return(1, cardIn(t, hands[0], "♠3/0")); err != nil {
		t.Fatal(err)
	}
	if got, err := tr.Hands(); err != nil {
		t.Fatalf("贡完还完该能取手牌：%v", err)
	} else if len(got) != Seats {
		t.Fatalf("该有四家手牌，得到 %d 家", len(got))
	}
}

func TestTributeKindString(t *testing.T) {
	for _, tc := range []struct {
		k    TributeKind
		want string
	}{
		{TributeSingle, "single"},
		{TributeDouble, "double"},
		{TributeResisted, "resisted"},
		{TributeKind(0), "TributeKind(0)"},
	} {
		if got := tc.k.String(); got != tc.want {
			t.Fatalf("%d 该写成 %q，得到 %q", tc.k, tc.want, got)
		}
	}
}

func TestTributeNextUsesScoring(t *testing.T) {
	// 第一副打完：双下（头游 + 二游是同一方）→ 该升 3 级，下一副要继续进贡。
	next, err := Next([]Result{prevResult(1, 3, 2, 4)})
	if err != nil {
		t.Fatalf("算下一副：%v", err)
	}
	if !next.Tribute {
		t.Fatal("已经打过一副，下一副该要进贡（第十条 二「从第二副牌开始」）")
	}
	if next.Level != Rank(5) {
		t.Fatalf("打 2 双下升 3 级，下一副该打 5，得到 %d", next.Level)
	}
	if next.Score.MatchRounds != 1 {
		t.Fatalf("该记了 1 局，得到 %d", next.Score.MatchRounds)
	}
}

// TestTributeReturnUnderTenPending 是「还牌不超过 10」这一条的牌例，**单列出来**：
// 仓库所有者还没拍板按哪种读法，改起来就改这一张表（判定集中在 canReturn 一个函数里）。
//
//	读法 A（现在按这个实现）：按**牌面数字**，2–10 都能还 —— 打 7 时 ♠7（级牌）、♥7（逢人配）
//	        牌面是 7，也能还。
//	读法 B：按第四条 一、1 的大小，级牌排在 A 之上，不算「不超过 10」，不能还。
//
// 「全都大于 10 时还最小的」两种读法都按当前级数下的大小（下面最后两行就是它）。
// 真要改成读法 B，把「能还」那两行改成 false、把 canReturn 里的判断换成按 Power 比即可。
func TestTributeReturnUnderTenPending(t *testing.T) {
	// 每行：手里的牌 / 想还的那张 / 现在（读法 A）能不能还
	cases := []struct {
		hand  string
		card  string
		canDo bool
		why   string
	}{
		{"♠3/0 ♠K/0", "♠3/0", true, "牌面 3 ≤ 10"},
		{"♠3/0 ♠K/0", "♠K/0", false, "手里有 ≤ 10 的牌时，只能还那种"},
		{"♠10/0 ♠J/0", "♠10/0", true, "牌面 10 正好是边界"},
		{"♠10/0 ♠J/0", "♠J/0", false, "11 不行"},
		{"♠7/0 ♠8/0", "♠7/0", true, "牌面 7：级牌（♠7）按读法 A 能还 —— **待确认**"},
		{"♥7/0 ♠8/0", "♥7/0", true, "牌面 7：逢人配（♥7）按读法 A 能还 —— **待确认**"},
		{"♠K/0 ♥A/0", "♠K/0", true, "全大于 10：还最小的（K 比 A 小）"},
		{"♠K/0 ♥A/0", "♥A/0", false, "全大于 10：A 不是最小的"},
	}
	for _, tc := range cases {
		hands := tributeHands(t,
			tc.hand,        // 1 号座（收贡、要还牌的那家）
			"♠2/0", "♠4/0", // 2、3 号座
			"♠A/0 ♦5/0", // 4 号座（下游、进贡）
		)
		tr, err := NewTribute(Seven, hands, prevResult(1, 2, 3, 4))
		if err != nil {
			t.Fatal(err)
		}
		if err := tr.Give(4, cardIn(t, hands[3], "♠A/0")); err != nil {
			t.Fatal(err)
		}
		card := cardIn(t, hands[0], tc.card)
		err = tr.Return(1, card)
		if tc.canDo && err != nil {
			t.Fatalf("手里 %s、还 %s：该能还（%s），却被拒：%v", tc.hand, tc.card, tc.why, err)
		}
		if !tc.canDo && err == nil {
			t.Fatalf("手里 %s、还 %s：不该能还（%s），却被接受了", tc.hand, tc.card, tc.why)
		}
	}
}

func TestTributeNextFirstDealNeedsNoTribute(t *testing.T) {
	// 一场的第一副牌不进贡（第十条 二「从第二副牌开始」），从 2 打起。
	next, err := Next(nil)
	if err != nil {
		t.Fatalf("算第一副：%v", err)
	}
	if next.Tribute {
		t.Fatal("第一副不该进贡")
	}
	if next.Level != Two {
		t.Fatalf("一场从打 2 开始，得到 %d", next.Level)
	}
}

func TestTributeNextAfterMatchOverNeedsNoTribute(t *testing.T) {
	// 一直双下（升 3 级）打到过 A：本场结束，再开的是**新一场的第一副** —— 也不进贡。
	for i := 1; i <= 12; i++ {
		results := make([]Result, i)
		for k := range results {
			results[k] = prevResult(1, 3, 2, 4)
		}
		next, err := Next(results)
		if err != nil {
			t.Fatalf("第 %d 副之后：%v", i, err)
		}
		if !next.Score.MatchOver {
			continue
		}
		if next.Tribute {
			t.Fatalf("第 %d 副之后本场结束（过了 A），下一副是新一场第一副，不该进贡", i)
		}
		return
	}
	t.Fatal("连打 12 副双下都没过 A —— scoring 那边的进度和这里对不上")
}

func hasCard(hand []Card, key string) bool {
	for _, c := range hand {
		if c.Key() == key {
			return true
		}
	}
	return false
}
