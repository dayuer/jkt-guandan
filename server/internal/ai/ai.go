// 第一版 AI 的策略：**规则型**，给 `rules.Moves` 的候选打分再挑一个。
//
// 目标是「像样」不是「强」（#88 的「不做」：不做搜索、不做训练模型、不做残局穷举）：
// 不犯蠢、不拆炸弹、知道配合队友，就够了。策略条文写在设计（#88 的评论）里，这里逐条对着实现：
//
//	领出：① 能一次走完就走完（**由 ② 承载**，见 pickLead 的注释）；② 优先张数多的牌型；③ 不主动拆炸弹；④ 牌不多了先出小的；
//	      ⑤ 单张先出孤张。
//	跟牌：① 用最小的能压住；② 队友领先时让牌；③ 对手快走完就不让、该炸就炸；
//	      ④ 炸弹的时机（平时不炸，残局/对手要走才炸；**绝不用炸弹压队友**）；⑤ 要不起就过。
//
// **纯函数**：同样的 `View` + `Config` 一定给同样的答案（不读时间、不掷骰子）。难度差别做成参数，
// 不做成随机 —— 测试要能照着复现。
package ai

import (
	"errors"
	"fmt"

	"github.com/dayuer/jkt-guandan/server/internal/rules"
)

// Config 是策略参数。第一版只有一组默认值（一档难度），但差别都做成参数 ——
// 以后要加「新手 / 熟练」档，改参数就行，不用重写策略。
type Config struct {
	// YieldToPartner：队友出的那一手没人压得住时让给他（过牌）。
	YieldToPartner bool
	// PressOpponentLeft：对手剩这么多张（含）以内，就不再让、能压就压。
	PressOpponentLeft int
	// BombWhenOwnLeft：自己剩这么多张（含）以内，该用炸弹就用（「该炸的时机」的一种）。
	BombWhenOwnLeft int
}

// DefaultConfig 是第一版（也是唯一一档）的参数。
func DefaultConfig() Config {
	return Config{YieldToPartner: true, PressOpponentLeft: 2, BombWhenOwnLeft: 6}
}

// Decision 是 AI 挑的一手。`Pass` 为真就是过牌；否则 `Combo` 是从 `rules.Moves` 的候选里挑的
// 一手 —— 调用方照着它调 `Game.Play(seat, c.Cards, c.Type, c.Rank)` 就行。
type Decision struct {
	Pass  bool
	Combo rules.Combo
}

// Decide 挑一手。
//
// 领出（这一圈还没人出过牌）时返回的**一定**是一个合法牌型；跟牌时挑不到压得住的就过牌。
func Decide(v View, cfg Config) (Decision, error) {
	if !validSeat(v.Seat) {
		return Decision{}, fmt.Errorf("AI：座位 %d 不是 1–%d", v.Seat, rules.Seats)
	}
	cands, err := rules.Moves(v.Hand, v.Level, leadOf(v))
	if err != nil {
		return Decision{}, fmt.Errorf("AI：算不出合法出法：%w", err)
	}

	if v.Leading {
		if len(cands) == 0 {
			// 领出时手里有牌就一定至少能出单张 —— 一个候选都没有，是 `Moves` 漏了牌型。
			return Decision{}, errors.New("AI：领出时一个出法都没有（Moves 漏了牌型？）")
		}
		return Decision{Combo: pickLead(v, cands)}, nil
	}
	if len(cands) == 0 {
		return Decision{Pass: true}, nil // 要不起
	}
	return pickFollow(v, cands, cfg), nil
}

func validSeat(seat int) bool { return seat >= 1 && seat <= rules.Seats }

// leadOf 把 View 变成 `rules.Moves` 要的「上一手」：领出时是 nil。
func leadOf(v View) *rules.Combo {
	if v.Leading {
		return nil
	}
	c := v.LastCombo
	return &c
}

// ── 领出 ──

