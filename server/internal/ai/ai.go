// 第一版 AI 的策略：**规则型**，给 `rules.Moves` 的候选打分再挑一个。
//
// 目标是「像样」不是「强」（#88 的「不做」：不做搜索、不做训练模型、不做残局穷举）：
// 不犯蠢、不拆炸弹、知道配合队友，就够了。策略条文写在设计（#88 的评论）里，这里逐条对着实现：
//
//	领出：① 能一次走完就走完；② 优先张数多的牌型；④ 牌不多了先出小的。
//	      （③ 不主动拆炸弹、⑤ 孤张先出这两条**不用单独做** —— 领出时它们改不了结果，
//	      论证和实测见 pickLead 上面的注释。）
//	跟牌：① 能一次走完就走完，否则用最小的能压住；② 队友领先、**且他那一手不小**才让牌；
//	      ③ 对手快走完就不让、该炸就炸；④ 炸弹的时机（平时不炸，残局/对手要走才炸；
//	      **绝不用炸弹压队友**）；⑤ 要不起就过。
//
// **两条相撞时的次序（#259 复审定）**：「能一次走完」压过「绝不用炸弹压队友」——
// 头游最重要，队友领先时自己手里正好能走光，就走（哪怕那一手是炸弹、哪怕会"压"到队友）。
// 所以 ① 放在让牌和炸弹过滤**之前**；整局统计「用炸弹压队友」时也**排除这一手走完的情况**。
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
	// YieldToPartner：队友领先时让牌（过牌）。
	YieldToPartner bool
	// YieldMinPower：队友那一手的牌力**到这个数（含）才算「不小」**，才值得让 ——
	// 比它小的一手，对手随手就能压过去，让牌等于把这一圈白送，不如自己接过来。
	// 只对非炸弹的牌型看这个数：炸弹（含同花顺、四大天王）一律算不小。
	// 按**当前级数下的大小**算（打 7 时 7 是级牌，算大的）。默认 10（十以上，含十）。
	YieldMinPower int
	// PressOpponentLeft：对手剩这么多张（含）以内，就不再让、能压就压。
	PressOpponentLeft int
	// BombWhenOwnLeft：自己剩这么多张（含）以内，该用炸弹就用（「该炸的时机」的一种）。
	BombWhenOwnLeft int
}

