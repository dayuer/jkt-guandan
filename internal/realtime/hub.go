package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// 协议上的各项上限（§5.5、§12.3）。
const (
	SendQueueSize      = 32
	MaxTopicsPerConn   = 20
	MaxMessageBytes    = 4 << 10
	ClientMsgPerSecond = 10
	MaxConnsPerIP      = 20
	NewConnsPerSecond  = 5 // 每个 IP
	// MaxConns 是全进程的连接上限：/ws 不占 256 的并发名额（长连接会把名额一直占着），
	// 改由它兜住内存。
	MaxConns = 5000

	DefaultPingInterval = 25 * time.Second
	DefaultPongTimeout  = 10 * time.Second
	writeTimeout        = 10 * time.Second
)

// 关闭码。
const (
	// StatusServiceRestart 是 §12.4 关闭时发的 1012，客户端随后重连。
	StatusServiceRestart = websocket.StatusServiceRestart
	// StatusSlowConsumer 表示发送队列满了被断开；客户端重连后重新拉 REST。
	StatusSlowConsumer websocket.StatusCode = 4000
)

// Event 是服务端推给客户端的一条消息（§5.5）。V 是牌局版本号，客户端丢弃不比本地新的消息；
// 没有版本概念的事件（如 claim.redeemed）V 为 0。
type Event struct {
	Topic string `json:"topic"`
	Type  string `json:"type"`
	V     int64  `json:"v,omitempty"`
	Data  any    `json:"data,omitempty"`
}

var (
	gameTopic = regexp.MustCompile(`^game:[0-9A-Za-z]{1,16}$`)
	feedTopic = regexp.MustCompile(`^feed:[0-9A-Za-z ._-]{1,32}$`)
	userTopic = regexp.MustCompile(`^user:[0-9A-Za-z_-]{1,64}$`)
)

// UserTopic 是某个用户的私有主题名。
func UserTopic(userID string) string { return "user:" + userID }

// errForbiddenTopic 和 errBadTopic 回给客户端，不断开连接。
var (
	errBadTopic       = errors.New("bad_topic")
	errForbiddenTopic = errors.New("forbidden_topic")
	errTooManyTopics  = errors.New("too_many_topics")
)

// checkTopic 判断 userID（空表示没有身份）能不能订阅 topic。
func checkTopic(topic, userID string) error {
	switch {
	case gameTopic.MatchString(topic), feedTopic.MatchString(topic):
		return nil
	case userTopic.MatchString(topic):
		if userID == "" || topic != UserTopic(userID) {
			return errForbiddenTopic
		}
		return nil
	default:
		return errBadTopic
	}
}

// Hub 是进程内的订阅表。零值不可用，用 NewHub。
type Hub struct {
	mu      sync.Mutex
	topics  map[string]map[*conn]struct{}
	conns   map[*conn]struct{}
	closing bool
	wg      sync.WaitGroup // 每个连接的读写 goroutine
}

func NewHub() *Hub {
	return &Hub{topics: make(map[string]map[*conn]struct{}), conns: make(map[*conn]struct{})}
}

// Publish 把事件发给订阅了 ev.Topic 的所有连接。只在写事务提交后调用（store.Tx.AfterCommit）。
// 序列化一次；加锁时只往各连接的发送队列里放，队列满的连接被断开，不在锁里做网络写。
func (h *Hub) Publish(ev Event) {
	msg, err := json.Marshal(ev)
	if err != nil {
		slog.Error("实时事件序列化失败", "topic", ev.Topic, "type", ev.Type, "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.topics[ev.Topic] {
		select {
		case c.send <- msg:
		default:
			c.slow()
		}
	}
}

// Subscribers 返回某个主题当前的订阅连接数（测试和 presence 用）。
func (h *Hub) Subscribers(topic string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.topics[topic])
}

// Conns 返回当前连接数（/metrics 用）。
func (h *Hub) Conns() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// Closing 说明 Hub 是否已经开始关闭（/readyz 用：§12.4 第 1 步，关闭时立刻返回 503）。
func (h *Hub) Closing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closing
}

// register 登记新连接；Hub 正在关闭或全局连接数已满时返回 false。
func (h *Hub) register(c *conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing || len(h.conns) >= MaxConns {
		return false
	}
	h.conns[c] = struct{}{}
	h.wg.Add(1)
	return true
}

// unregister 把连接从所有主题里删掉。可以重复调用。
func (h *Hub) unregister(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.conns[c]; !ok {
		return
	}
	for t := range c.topics {
		h.removeLocked(t, c)
	}
	delete(h.conns, c)
	h.wg.Done()
}

func (h *Hub) subscribe(c *conn, topics []string) (ok []string, rejected map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rejected = map[string]string{}
	for _, t := range topics {
		if _, already := c.topics[t]; already {
			ok = append(ok, t)
			continue
		}
		if err := checkTopic(t, c.userID); err != nil {
			rejected[t] = err.Error()
			continue
		}
		if len(c.topics) >= MaxTopicsPerConn {
			rejected[t] = errTooManyTopics.Error()
			continue
		}
		c.topics[t] = struct{}{}
		if h.topics[t] == nil {
			h.topics[t] = make(map[*conn]struct{})
		}
		h.topics[t][c] = struct{}{}
		ok = append(ok, t)
	}
	return ok, rejected
}

func (h *Hub) unsubscribe(c *conn, topics []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range topics {
		h.removeLocked(t, c)
	}
}

func (h *Hub) removeLocked(topic string, c *conn) {
	delete(c.topics, topic)
	if subs := h.topics[topic]; subs != nil {
		delete(subs, c)
		if len(subs) == 0 {
			delete(h.topics, topic)
		}
	}
}

// Shutdown 是 §12.4 第 2 步：不再接受新连接，给所有连接发 1012，等它们的 goroutine 退出。
// ctx 到期时直接返回，剩下的连接随进程退出。
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closing = true
	for c := range h.conns {
		c.kick(StatusServiceRestart, "server restarting")
	}
	h.mu.Unlock()

	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