// pickLead 挑一手领出（上面「领出」那五条）。打分取最高的；同分保留先出现的那个（**确定**）。
func pickLead(v View, cands []rules.Combo) rules.Combo {
	// ③ 不主动拆炸弹：**还有别的选择**时，先把这个可能性去掉 —— 做成结构性的首选，
	// 而不是打个很重的分（打分要跟别的项比大小，容易被「一次走完」之类的权重顶掉）。
	if keep := notSplittingBombs(v, cands); len(keep) > 0 {
		cands = keep
	}
	best, bestScore := cands[0], 0
	for i, c := range cands {
		score := 0
		// ①「能一次走完就走完」**不单独加分**：能把整手牌一次走完的那一手，张数一定最多，
		// ② 就把它挑出来了 —— 加 1_000_000 也永远改变不了结果。
		// （实测：从旧版本里删掉这个分支，整个包的测试 + 整局驱动照样全绿，说明没有用例能区分它。
		//   一条谁都区分不出来的分支就是噪声，所以这里只留 ②。「一次走完」这件事本身由
		//   `TestAIFinishesInOneGo` 钉着。）
		score += len(c.Cards) * 1_000 // ② 张数多优先 / ① 一次走完
		if isBomb(c) {
			score -= 30_000 // 领出时也别急着丢炸弹
		}
		if len(c.Cards) == 1 && isLone(v.Hand, v.Level, c.Cards[0]) {
			score += 500 // ⑤ 孤张先出
		}
		if len(v.Hand) <= 6 {
			// ④ 牌不多了：先出小的，大的留作后手。
			score -= int(c.Rank.Power(v.Level)) * 10
		}
		if i == 0 || score > bestScore {
			best, bestScore = c, score
		}
	}
	return best
}

// ── 跟牌 ──

// pickFollow 挑一手跟上（上面「跟牌」那五条）。
func pickFollow(v View, cands []rules.Combo, cfg Config) Decision {
	oppLow := v.OpponentAboutToWin(cfg.PressOpponentLeft)

	// ② 队友领先、而且没人快走完：让给他（留着牌）。
	// ④ 例外：**绝不用炸弹压队友** —— 所以队友这一手直接让，不去看候选。
	if cfg.YieldToPartner && v.LastFromPartner() && !oppLow {
		return Decision{Pass: true}
	}

	// ③b 先把「拆成对子/单张」的候选去掉（还有别的选择就不这么打）。
	//
	// ⚠️ 这两道的**顺序**要紧：先去掉拆炸弹的、再看要不要动炸弹。反过来的话，
	// 「只有拆炸弹才压得住」会先被选出来，而它旁边可能正好摆着一个炸弹（那才是不拆的选择）
	// —— 结果就是「有别的选择还是拆了」（整局统计口径下就是主动拆炸弹）。
	pool := cands
	if keep := notSplittingBombs(v, cands); len(keep) > 0 {
		pool = keep
	}
	// ④ **绝不用炸弹压队友** —— 这条是硬性的，和「该不该炸」无关：队友出的那一手，
	// 候选里先把炸弹去掉，去掉之后没得选就过牌。（③ 的「对手快走完就不让」不盖过它。）
	if v.LastFromPartner() {
		pool = nonBombs(pool)
		if len(pool) == 0 {
			return Decision{Pass: true}
		}
	}

	// ③ 平时先看不用炸弹的；对手快走完 / 自己残局时才允许动炸弹。
	if !oppLow && len(v.Hand) > cfg.BombWhenOwnLeft {
		nonBomb := nonBombs(pool)
		if len(nonBomb) == 0 {
			return Decision{Pass: true} // 只剩炸弹能压，但还不到该炸的时候
		}
		pool = nonBomb
	}
	return Decision{Combo: minCombo(v, pool)} // ① 用最小的能压住
}

