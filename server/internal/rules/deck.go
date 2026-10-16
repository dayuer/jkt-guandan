// Package rules 是掼蛋规则引擎（#204）：牌、发牌、牌点大小、牌型识别与比较、合法出法、
// 出牌流程、进贡还贡抗贡。练习局由服务端主持整局（#88），裁决以这里为准；客户端的 TS 版
// （#87，app/rules/）只用来在本地给选牌提示，两套实现读同一批牌例。
//
// 规则口径按 2022《竞技掼蛋竞赛规则（试行）》（国家体育总局棋牌运动管理中心审定，2022-09-08），
// 条款号写在每条规则旁边。牌例和格式见仓库根 testdata/rules/README.md。
//
// 纯逻辑：不读数据库、不看时间、不用全局随机数；随机源从外部注入（见 Shuffler），
// 状态可以序列化成 JSON（断线恢复、复盘）。
package rules

import (
	"fmt"
	"strconv"
	"strings"
)

// Rank 是牌点。2–10 就是数字，J=11、Q=12、K=13、A=14；大小王排在 A 之后（15、16）。
//
// A 在搭配成三连对、三同连张、顺子、同花顺时可视作 1（第四条 一、2），那是牌型识别里的事，
// 不是这里的数值。
type Rank int

const (
	Two   Rank = 2
	Three Rank = 3
	Four  Rank = 4
	Five  Rank = 5
	Six   Rank = 6
	Seven Rank = 7
	Eight Rank = 8
	Nine  Rank = 9
	Ten   Rank = 10
	Jack  Rank = 11
	Queen Rank = 12
	King  Rank = 13
	Ace   Rank = 14
	// SmallJoker、BigJoker 是小王、大王。
	SmallJoker Rank = 15
	BigJoker   Rank = 16
)

// IsJoker 判断是不是王。红桃级牌不能顶替王（第二条 14）。
func (r Rank) IsJoker() bool { return r >= SmallJoker }

// RankName 把牌点写成牌面：11 → J、15 → 小王。
func (r Rank) RankName() string {
	switch r {
	case Jack:
		return "J"
	case Queen:
		return "Q"
	case King:
		return "K"
	case Ace:
		return "A"
	case SmallJoker:
		return "小王"
	case BigJoker:
		return "大王"
	}
	return strconv.Itoa(int(r))
}

// ParseRank 认牌面：2–10、J、Q、K、A、小王、大王。
//
// **只认规范写法**（#213 复审）：字母大写（`J` 不认成 `j`）、数字不带前导零或正号
// （`5` 认，`05`、`+5` 不认）。牌例是两端逐字符串比对的，Go 端这里松一点，
// 严格比对的 TS 端就会挂 —— 和 ParseKey 一个口径。
func ParseRank(s string) (Rank, error) {
	switch s {
	case "J":
		return Jack, nil
	case "Q":
		return Queen, nil
	case "K":
		return King, nil
	case "A":
		return Ace, nil
	case "10":
		return Ten, nil
	case "小王":
		return SmallJoker, nil
	case "大王":
		return BigJoker, nil
	}
	// 2–9 就一个字节，别顺手把 "05"、"+5" 这种也放进来。
	if len(s) == 1 && s[0] >= '2' && s[0] <= '9' {
		return Rank(s[0] - '0'), nil
	}
	return 0, fmt.Errorf("认不出牌点 %q：只认 2–10、J、Q、K、A、小王、大王（大写，数字不带前导零）", s)
}

// Suit 是花色。
type Suit int

const (
	Spade   Suit = iota // ♠
	Heart               // ♥
	Club                // ♣
	Diamond             // ♦
	NoSuit              // 王没有花色
)

// SuitName 把花色写成符号；王是空串。
func (s Suit) SuitName() string {
	switch s {
	case Spade:
		return "♠"
	case Heart:
		return "♥"
	case Club:
		return "♣"
	case Diamond:
		return "♦"
	}
	return ""
}

// ParseSuit 认花色符号（"♠"、"♥"、"♣"、"♦"；空串表示没有花色，也就是王）。
// 符号后面不许跟变体选择符 U+FE0F（有些编辑器会把 ♥ 存成 ♥️），见 ParseKey。
func ParseSuit(s string) (Suit, error) {
	switch s {
	case "♠":
		return Spade, nil
	case "♥":
		return Heart, nil
	case "♣":
		return Club, nil
	case "♦":
		return Diamond, nil
	case "":
		return NoSuit, nil
	}
	return 0, fmt.Errorf("认不出花色 %q", s)
}

// Card 是一张牌。两副牌一共 108 张，每张都不一样：ID 是它的身份证，记牌、出牌记录、
// 断线恢复都靠它区分「就是那一张」。同一个 ID 永远是同一张牌（见 NewDeck）。
type Card struct {
	ID   int
	Rank Rank
	Suit Suit
	Deck int // 0 或 1：两副牌里的第几副
}

