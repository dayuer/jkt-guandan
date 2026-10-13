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

// Message 是一条实时消息里除了 topic 之外的部分。
//
// 线格式是 {"topic":"game:8K2","type":"seat.taken","v":7,"data":{…}}（§5.5），
// topic 由 Hub 按订阅关系补上，所以这里不带。
type Message struct {
	Type string `json:"type"`
	V    int64  `json:"v"`
	Data any    `json:"data,omitempty"`
}

// Publisher 是实时事件的出口。
//
// #18 的 Hub（internal/realtime）合并之前，这里挂的是 NopPublisher；
// 两边都合进 main 之后，后合并的一方负责把 realtime.Hub 接到这个接口上。
type Publisher interface {
	Publish(topic string, msg Message)
}

// NopPublisher 什么也不做。
type NopPublisher struct{}

func (NopPublisher) Publish(string, Message) {}

// GameTopic 是某一局的订阅主题。
func GameTopic(code string) string { return "game:" + code }

// FeedTopic 是某个片区的首页订阅主题。
func FeedTopic(area string) string { return "feed:" + area }
