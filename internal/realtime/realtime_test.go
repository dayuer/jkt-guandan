package realtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/goleak"

	"github.com/dayuer/jkt-guandan/internal/realtime"
	"github.com/dayuer/jkt-guandan/internal/store"
)

// §9：WebSocket 的测试要用 goleak 检查 goroutine 泄漏。
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const site = "https://guandan.id"

type env struct {
	hub     *realtime.Hub
	srv     *httptest.Server
	revoked sync.Map // deviceID → true：心跳复查时判为失效
}

// newEnv 起一个只挂 /ws 的服务。测试里用 X-Test-User 头冒充 cookie 认出的用户。
func newEnv(t *testing.T, ping, pong time.Duration) *env {
	t.Helper()
	e := &env{hub: realtime.NewHub()}
	hub := e.hub
	srv := httptest.NewServer(realtime.Handler(realtime.Options{
		Hub:         hub,
		AllowOrigin: func(o string) bool { return o == site },
		Identify: func(r *http.Request) (realtime.Principal, error) {
			if r.Header.Get("X-Test-User") == "banned" {
				return realtime.Principal{}, errors.New("banned")
			}
			return realtime.Principal{UserID: r.Header.Get("X-Test-User"), DeviceID: r.Header.Get("X-Test-Device")}, nil
		},
		Revalidate: func(_ context.Context, p realtime.Principal) bool {
			_, revoked := e.revoked.Load(p.DeviceID)
			return !revoked
		},
		Build:        "build-123",
		PingInterval: ping,
		PongTimeout:  pong,
	}))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
		srv.Close()
	})
	e.srv = srv
	return e
}

func (e *env) dial(t *testing.T, user, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return e.dialDevice(t, user, "", origin)
}

// dialDevice 以某个用户的某台设备连上来（user 为空表示没有身份）。
func (e *env) dialDevice(t *testing.T, user, device, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if device != "" {
		h.Set("X-Test-Device", device)
	}
	if origin != "" {
		h.Set("Origin", origin)
	}
	if user != "" {
		h.Set("X-Test-User", user)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/ws",
		&websocket.DialOptions{HTTPHeader: h})
	if c != nil {
		c.SetReadLimit(1 << 20)
		t.Cleanup(func() { _ = c.CloseNow() })
	}
	return c, resp, err
}

func (e *env) mustDial(t *testing.T, user string) *websocket.Conn {
	t.Helper()
	c, _, err := e.dial(t, user, site)
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	if m := read(t, c); m["type"] != "hello" {
		t.Fatalf("第一条应是 hello，实际 %v", m)
	}
	return c
}

func read(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("读消息: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("不是 JSON：%s", data)
	}
	return m
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("发消息: %v", err)
	}
}

func subscribe(t *testing.T, c *websocket.Conn, topics ...string) map[string]any {
	t.Helper()
	send(t, c, map[string]any{"op": "sub", "topics": topics})
	return read(t, c)
}

// eventually 轮询 cond，直到为真或超时。
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("超时：%s", what)
}

// closeStatus 等连接被服务端关闭，返回关闭码（异常断开时为 -1）。
func closeStatus(t *testing.T, c *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("连接没有被关闭")
			}
			return websocket.CloseStatus(err)
		}
	}
}

func TestHelloCarriesBuildAndUser(t *testing.T) {
	e := newEnv(t, 0, 0)
	c, _, err := e.dial(t, "u1", site)
	if err != nil {
		t.Fatal(err)
	}
	m := read(t, c)
	if m["type"] != "hello" || m["build"] != "build-123" || m["user_id"] != "u1" || m["server_time"] == nil {
		t.Fatalf("hello = %v", m)
	}
}

// §5.5：握手必须校验 Origin，防跨站 WebSocket 劫持。
func TestCrossSiteOriginRejected(t *testing.T) {
	e := newEnv(t, 0, 0)
	for _, origin := range []string{"https://evil.example", ""} {
		_, resp, err := e.dial(t, "u1", origin)
		if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("Origin %q 应被拒（403），实际 %v %v", origin, resp, err)
		}
	}
}

func TestIdentifyErrorRejectsHandshake(t *testing.T) {
	e := newEnv(t, 0, 0)
	if _, resp, err := e.dial(t, "banned", site); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("身份校验失败时应 403，实际 %v %v", resp, err)
	}
}

