package rules

import "fmt"

// Shuffler 是外部注入的随机源：给定 n，返回 0 到 n-1 的一个排列，每个数正好出现一次。
//
// 引擎自己不掷骰子：随机从外面来，测试才能复现，复盘和断线恢复才有意义（#204 的
// 「随机源注入」）。生产上用一个真随机源，牌例和回归测试用一个写死的排列。
type Shuffler func(n int) []int

// PermShuffler 把一个现成的顺序当成随机源：第 i 张发出去的是 deck[perm[i]]。
func PermShuffler(perm []int) Shuffler {
	return func(n int) []int {
		if len(perm) != n {
			return nil // 交给 Deal 报「随机源给的排列不对」
		}
		out := make([]int, n)
		copy(out, perm)
		return out
	}
}

// handsEach 是每人几张：两副牌 108 张，四个人平分（第二条 6：每名运动员 27 张）。
const handsEach = 27

// Deal 洗牌、发牌：用 shuffle 排出 108 张的出牌顺序，按顺序给 1–4 号座每人 27 张。
//
// 返回值下标 0–3 对应 1–4 号座；手里的牌按发到的先后排，不排序（排序是界面的事，
// 引擎按 ID 认牌）。随机源不合法（长度为 0、不是排列、有重复或越界）时返回错误，
// 而不是悄悄发一副坏牌 —— 复盘时对不上要立刻知道。
func Deal(shuffle Shuffler) ([4][]Card, error) {
	if shuffle == nil {
		return [4][]Card{}, fmt.Errorf("发牌：没有随机源")
	}
	deck := NewDeck()
	perm := shuffle(len(deck))
	if err := checkPerm(perm, len(deck)); err != nil {
		return [4][]Card{}, fmt.Errorf("发牌：%w", err)
	}

	var hands [4][]Card
	for i := range hands {
		hands[i] = make([]Card, 0, handsEach)
	}
	// 每人每次一张，逆时针轮流抓，发完为止（第七条一，5）。
	for i, idx := range perm {
		seat := i % 4
		hands[seat] = append(hands[seat], deck[idx])
	}
	return hands, nil
}

// checkPerm 确认 perm 是 0..n-1 的一个排列。
func checkPerm(perm []int, n int) error {
	if len(perm) != n {
		return fmt.Errorf("随机源给了 %d 个位置，应该有 %d 个", len(perm), n)
	}
	seen := make([]bool, n)
	for _, idx := range perm {
		if idx < 0 || idx >= n {
			return fmt.Errorf("随机源给的位置 %d 超出 0–%d", idx, n-1)
		}
		if seen[idx] {
			return fmt.Errorf("随机源把第 %d 张牌给了两次", idx)
		}
		seen[idx] = true
	}
	return nil
}
