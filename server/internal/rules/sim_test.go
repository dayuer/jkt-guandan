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
	// simPassPercent 是「明明压得住也过牌」的概率（百分比）：真实牌局里常见（留牌、
	// 让搭档先走），模拟里不主动制造的话这条路走不到。
	simPassPercent = 25

	// simMaxSteps 是一副牌的步数上限：一副 108 张，每次出牌至少少一张牌，
	// 圈数不会超过总张数；给一个远大于实际的值当「必然结束」的哨兵。
	simMaxSteps = 400
)

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

// playDeal 打完一副牌：从 leader 领出开始，一直走到分出上游。返回结束后的局面和步数。
func playDeal(rnd *rand.Rand, level Rank, leader int) (*Game, int, error) {
	hands, err := dealFor(rnd, leader)
	if err != nil {
		return nil, 0, err
	}
	g, err := NewGame(level, hands, leader)
	if err != nil {
		return nil, 0, fmt.Errorf("开一副牌：%w", err)
	}

	steps := 0
	for !g.Over() {
		steps++
		if steps > simMaxSteps {
			return nil, steps, fmt.Errorf("打了 %d 步还没结束（上限 %d）—— 卡住了？局面：%s", steps, simMaxSteps, gameText(g))
		}
		seat := g.Turn()
		for _, done := range g.Finished() {
			if done == seat {
				return nil, steps, fmt.Errorf("第 %d 步：轮到了已经出完的 %d 号座（出完的人不该再轮到）", steps, seat)
			}
		}
		if err := stepOnce(rnd, g, seat, level); err != nil {
			return nil, steps, fmt.Errorf("第 %d 步：%w", steps, err)
		}
		if err := checkHandsConserved(g); err != nil {
			return nil, steps, fmt.Errorf("第 %d 步之后：%w", steps, err)
		}
	}
	return g, steps, nil
}

// stepOnce 是驱动器的一步：按 Moves 给的选择出牌或者过牌。
func stepOnce(rnd *rand.Rand, g *Game, seat int, level Rank) error {
	hand := g.Hand(seat)
	var lead *Combo
	if !g.Leading() {
		_, last, ok := g.LastPlay()
		if !ok {
			return fmt.Errorf("%d 号座不是领出，却取不到上一手", seat)
		}
		lead = &last
	}

	moves, err := Moves(hand, level, lead)
	if err != nil {
		return fmt.Errorf("算 %d 号座（手里 %s）的出法：%w", seat, cardsText(hand), err)
	}
	if g.Leading() && len(moves) == 0 {
		return fmt.Errorf("领出时 %d 号座手里还有 %d 张牌，却一手都出不了（手里有牌至少能出单张）：%s",
			seat, len(hand), cardsText(hand))
	}
	if lead != nil {
		for _, m := range moves {
			if m.Compare(*lead) != Greater {
				return fmt.Errorf("Moves 给了压不住上一手的出法：上一手 %s，给的是 %s", lead.String(), m.String())
			}
		}
	}

	if len(moves) == 0 || (lead != nil && rnd.Intn(100) < simPassPercent) {
		if err := g.Pass(seat); err != nil {
			return fmt.Errorf("%d 号座过牌被拒（手里 %s，上一手 %s）：%w", seat, cardsText(hand), comboText(lead), err)
		}
		return nil
	}

	m := moves[rnd.Intn(len(moves))]
	held := map[int]bool{}
	for _, c := range hand {
		held[c.ID] = true
	}
	for _, c := range m.Cards {
		if !held[c.ID] {
			return fmt.Errorf("Moves 给的这一手用了不在 %d 号座手里的牌 %s，手牌 %s，这一手 %s",
				seat, c.Key(), cardsText(hand), m.String())
		}
	}
	if err := g.Play(seat, m.Cards, m.Type, m.Rank); err != nil {
		return fmt.Errorf("局面不认 Moves 给的这一手：上一手 %s，Moves 给的 %s，局面说 %w",
			comboText(lead), m.String(), err)
	}
	return nil
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

	if err := checkHandsConserved(g); err != nil {
		return fmt.Errorf("打完这一副之后：%w", err)
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
func simDeal(i int) (steps int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("引擎 panic 了：%v", r)
		}
	}()
	levels := []Rank{Two, Five, Seven, Ten, Ace}
	rnd := rand.New(rand.NewSource(int64(simSeedBase) + int64(i)))
	level := levels[i%len(levels)]
	leader := 1 + rnd.Intn(Seats)

	g, steps, err := playDeal(rnd, level, leader)
	if err != nil {
		return steps, fmt.Errorf("第 %d 副（打 %s，%d 号座领出）：%w", i+1, level.RankName(), leader, err)
	}
	if err := checkFinishedDeal(g); err != nil {
		return steps, fmt.Errorf("第 %d 副（打 %s，%d 号座领出）：%w", i+1, level.RankName(), leader, err)
	}
	return steps, nil
}

const simSeedBase = 20407

// TestSimManyDeals 跑几千副牌。种子写死：红了能原样再跑一遍（第 1 步的口径）。
func TestSimManyDeals(t *testing.T) {
	deals := simDeals
	if testing.Short() {
		deals = 200
	}

	stepsOf := make([]int, deals)
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
				stepsOf[i], errs[i] = simDeal(i)
			}
		}()
	}
	for i := 0; i < deals; i++ {
		next <- i
	}
	close(next)
	wg.Wait()

	stepsTotal, longest, failed := 0, 0, 0
	firstErr := error(nil)
	for i, err := range errs {
		stepsTotal += stepsOf[i]
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
	t.Logf("跑了 %d 副牌（%d 个 goroutine）：一共 %d 步，最长一副 %d 步（上限 %d）",
		deals, workers, stepsTotal, longest, simMaxSteps)
}

// TestSimReplayIsDeterministic 单独钉「同一副牌重放两遍结果一样」：
// 同一个种子发两次牌、按同样的选择打，局面和结果必须一致。
func TestSimReplayIsDeterministic(t *testing.T) {
	for i := 0; i < 50; i++ {
		level := []Rank{Two, Seven, Ace}[i%3]
		leader := 1 + i%Seats

		first, steps, err := playDeal(rand.New(rand.NewSource(int64(1000+i))), level, leader)
		if err != nil {
			t.Fatalf("第 %d 副（打 %s，%d 号座领出）：%v", i+1, level.RankName(), leader, err)
		}
		second, steps2, err := playDeal(rand.New(rand.NewSource(int64(1000+i))), level, leader)
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
	g, _, err := playDeal(rand.New(rand.NewSource(20408)), Seven, 2)
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
