package games

// 实时事件（§5.5）。REST 始终是事实来源，这些消息只是「告诉你哪里变了」，
// 客户端断线重连后一律重新拉 REST。
const (
	EventSeatTaken   = "seat.taken"
	EventSeatLeft    = "seat.left"
	EventGameFull    = "game.full"
	EventGameUpdated = "game.updated"
	EventFeedChanged = "feed.game_changed"
	EventFeedAdded   = "feed.game_added"
)

// Event 是推给客户端的一条消息，字段和 realtime.Event 一一对应（topic/type/v/data）。
// V 是牌局版本号，客户端丢弃不比本地新的消息，所以乱序和重复都不会出错。
type Event struct {
	Topic string `json:"topic"`
	Type  string `json:"type"`
	V     int64  `json:"v,omitempty"`
	Data  any    `json:"data,omitempty"`
}

// Publisher 是实时事件的出口。
//
// #40 的 Hub（internal/realtime）合并之前挂 NopPublisher。合并之后由 main 接上：
// 业务包不依赖 realtime，所以 main 里放一个几行的适配器，把 games.Event 转成
// realtime.Event 再交给 Hub.Publish。
type Publisher interface {
	Publish(ev Event)
}

// NopPublisher 什么也不做。
type NopPublisher struct{}

func (NopPublisher) Publish(Event) {}

// GameTopic 是某一局的订阅主题。
func GameTopic(code string) string { return "game:" + code }

// FeedTopic 是某个片区的首页订阅主题。
func FeedTopic(area string) string { return "feed:" + area }