// DefaultConfig 是第一版（也是唯一一档）的参数。
func DefaultConfig() Config {
	return Config{YieldToPartner: true, YieldMinPower: 10, PressOpponentLeft: 2, BombWhenOwnLeft: 6}
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

// pickLead 挑一手领出（上面「领出」那三条）。打分取最高的；同分保留先出现的那个（**确定**）。
//
// 写在最前面的一条经验（#259 复审踩过）：**「没有用例能区分」不等于「冗余」**。
// 判一条策略是不是真冗余，要看「有没有**任何输入**会给出不同的答案」，不是看测试红不红 ——
// 当时把 ① 删掉（全绿）就当成冗余了，其实只是缺用例。下面每条不做的策略都做了差分验算：
// 2 万个随机局面，逐个比较「有这条策略 / 没有」的输出，看有没有一个输入会变。
func pickLead(v View, cands []rules.Combo) rules.Combo {
	// ① 能一次走完就走完：**直接走完，不再比别的** —— 走完这一副对本方有利，
	// 「炸弹留着」「大牌留后手」都排在这条后面。
	//
	// ⚠️ 这条**不能**只用打分表达（打分会跟别的项比大小）：审计 #259 给的三个反例 ——
	// 手里正好 4 张 K 时，炸弹扣 30000 分压过「张数多」→ 出三张 K；6 张 K 出三张 K；
	// 四大天王出一对小王。「走完那一手张数最多，② 就能挑出来」这个说法**只在不扣分时成立**。
	if c, ok := finishInOneGo(v, cands); ok {
		return c
	}
	// ③ 不主动拆炸弹：领出这条**不用单独做**，所以这里没有过滤。手里某种点数有 4 张以上时，
	// 一定同时有「三张」这个候选，而三张（3×1000）永远压过对子（≤2×1000+500）和单张
	// （≤1000+500），所以拆成对子/单张从来不会被选中。原来这里有一道 `notSplittingBombs`
	// 过滤，差分验算（2 万个局面）的结果是**一个输入都不会变**，所以删掉。
	// （跟牌那边不能这么推：压得住的候选可能只剩拆炸弹一种，所以那边的过滤留着。）
	//
	// ⑤ 单张先出孤张：这条**撤了**（保留撤销）。理由**不是**「它从不改变结果」—— 差分验算
	// （2 万个随机局面）没找到差异，但那只是抽样，不是证明，审计 #259 直接给了个反例：
	// 打 7、手里两张 ♥7（逢人配）+ 一张小王时，`isLone` 里**逢人配不算孤张**，
	// 于是 +500 落到小王头上 —— AI 会先甩王、把逢人配留着。方向是反的：王是最不该先出的单张。
	// 所以是不做这条，而不是「做了也没差别」。
	best, bestScore := cands[0], 0
	for i, c := range cands {
		score := 0
		score += len(c.Cards) * 1_000 // ② 张数多优先
		if isBomb(c) {
			score -= 30_000 // 领出时也别急着丢炸弹
		}
		if len(v.Hand) <= 6 {
			// ④ 牌不多了：先出小的（按当前级数下的大小 —— 打 J 时的 J 是级牌，比 K 大，先出 K），
			// 大的留作后手。
			score -= int(c.Rank.Power(v.Level)) * 10
		}
		if i == 0 || score > bestScore {
			best, bestScore = c, score
		}
	}
	return best
}

// finishInOneGo 找「一手把整手牌出光」的候选（领出、跟牌共用）。
//
// 有就返回它 —— 走完这一副对本方最有利，别的策略（留炸弹、留大牌、让牌给队友）都排在这条后面。
func finishInOneGo(v View, cands []rules.Combo) (rules.Combo, bool) {
	for _, c := range cands {
		if len(c.Cards) == len(v.Hand) {
			return c, true
		}
	}
	return rules.Combo{}, false
}

// ── 跟牌 ──

// pickFollow 挑一手跟上（上面「跟牌」那五条）。
func pickFollow(v View, cands []rules.Combo, cfg Config) Decision {
	// ① 能一次走完就走完 —— 这条**压过让牌**：走完这一副对本方最有利，哪怕队友正领先着。
	// 放在让牌前面，否则「队友领先」会把自家走完的机会让掉。
	if c, ok := finishInOneGo(v, cands); ok {
		return Decision{Combo: c}
	}

	oppLow := v.OpponentAboutToWin(cfg.PressOpponentLeft)

	// ② 队友领先、他那一手不小、而且没人快走完：让给他（留着牌）。
	// ④ 例外：**绝不用炸弹压队友** —— 所以队友这一手直接让，不去看候选。
	//
	// 「不小」这一条是审计 #259 定的口径：队友出一手小牌，对手随手就能压过去，让牌等于把这一圈
	// 白送；不如自己接过来。阈值见 Config.YieldMinPower（炸弹一律算不小）。
	if cfg.YieldToPartner && v.LastFromPartner() && !oppLow && playIsBig(v.LastCombo, v.Level, cfg) {
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
	return Decision{Combo: minCombo(pool)} // ① 用最小的能压住
}

// minCombo 挑「最小的一手压住」。
//
// 大小**用引擎的 `Combo.Compare`（第四条 二）**，不自己按点数排 —— 这两个不是一回事：
//
//   - 炸弹压任何普通牌型（所以「先在非炸弹里挑、非炸弹压不住才动炸弹」是**自动**成立的：
//     炸弹比任何普通牌型都大，取最小自然先取普通牌型）；
//   - 炸弹之间也不按点数：先比档次（四大天王 ＞ 超过五张 ＞ 同花顺 ＞ 五张 ＞ 四张），
//     同档再比张数、牌点 —— 所以 5 张 3 的炸弹**大于** 4 张 A 的炸弹。
//
// 原来这里按 `Rank.Power` 比，两个坑都踩了（#259 复审 B1 的实测）：手里 3333 + AA、对手出对 K，
// 它会甩 3 炸（点数 3 最小）；手里 5 张 3 + 4 张 K 的炸弹，它会先挑 5 张 3（张数多的反而"小"）。
func minCombo(cands []rules.Combo) rules.Combo {
	best := cands[0]
	for _, c := range cands[1:] {
		// 同一种上一手的所有候选，要么同牌型、要么是炸弹，`Compare` 都给得出大小；
		// 真碰上 Incomparable 就保留先出现的那个（确定）。
		if c.Compare(best) == rules.Less {
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

// playIsBig 判断上一手「算不算不小」：炸弹（含同花顺、四大天王）一律算不小；
// 其余按牌力，到 Config.YieldMinPower 才算不小（**按当前级数算大小** —— 打 3 时的 3 是级牌，
// 算大的；不是按原始点数）。
//
// 「炸弹一律算不小」这一支**眼下改不了行为**：队友出炸弹时，下面那条「绝不用炸弹压队友」会把
// 候选里的炸弹全去掉、最后过牌，让不让都是过。留着是因为它是这条规则的**真实含义**
// （炸弹当然不小），省得以后有人改了「队友出的炸弹怎么办」那条时，这里变成一个安静的例外。
func playIsBig(c rules.Combo, level rules.Rank, cfg Config) bool {
	if isBomb(c) {
		return true
	}
	return int(c.Rank.Power(level)) >= cfg.YieldMinPower
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
