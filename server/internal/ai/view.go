// Package ai 是第一版的规则型对手：给一个局面（`View`）和一组参数，挑一手牌打出去。
//
// 它**只消费** server/internal/rules —— 出法全部来自 `rules.Moves`（合法出法生成），
// 自己只负责「从候选里挑一个」。裁决、进贡、升级都不在这里（那些是引擎和练习局的事，见 #204 / #88）。
//
// # AI 看不见别家的手牌
//
// 这是这个包最重要的一条约定：`View` 里只有**自己的手牌 + 各家的张数**。
// `rules.Game.Hand(seat)` 谁的手牌都给得出来，所以调用方**不要**想着「直接把 `*Game` 塞进去省事」
// —— 那样 AI 就成了看得见所有牌的作弊者，而且从外面看不出来：照样能跑、照样能过测试，
// 只是它每一手都打得「恰好」正确。`view_test.go` 里有一条用反射钉住这件事（结构体里不许出现
// 手牌形状的字段）。
package ai

import "github.com/dayuer/jkt-guandan/server/internal/rules"

// View 是 AI 做决定时能看到的全部东西。
//
// **有意没有**别家的手牌，也没有整副牌堆、没有别人的出牌历史 —— 只有牌桌上人人看得见的那些。
type View struct {
	// Level 是这一副打几。
	Level rules.Rank
	// Seat 是 AI 自己坐几号座（1–4）。
	Seat int
	// Hand 是 AI 自己的手牌。
	Hand []rules.Card
	// Counts 下标是座位号（1–4），0 不用：各家**还剩几张**（含自己）。
	Counts [rules.Seats + 1]int
	// Done 是已经出完的座位，按先后。
	Done []int
	// Leading 为真表示这一圈还没人出过牌（自己是领出，什么牌型都能出）。
	Leading bool
	// LastSeat 是这一圈最近出牌的人（Leading 时为 0），LastCombo 是他出的那一手。
	LastSeat  int
	LastCombo rules.Combo
}

// Partner 是自己的搭档：1、3 一方，2、4 一方（和 scoring、rules 同一个口径）。
func (v View) Partner() int { return rules.PartnerSeat(v.Seat) }

// ViewOf 把一局牌收成 seat 这个座位**能看到的** View。
//
// 它是 `View` 那条红线的唯一生产实现：只读自己的手牌、各家**张数**和公开信息
// （轮次、这一圈的领出/上一手），别家的手牌一律不进 View。练习局和任何未来的调用方都走它，
// 不要自己拿 `*Game` 拼 —— 那会把「AI 看不见别家牌」悄悄变成一句口号。
func ViewOf(g *rules.Game, seat int) View {
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

// Opponents 是对方两家。
func (v View) Opponents() []int {
	out := make([]int, 0, rules.Seats-2)
	for s := 1; s <= rules.Seats; s++ {
		if s != v.Seat && s != v.Partner() {
			out = append(out, s)
		}
	}
	return out
}

// LastFromPartner 判断这一圈最近出的那一手是不是队友出的。
func (v View) LastFromPartner() bool { return !v.Leading && v.LastSeat == v.Partner() }
