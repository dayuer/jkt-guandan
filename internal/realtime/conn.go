package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// conn 是一条 WebSocket 连接。topics 由 Hub 的锁保护；send、kicked 是读写两个 goroutine 之间的通道。
type conn struct {
	ws     *websocket.Conn
	userID string
	ip     string
	send   chan []byte
	topics map[string]struct{}

	kickOnce   sync.Once
	kicked     chan struct{}
	kickCode   websocket.StatusCode
	kickReason string
	// abortWrite 打断正在进行的写：慢客户端的写会一直卡到超时，队列满时直接断开它，不等。
	abortWrite context.CancelFunc
	writeCtx   context.Context
}

// kick 要求写 goroutine 用给定的关闭码断开连接。不阻塞，可以在 Hub 的锁里调用，可以重复调用。
func (c *conn) kick(code websocket.StatusCode, reason string) {
	c.kickOnce.Do(func() {
		c.kickCode, c.kickReason = code, reason
		close(c.kicked)
	})
}

// trySend 往发送队列里放一条控制消息（hello、订阅回执、错误），队列满就断开。
func (c *conn) trySend(v any) {
	msg, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- msg:
	default:
		c.slow()
	}
}

// slow 处理发送队列满：断开连接，并打断卡住的写。不阻塞，可以在 Hub 的锁里调用。
func (c *conn) slow() {
	c.kick(StatusSlowConsumer, "send queue full")
	c.abortWrite()
}

// Options 组装 GET /ws 的处理器。
type Options struct {
	Hub *Hub
	// AllowOrigin 判断握手的 Origin 是不是本站（§5.5：防跨站 WebSocket 劫持）。
	AllowOrigin func(origin string) bool
	// Identify 从请求（cookie）里认出用户；没有身份返回空字符串。返回错误时拒绝握手（403），
	// 比如设备被封禁。
	Identify func(r *http.Request) (userID string, err error)
	// Build 是构建号，放在 hello 消息里，前端据此提示刷新（§13.4）。
	Build string
	// 测试里可以调短；零值用 25 秒和 10 秒（§5.5）。
	PingInterval time.Duration
	PongTimeout  time.Duration
}

// Handler 返回 GET /ws 的处理器。它挂在主 mux 上，前面的中间件已经把 RemoteAddr 换成了
// 真实的客户端 IP（#35），并且 /ws 不占 256 的并发名额（httpapi.limitConcurrency）。
func Handler(opts Options) http.Handler {
	if opts.PingInterval <= 0 {
		opts.PingInterval = DefaultPingInterval
	}
	if opts.PongTimeout <= 0 {
		opts.PongTimeout = DefaultPongTimeout
	}
	return &handler{opts: opts, limits: newIPLimiter(time.Now)}
}

type handler struct {
	opts   Options
	limits *ipLimiter
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.opts.AllowOrigin == nil || !h.opts.AllowOrigin(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if h.opts.Hub.Closing() {
		http.Error(w, "server restarting", http.StatusServiceUnavailable)
		return
	}
	ip := hostOf(r.RemoteAddr)
	if !h.limits.acquire(ip) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "too many websocket connections", http.StatusTooManyRequests)
		return
	}
	released := false
	release := func() {
		if !released {
			released = true
			h.limits.release(ip)
		}
	}
	defer release()

	var userID string
	if h.opts.Identify != nil {
		id, err := h.opts.Identify(r)
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		userID = id
	}

	// http.Server 的 ReadTimeout、WriteTimeout 会在连接上留下截止时间，接管之后要清掉，
	// 改由心跳管理（§12.2）。
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	// Origin 上面已经按本站域名校验过；库自带的检查只比较 Host，代理后面会误判，所以关掉。
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return // Accept 已经写好错误响应
	}
	ws.SetReadLimit(MaxMessageBytes)

	c := &conn{
		ws:     ws,
		userID: userID,
		ip:     ip,
		send:   make(chan []byte, SendQueueSize),
		topics: make(map[string]struct{}),
		kicked: make(chan struct{}),
	}
	c.writeCtx, c.abortWrite = context.WithCancel(context.Background())
	defer c.abortWrite()
	if !h.opts.Hub.register(c) {
		_ = ws.Close(websocket.StatusTryAgainLater, "server busy")
		return
	}
	defer h.opts.Hub.unregister(c)

	c.trySend(map[string]any{
		"type":        "hello",
		"server_time": time.Now().Unix(),
		"build":       h.opts.Build,
		"user_id":     userID,
	})

	// 两个 goroutine：这个（HTTP handler 的 goroutine）负责读，另起一个负责写。
	ctx, cancel := context.WithCancel(context.Background())
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		h.writeLoop(ctx, c)
	}()
	h.readLoop(ctx, c)
	cancel()
	c.abortWrite() // 写 goroutine 可能正卡在一次写上
	<-writerDone
	_ = ws.CloseNow()
}