func TestPublishReachesSubscribers(t *testing.T) {
	e := newEnv(t, 0, 0)
	a, b := e.mustDial(t, ""), e.mustDial(t, "")
	if m := subscribe(t, a, "game:8K2"); m["type"] != "subscribed" {
		t.Fatalf("订阅回执 %v", m)
	}
	subscribe(t, b, "feed:SCBD")

	e.hub.Publish(realtime.Event{Topic: "game:8K2", Type: "seat.taken", V: 7, Data: map[string]int{"missing": 1}})
	m := read(t, a)
	if m["topic"] != "game:8K2" || m["type"] != "seat.taken" || m["v"] != float64(7) {
		t.Fatalf("收到 %v", m)
	}
	// b 没订阅这个主题，收不到；再发一条 b 订阅的，确认 b 的第一条就是它。
	e.hub.Publish(realtime.Event{Topic: "feed:SCBD", Type: "feed.game_added"})
	if m := read(t, b); m["type"] != "feed.game_added" {
		t.Fatalf("b 收到了不该收的：%v", m)
	}

	send(t, a, map[string]any{"op": "unsub", "topics": []string{"game:8K2"}})
	eventually(t, "退订生效", func() bool { return e.hub.Subscribers("game:8K2") == 0 })
}

// user:{id} 只能订阅自己的；没有身份不能订阅任何 user 主题。
func TestUserTopicIsPrivate(t *testing.T) {
	e := newEnv(t, 0, 0)
	me := e.mustDial(t, "u1")
	if m := subscribe(t, me, "user:u1"); m["type"] != "subscribed" || len(m["topics"].([]any)) != 1 {
		t.Fatalf("订阅自己的 user 主题：%v", m)
	}

	subscribe(t, me, "user:u2") // 回执里没有它，随后是一条错误
	if m := read(t, me); m["type"] != "error" || m["code"] != "forbidden_topic" || m["topic"] != "user:u2" {
		t.Fatalf("订阅别人的 user 主题应被拒：%v", m)
	}

	anon := e.mustDial(t, "")
	subscribe(t, anon, "user:u1")
	if m := read(t, anon); m["code"] != "forbidden_topic" {
		t.Fatalf("没有身份不能订阅 user 主题：%v", m)
	}
	if n := e.hub.Subscribers("user:u1"); n != 1 {
		t.Fatalf("user:u1 应只有 1 个订阅者，实际 %d", n)
	}

	subscribe(t, me, "bogus")
	if m := read(t, me); m["code"] != "bad_topic" {
		t.Fatalf("不认识的主题：%v", m)
	}
}

func TestTopicLimitPerConnection(t *testing.T) {
	e := newEnv(t, 0, 0)
	c := e.mustDial(t, "")
	var topics []string
	for i := 0; i < realtime.MaxTopicsPerConn+1; i++ {
		topics = append(topics, "game:G"+string(rune('A'+i)))
	}
	if m := subscribe(t, c, topics...); len(m["topics"].([]any)) != realtime.MaxTopicsPerConn {
		t.Fatalf("应只订上 %d 个：%v", realtime.MaxTopicsPerConn, m)
	}
	if m := read(t, c); m["code"] != "too_many_topics" {
		t.Fatalf("第 21 个应被拒：%v", m)
	}
}