// Key 是牌在牌例里的写法：「花色 + 牌面 / 副号」，比如 ♠7/0、♥10/1、大王/1。
// 王没有花色，写成 小王/0、大王/1。
func (c Card) Key() string {
	return fmt.Sprintf("%s%s/%d", c.Suit.SuitName(), c.Rank.RankName(), c.Deck)
}

func (c Card) String() string { return c.Key() }

// ParseKey 读回 Key 写出来的那张牌，带上它在整副牌里的 ID（按 NewDeck 的顺序查）。
// 「哪一张牌」在 Key 里就定死了（花色 + 牌面 + 副号），所以 ID 是查得出来的。
//
// **只认规范写法**（#213 复审）：读出来再写回去必须还是原来那串（`ParseKey(s).Key() == s`）。
// 也就是说 `♠j/0`、`♠07/0`、`♥️7/0`（多一个变体选择符 U+FE0F）这些都拒绝 ——
// 牌例是两端逐字符串比对的，Go 端这里松一点，到了严格比对的 TS 端就会挂。
func ParseKey(s string) (Card, error) {
	c, err := parseKeyBody(s)
	if err != nil {
		return Card{}, err
	}
	id, ok := canonicalIndex[c.Key()]
	if !ok {
		return Card{}, fmt.Errorf("认不出牌 %q：整副牌里没有这一张", s)
	}
	c.ID = id
	return c, nil
}

// canonicalIndex 是 Key → ID 的表，按 NewDeck 的顺序建一次（108 张，不用加锁）。
var canonicalIndex = func() map[string]int {
	index := make(map[string]int, 108)
	for _, c := range NewDeck() {
		index[c.Key()] = c.ID
	}
	return index
}()

func parseKeyBody(s string) (Card, error) {
	i := strings.LastIndex(s, "/")
	if i < 0 {
		return Card{}, fmt.Errorf("认不出牌 %q：要写成「花色+牌面/副号」，比如 ♠7/0、大王/1", s)
	}
	deck, err := strconv.Atoi(s[i+1:])
	if err != nil || (deck != 0 && deck != 1) {
		return Card{}, fmt.Errorf("认不出牌 %q：副号只能是 0 或 1", s)
	}
	body := s[:i]
	if body == "" {
		return Card{}, fmt.Errorf("认不出牌 %q：没写牌面", s)
	}
	// 王写成 小王/0、大王/1，没有花色；其余的牌第一格是花色符号。
	suit := NoSuit
	rest := body
	if !strings.HasPrefix(body, "小王") && !strings.HasPrefix(body, "大王") {
		got, err := ParseSuit(string([]rune(body)[0]))
		if err != nil {
			return Card{}, fmt.Errorf("认不出牌 %q：%v", s, err)
		}
		suit, rest = got, string([]rune(body)[1:])
	}
	rank, err := ParseRank(rest)
	if err != nil {
		return Card{}, fmt.Errorf("认不出牌 %q：%v", s, err)
	}
	if rank.IsJoker() != (suit == NoSuit) {
		return Card{}, fmt.Errorf("认不出牌 %q：王没有花色，别的牌要有花色", s)
	}
	c := Card{Rank: rank, Suit: suit, Deck: deck}
	// 只认规范写法：牌面大写、副号不补零、花色不带变体选择符（见函数注释）。
	if got := c.Key(); got != s {
		return Card{}, fmt.Errorf("认不出牌 %q：要写成 %s", s, got)
	}
	return c, nil
}

// NewDeck 造出两副牌共 108 张，顺序固定：先第 0 副、再第 1 副；每副里花色按 ♠♥♣♦，
// 花色内按 2、3……10、J、Q、K、A，最后小王、大王。切片下标就是 Card.ID。
//
// 这个顺序被 testdata/rules/deck.json 锁着：两端（#87 的 TS 版和这里的 Go 版）必须一样，
// 否则同一串牌例在两边指的就不是同一张牌。
func NewDeck() []Card {
	deck := make([]Card, 0, 108)
	for d := 0; d < 2; d++ {
		for _, s := range []Suit{Spade, Heart, Club, Diamond} {
			for r := Two; r <= Ace; r++ {
				deck = append(deck, Card{Rank: r, Suit: s, Deck: d})
			}
		}
		deck = append(deck,
			Card{Rank: SmallJoker, Suit: NoSuit, Deck: d},
			Card{Rank: BigJoker, Suit: NoSuit, Deck: d})
	}
	// ID 统一在最后按位置写：两张牌一起 append 时 len(deck) 还是同一个数。
	for i := range deck {
		deck[i].ID = i
	}
	return deck
}