// readLoop 处理客户端的 sub / unsub。每秒最多 ClientMsgPerSecond 条，超了就断开。
func (h *handler) readLoop(ctx context.Context, c *conn) {
	bucket := float64(ClientMsgPerSecond)
	last := time.Now()
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		now := time.Now()
		bucket += now.Sub(last).Seconds() * ClientMsgPerSecond
		if bucket > ClientMsgPerSecond {
			bucket = ClientMsgPerSecond
		}
		last = now
		if bucket < 1 {
			c.kick(websocket.StatusPolicyViolation, "too many messages")
			continue // 写 goroutine 会关掉连接，Read 随后返回错误
		}
		bucket--

		var msg struct {
			Op     string   `json:"op"`
			Topics []string `json:"topics"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			c.trySend(map[string]any{"type": "error", "code": "bad_message"})
			continue
		}
		switch msg.Op {
		case "sub":
			ok, rejected := h.opts.Hub.subscribe(c, msg.Topics)
			c.trySend(map[string]any{"type": "subscribed", "topics": nonNil(ok)})
			for topic, code := range rejected {
				c.trySend(map[string]any{"type": "error", "code": code, "topic": topic})
			}
		case "unsub":
			h.opts.Hub.unsubscribe(c, msg.Topics)
		default:
			c.trySend(map[string]any{"type": "error", "code": "bad_op"})
		}
	}
}

// writeLoop 从发送队列取消息写出，每 PingInterval 发一次 ping，PongTimeout 内没有 pong 就断开。
func (h *handler) writeLoop(ctx context.Context, c *conn) {
	ticker := time.NewTicker(h.opts.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.kicked:
			// Close 会等对方回关闭帧（读 goroutine 还在读），最多等 5 秒。
			_ = c.ws.Close(c.kickCode, c.kickReason)
			return
		case msg := <-c.send:
			wctx, cancel := context.WithTimeout(c.writeCtx, writeTimeout)
			err := c.ws.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				_ = c.ws.CloseNow()
				return
			}
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, h.opts.PongTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					slog.Info("websocket 心跳超时，断开", "ip", c.ip)
				}
				_ = c.ws.CloseNow()
				return
			}
		}
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func hostOf(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return remote
}

// ipLimiter 是 §12.3 的 WebSocket 限制：同一 IP 最多 MaxConnsPerIP 条连接，
// 新连接每秒最多 NewConnsPerSecond 条（令牌桶，容量同速率）。
type ipLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	state map[string]*ipState
}

type ipState struct {
	active int
	tokens float64
	last   time.Time
}

func newIPLimiter(now func() time.Time) *ipLimiter {
	return &ipLimiter{now: now, state: make(map[string]*ipState)}
}

func (l *ipLimiter) acquire(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.state) > 4096 {
		l.sweepLocked(now)
	}
	st := l.state[ip]
	if st == nil {
		st = &ipState{tokens: NewConnsPerSecond, last: now}
		l.state[ip] = st
	}
	st.tokens += now.Sub(st.last).Seconds() * NewConnsPerSecond
	if st.tokens > NewConnsPerSecond {
		st.tokens = NewConnsPerSecond
	}
	st.last = now
	if st.active >= MaxConnsPerIP || st.tokens < 1 {
		return false
	}
	st.tokens--
	st.active++
	return true
}

func (l *ipLimiter) release(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.state[ip]
	if st == nil {
		return
	}
	st.active--
	// 没有活跃连接、令牌也已经回满的 IP 不用再记，表不会无限增长（§11.5）。
	if st.active <= 0 && st.tokens+l.now().Sub(st.last).Seconds()*NewConnsPerSecond >= NewConnsPerSecond {
		delete(l.state, ip)
	}
}

// sweepLocked 删掉没有活跃连接、令牌已经回满的 IP。
func (l *ipLimiter) sweepLocked(now time.Time) {
	for ip, st := range l.state {
		if st.active <= 0 && st.tokens+now.Sub(st.last).Seconds()*NewConnsPerSecond >= NewConnsPerSecond {
			delete(l.state, ip)
		}
	}
}