// §5.5：发送队列写满就断开这个连接，不拖慢别人。
//
// 慢客户端完全不读：内核缓冲写满后写 goroutine 卡住，队列随之写满，它被断开。
// 发布按「快客户端读到一条再发下一条」的节奏进行，所以快客户端的队列最多只有 1 条，
// 在任何机器上（包括 CI 加 -race）都不会溢出——被断开的只可能是慢的那个（#40 复审第 1 条）。
//
// 断言的是「谁被断开」：快客户端仍在订阅、随后发的消息照收；慢客户端的连接结束。
// 慢客户端收不到 4000 这个关闭码：它的 socket 缓冲是满的，关闭帧写不进去，服务端只能直接关掉连接，
// 它把缓冲里的消息读完之后看到的是连接断开。
func TestSlowConsumerIsDisconnected(t *testing.T) {
	e := newEnv(t, 0, 0)
	slow := e.mustDial(t, "")
	subscribe(t, slow, "game:SLOW")
	fast := e.mustDial(t, "")
	subscribe(t, fast, "game:SLOW")

	got := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for {
			if _, _, err := fast.Read(ctx); err != nil {
				readErr <- err
				return
			}
			got <- struct{}{}
		}
	}()

	payload := strings.Repeat("x", 3000)
	published := 0
	for published < 100000 && e.hub.Subscribers("game:SLOW") == 2 {
		e.hub.Publish(realtime.Event{Topic: "game:SLOW", Type: "seat.taken", Data: payload})
		published++
		select {
		case <-got: // 快客户端读到了，再发下一条
		case err := <-readErr:
			t.Fatalf("快客户端在第 %d 条时被断开：%v", published, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("快客户端 5 秒内没收到第 %d 条", published)
		}
	}
	eventually(t, "慢客户端被断开", func() bool { return e.hub.Subscribers("game:SLOW") == 1 })
	t.Logf("发到第 %d 条时慢客户端被断开", published)

	// 留下的是快客户端：随后发的消息它照收。
	e.hub.Publish(realtime.Event{Topic: "game:SLOW", Type: "after"})
	select {
	case <-got:
	case err := <-readErr:
		t.Fatalf("快客户端不该被断开：%v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("快客户端没收到断开之后的消息")
	}

	// 慢客户端：把缓冲里的消息读完之后，连接是断开的。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if _, _, err := slow.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("慢客户端的连接没有被关闭")
			}
			break
		}
	}
}

// §5.5：每 25 秒 ping 一次，10 秒内没有 pong 就断开。这里调成 100ms / 200ms。
func TestMissingPongDisconnects(t *testing.T) {
	e := newEnv(t, 100*time.Millisecond, 200*time.Millisecond)
	alive := e.mustDial(t, "")
	subscribe(t, alive, "game:LIVE")
	// 一直在读的连接会自动回 pong，不会被断开。
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for {
			if _, _, err := alive.Read(ctx); err != nil {
				return
			}
		}
	}()

	dead := e.mustDial(t, "")
	subscribe(t, dead, "game:DEAD")
	// dead 不再读，也就不回 pong。
	eventually(t, "不回 pong 的连接被断开", func() bool { return e.hub.Subscribers("game:DEAD") == 0 })
	if e.hub.Subscribers("game:LIVE") != 1 {
		t.Fatal("正常回 pong 的连接不该被断开")
	}
	<-readDone
}