// minCombo 挑「最小的一手」：先比牌力、再比张数（同牌力时张数少的省牌）。
func minCombo(v View, cands []rules.Combo) rules.Combo {
	best := cands[0]
	for _, c := range cands[1:] {
		cp, bp := c.Rank.Power(v.Level), best.Rank.Power(v.Level)
		if cp < bp || (cp == bp && len(c.Cards) < len(best.Cards)) {
			best = c
		}
	}
	return best
}

// OpponentAboutToWin 判断有没有对手快走完了（剩 count 张以内）。
func (v View) OpponentAboutToWin(count int) bool {
	for _, s := range v.Opponents() {
		if v.Counts[s] > 0 && v.Counts[s] <= count {
			return true
		}
	}
	return false
}

// ── 牌型的判断（都用 rules 的公开类型，不另立一套）──

// isBomb 是「这一手算不算炸弹」：炸弹、同花顺、四大天王都算（第三条 8–10）。
func isBomb(c rules.Combo) bool {
	switch c.Type {
	case rules.TypeBomb, rules.TypeStraightFlush, rules.TypeJokerBomb:
		return true
	}
	return false
}

// nonBombs 去掉炸弹，保留其余候选。
func nonBombs(cands []rules.Combo) []rules.Combo {
	out := make([]rules.Combo, 0, len(cands))
	for _, c := range cands {
		if !isBomb(c) {
			out = append(out, c)
		}
	}
	return out
}

// notSplittingBombs 去掉「拆炸弹」的候选（手里某种点数有 4 张以上，这一手却只用掉一部分）。
func notSplittingBombs(v View, cands []rules.Combo) []rules.Combo {
	out := make([]rules.Combo, 0, len(cands))
	for _, c := range cands {
		if !splitsBombIntoSmall(v.Hand, v.Level, c) {
			out = append(out, c)
		}
	}
	return out
}

// splitsBombIntoSmall 判断这一手是不是「把一手完整的炸弹拆成对子/单张出」：
// 手里某种点数有 4 张及以上（**不含逢人配** —— 逢人配凑出来的不算手里原有的炸弹），
// 而这一手是**对子或单张**、只用了其中一部分。
//
// 为什么限定对子/单张（设计里那句原话就是「不主动把它拆成对子/单张出」）：那是纯粹的浪费 ——
// 同样能打的话，换个对子就行。用炸弹里的一张凑一手三带对/三连对**不算**：那是一手正经牌型，
// 而它的替代品往往就是「花掉一个炸弹」，反而更亏。
//
// 出光（把炸弹当炸弹打出去）当然也不算拆。
func splitsBombIntoSmall(hand []rules.Card, level rules.Rank, c rules.Combo) bool {
	if c.Type != rules.TypePair && c.Type != rules.TypeSingle {
		return false
	}
	inHand := map[rules.Rank]int{}
	for _, card := range hand {
		if !card.IsWild(level) {
			inHand[card.Rank]++
		}
	}
	used := map[rules.Rank]int{}
	for _, card := range c.Cards {
		if !card.IsWild(level) {
			used[card.Rank]++
		}
	}
	for rank, n := range inHand {
		left := n - used[rank]
		// 手里本来就有 4 张以上（能凑炸弹），这一手又把它拆成「剩 1–3 张」—— 剩下的凑不成炸弹了。
		// 出光（left == 0）不算拆；两副牌同点数最多 8 张，剩 ≥4 张也还留着炸弹，同样不算拆。
		if n >= 4 && used[rank] > 0 && left > 0 && left < 4 {
			return true
		}
	}
	return false
}

// isLone 判断这张牌在手里是不是孤张：同点数（不含逢人配）就它一张。
func isLone(hand []rules.Card, level rules.Rank, card rules.Card) bool {
	if card.IsWild(level) {
		return false
	}
	n := 0
	for _, c := range hand {
		if !c.IsWild(level) && c.Rank == card.Rank {
			n++
		}
	}
	return n == 1
}
