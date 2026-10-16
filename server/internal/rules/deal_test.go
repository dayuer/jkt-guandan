package rules

import "testing"

// 没有随机源、随机源给的排列不对：报错，而不是发一副坏牌。
// 复盘时牌对不上要立刻知道（#204：「随机源注入」）。
func TestDealRejectsBadShuffler(t *testing.T) {
	cases := []struct {
		name string
		s    Shuffler
	}{
		{"没有随机源", nil},
		{"返回空", func(int) []int { return nil }},
		{"少了几张", func(n int) []int { return make([]int, n-1) }},
		{"多给了几张", func(n int) []int { return make([]int, n+1) }},
		{"位置越界", func(n int) []int {
			p := identity(n)
			p[0] = n
			return p
		}},
		{"负数位置", func(n int) []int {
			p := identity(n)
			p[0] = -1
			return p
		}},
		{"同一张牌给了两次", func(n int) []int {
			p := identity(n)
			p[0], p[1] = p[1], p[0]
			p[1] = p[0]
			return p
		}},
	}
	for _, tc := range cases {
		if _, err := Deal(tc.s); err == nil {
			t.Errorf("%s：应当报错", tc.name)
		}
	}
}

func identity(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return p
}

// 发到的牌按顺序轮流给四个座位：不洗牌时 1 号座拿的是整副牌的第 0、4、8…张。
func TestDealRoundRobin(t *testing.T) {
	deck := NewDeck()
	hands, err := Deal(PermShuffler(identity(len(deck))))
	if err != nil {
		t.Fatal(err)
	}
	for seat := 0; seat < 4; seat++ {
		for j, c := range hands[seat] {
			want := deck[j*4+seat]
			if c.ID != want.ID {
				t.Fatalf("%d 号座第 %d 张是 %s，应该是 %s", seat+1, j+1, c.Key(), want.Key())
			}
		}
	}
}

// PermShuffler 不改调用方的切片，也不让 Deal 改到它（同一个排列可以反复发牌）。
func TestPermShufflerDoesNotMutate(t *testing.T) {
	perm := identity(108)
	perm[0], perm[1] = perm[1], perm[0]
	before := append([]int{}, perm...)

	first, err := Deal(PermShuffler(perm))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Deal(PermShuffler(perm))
	if err != nil {
		t.Fatal(err)
	}
	for seat := range first {
		for i := range first[seat] {
			if first[seat][i].ID != second[seat][i].ID {
				t.Fatalf("同一个排列发了两次不同的牌：%d 号座第 %d 张", seat+1, i+1)
			}
		}
	}
	for i := range perm {
		if perm[i] != before[i] {
			t.Fatal("排列被改了")
		}
	}
}