// §5.5：每个连接每秒最多 10 条客户端消息，超了就断开（1008）。
func TestClientMessageFlood(t *testing.T) {
	e := newEnv(t, 0, 0)
	c := e.mustDial(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < 30; i++ {
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"op":"unsub","topics":[]}`)); err != nil {
			break
		}
	}
	if code := closeStatus(t, c); code != websocket.StatusPolicyViolation {
		t.Fatalf("关闭码 %d，期望 1008", code)
	}
}

// §12.3：同一 IP 每秒最多 5 条新连接。
func TestNewConnectionRateLimitPerIP(t *testing.T) {
	e := newEnv(t, 0, 0)
	for i := 0; i < realtime.NewConnsPerSecond; i++ {
		e.mustDial(t, "")
	}
	_, resp, err := e.dial(t, "", site)
	if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("第 6 条应被限流（429），实际 %v %v", resp, err)
	}
}

// §12.4 第 2 步：关闭时给所有连接发 1012；之后的握手直接 503。
func TestShutdownSends1012(t *testing.T) {
	e := newEnv(t, 0, 0)
	c := e.mustDial(t, "u1")
	subscribe(t, c, "user:u1")

	codes := make(chan websocket.StatusCode, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for {
			if _, _, err := c.Read(ctx); err != nil {
				codes <- websocket.CloseStatus(err)
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := e.hub.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if code := <-codes; code != realtime.StatusServiceRestart {
		t.Fatalf("关闭码 %d，期望 1012", code)
	}
	if !e.hub.Closing() || e.hub.Conns() != 0 {
		t.Fatalf("Closing=%v Conns=%d", e.hub.Closing(), e.hub.Conns())
	}
	if _, resp, err := e.dial(t, "", site); err == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("关闭后的握手应 503，实际 %v %v", resp, err)
	}
}

// §5.5：只在事务提交后发布。回滚的事务登记的事件一条都不能发出去（不会有「幽灵座位」）。
func TestRolledBackTransactionPublishesNothing(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, 0, 0)
	c := e.mustDial(t, "")
	subscribe(t, c, "game:GHOST")

	publishIn := func(fail bool, typ string) error {
		return st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
			tx.AfterCommit(func(context.Context) {
				e.hub.Publish(realtime.Event{Topic: "game:GHOST", Type: typ})
			})
			if fail {
				return errors.New("业务失败，回滚")
			}
			return nil
		})
	}
	if err := publishIn(true, "seat.taken.ghost"); err == nil {
		t.Fatal("应当回滚")
	}
	if err := publishIn(false, "seat.taken.real"); err != nil {
		t.Fatal(err)
	}
	// 收到的第一条就是提交成功的那条：回滚的那条没有发出来。
	if m := read(t, c); m["type"] != "seat.taken.real" {
		t.Fatalf("收到 %v", m)
	}
}

// #43：设备被退出、切换身份之后，它已经建立的连接立刻以 4001 断开，之后发到 user:{旧id} 的事件收不到；
// 同一个用户的其他设备不受影响。
func TestKickDeviceClosesOnlyThatDevice(t *testing.T) {
	e := newEnv(t, 0, 0)
	stolen, _, err := e.dialDevice(t, "u1", "d-stolen", site)
	if err != nil {
		t.Fatal(err)
	}
	read(t, stolen) // hello
	subscribe(t, stolen, "user:u1")
	mine, _, err := e.dialDevice(t, "u1", "d-mine", site)
	if err != nil {
		t.Fatal(err)
	}
	read(t, mine)
	subscribe(t, mine, "user:u1")

	start := time.Now()
	e.hub.KickDevice("d-stolen")
	e.hub.Publish(realtime.Event{Topic: "user:u1", Type: "me.updated"})

	if code := closeStatus(t, stolen); code != realtime.StatusIdentityRevoked {
		t.Fatalf("被退出的设备应收到 4001，实际 %d", code)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("应在 1 秒内断开，实际 %v", d)
	}
	if m := read(t, mine); m["type"] != "me.updated" {
		t.Fatalf("同一用户的其他设备应照常收到事件：%v", m)
	}
}

// 被踢之后、真正关闭之前，连接上不能再收到任何事件（#43）。
func TestKickedConnectionReceivesNothingAfterKick(t *testing.T) {
	e := newEnv(t, 0, 0)
	c, _, err := e.dialDevice(t, "u1", "d1", site)
	if err != nil {
		t.Fatal(err)
	}
	read(t, c)
	subscribe(t, c, "user:u1")
	e.hub.KickUser("u1")
	for i := 0; i < 20; i++ {
		e.hub.Publish(realtime.Event{Topic: "user:u1", Type: "secret"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err == nil {
		t.Fatalf("被踢之后不该再收到任何消息：%s", data)
	}
	if code := websocket.CloseStatus(err); code != realtime.StatusIdentityRevoked {
		t.Fatalf("关闭码 %d，期望 4001", code)
	}
}

// #43 的兜底：另一个进程里的操作（guandan admin ban、直接改库）通知不到 Hub，靠每次心跳时复查。
func TestHeartbeatRevalidatesIdentity(t *testing.T) {
	e := newEnv(t, 100*time.Millisecond, 500*time.Millisecond)
	c, _, err := e.dialDevice(t, "u1", "d1", site)
	if err != nil {
		t.Fatal(err)
	}
	read(t, c)
	subscribe(t, c, "user:u1")
	anon := e.mustDial(t, "") // 没有身份的连接不复查
	subscribe(t, anon, "game:X")

	e.revoked.Store("d1", true)
	if code := closeStatus(t, c); code != realtime.StatusIdentityRevoked {
		t.Fatalf("复查失败应以 4001 断开，实际 %d", code)
	}
	time.Sleep(300 * time.Millisecond)
	if e.hub.Subscribers("game:X") != 1 {
		t.Fatal("没有身份的连接不该被复查断开")
	}
}
