package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
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

	// DefaultPresenceInterval 是同一牌局两次 presence 广播之间的最短间隔（#46）。
	DefaultPresenceInterval = 2 * time.Second

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
	// StatusIdentityRevoked 表示这条连接的身份失效了（设备被退出、切换到别的身份、被封禁，#43）。
	// 客户端收到后重新拉一次身份再重连。
	StatusIdentityRevoked websocket.StatusCode = 4001
)

// Principal 是一条连接认出来的身份。都为空表示没有身份（只能订阅公开主题）。
type Principal struct {
	UserID   string
	DeviceID string
}

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

	// presence 是各 game: 主题「N 人正在看」的广播状态，只在内存里（#46）。
	presence      map[string]*presenceState
	presenceEvery time.Duration
}

type presenceState struct {
	last     int         // 上次广播出去的人数
	lastSent time.Time   // 上次广播的时间
	timer    *time.Timer // 已排上的下一次广播；nil 表示没有
	// greet 是订阅后还没收到过人数的连接。下一次 flush 时：人数变了就广播给所有人（它们也在里面），
	// 没变就只发给它们。这样一条连接订阅一个牌局只收到一条 presence，不会和广播重复。
	greet map[*conn]struct{}
}

// presenceHeadroom：发送队列里至少要留这么多空位才发 presence。presence 只是展示用、可以丢，
// 不能挤占真正的事件，更不能因为它把客户端当慢客户端踢掉。
const presenceHeadroom = 4

func NewHub() *Hub {
	return &Hub{
		topics:        make(map[string]map[*conn]struct{}),
		conns:         make(map[*conn]struct{}),
		presence:      make(map[string]*presenceState),
		presenceEvery: DefaultPresenceInterval,
	}
}

// SetPresenceInterval 改 presence 广播的最短间隔（测试用）。
func (h *Hub) SetPresenceInterval(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.presenceEvery = d
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
	h.publishLocked(ev.Topic, msg)
}

func (h *Hub) publishLocked(topic string, msg []byte) {
	for c := range h.topics[topic] {
		if c.isKicked() {
			continue
		}
		select {
		case c.send <- msg:
		default:
			c.slow()
		}
	}
}

// KickDevice 以 4001 断开这台设备的所有连接（#43）。在写事务提交后调用：设备被退出、切换到别的身份。
func (h *Hub) KickDevice(deviceID string) {
	if deviceID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		if c.deviceID == deviceID {
			c.kick(StatusIdentityRevoked, "identity revoked")
			h.markKickedPresenceLocked(c)
		}
	}
}

// KickUser 以 4001 断开这个用户所有设备的连接（#43）：用户被封禁、注销。
func (h *Hub) KickUser(userID string) {
	if userID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		if c.userID == userID {
			c.kick(StatusIdentityRevoked, "identity revoked")
			h.markKickedPresenceLocked(c)
		}
	}
}

// markKickedPresenceLocked：被踢的连接马上不计入在看人数（viewersLocked 跳过它），
// 但要等关闭握手结束才 unregister，客户端不回应时可能要几秒；所以踢的时候就排一次广播。
func (h *Hub) markKickedPresenceLocked(c *conn) {
	for t := range c.topics {
		h.markPresenceLocked(t, nil)
	}
}

// Viewers 返回某个牌局主题当前有几台设备在看（#46）。同一设备开多个标签页算一个；
// 没有身份的连接认不出设备，每条算一个。
func (h *Hub) Viewers(topic string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.viewersLocked(topic)
}

func (h *Hub) viewersLocked(topic string) int {
	seen := make(map[any]struct{}, len(h.topics[topic]))
	for c := range h.topics[topic] {
		if c.isKicked() {
			continue
		}
		if c.deviceID != "" {
			seen[c.deviceID] = struct{}{}
		} else {
			seen[c] = struct{}{}
		}
	}
	return len(seen)
}

func presenceMsg(topic string, n int) []byte {
	msg, _ := json.Marshal(Event{Topic: topic, Type: EventPresence, Data: map[string]int{"viewers": n}})
	return msg
}

// EventPresence 是「N 人正在看」的事件类型（§5.5）。没有版本号。
const EventPresence = "presence"

// markPresenceLocked 在 game: 主题的订阅者变了之后调用：排一次广播，离上次广播不到
// presenceEvery 就等到满了再发，期间的变化合并成一次。newcomer 不为 nil 时，它是刚订阅的连接，
// 下一次 flush 一定告诉它当前人数。
func (h *Hub) markPresenceLocked(topic string, newcomer *conn) {
	if h.closing || !strings.HasPrefix(topic, "game:") {
		return
	}
	st := h.presence[topic]
	if st == nil {
		st = &presenceState{greet: make(map[*conn]struct{})}
		h.presence[topic] = st
	}
	if newcomer != nil {
		st.greet[newcomer] = struct{}{}
	}
	if st.timer != nil {
		return
	}
	wait := time.Until(st.lastSent.Add(h.presenceEvery))
	if wait < 0 {
		wait = 0
	}
	st.timer = time.AfterFunc(wait, func() { h.flushPresence(topic) })
}

func (h *Hub) flushPresence(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.presence[topic]
	if st == nil || h.closing {
		return
	}
	st.timer = nil
	n := h.viewersLocked(topic)
	if n == 0 {
		delete(h.presence, topic) // 没人看了，也就没人收；下次有人来重新开始
		return
	}
	greet := st.greet
	st.greet = make(map[*conn]struct{})
	if n == st.last {
		// 人数没变：只告诉新来的（比如同一设备新开的标签页）。
		if len(greet) > 0 {
			msg := presenceMsg(topic, n)
			for c := range greet {
				sendPresence(c, msg)
			}
		}
		return
	}
	st.last, st.lastSent = n, time.Now()
	msg := presenceMsg(topic, n)
	for c := range h.topics[topic] {
		sendPresence(c, msg)
	}
}

// sendPresence 往发送队列里放一条 presence；队列快满了就丢掉这条，不踢连接。
func sendPresence(c *conn, msg []byte) {
	if c.isKicked() || len(c.send) > cap(c.send)-presenceHeadroom {
		return
	}
	select {
	case c.send <- msg:
	default:
	}
}

// Subscribers 返回某个主题当前的订阅连接数（测试用）。
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
		h.markPresenceLocked(t, c)
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
		if _, ok := subs[c]; !ok {
			return
		}
		delete(subs, c)
		if len(subs) == 0 {
			delete(h.topics, topic)
		}
		if st := h.presence[topic]; st != nil {
			delete(st.greet, c)
		}
		h.markPresenceLocked(topic, nil)
	}
}

// Shutdown 是 §12.4 第 2 步：不再接受新连接，给所有连接发 1012，等它们的 goroutine 退出。
// ctx 到期时直接返回，剩下的连接随进程退出。
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closing = true
	for t, st := range h.presence {
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(h.presence, t)
	}
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
