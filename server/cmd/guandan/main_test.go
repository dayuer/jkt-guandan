package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/realtime"
	"github.com/dayuer/jkt-guandan/server/internal/store"
	"github.com/dayuer/jkt-guandan/server/internal/supervise"
)

// §12.4：先排空 HTTP（第 3 步），再停后台任务（第 4 步），然后关库、放锁。
// 请求还没做完时，后台任务的 ctx 不能被取消（PR #25 复审第 5 条）。
func TestShutdownDrainsHTTPBeforeStoppingBackground(t *testing.T) {
	dir := t.TempDir()
	lock, err := store.LockDataDir(dir)
	if err != nil {
		t.Fatalf("加锁: %v", err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("迁移: %v", err)
	}

	var (
		mu     sync.Mutex
		events []string
	)
	record := func(e string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}

	bgCtx, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()
	group := supervise.New(nil)
	group.Go(bgCtx, "probe", time.Minute, func(ctx context.Context, beat func()) error {
		<-ctx.Done()
		record("后台任务停止")
		return nil
	})

	entered := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		record("请求做完")
		w.WriteHeader(http.StatusNoContent)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()

	reqDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			resp.Body.Close()
		}
		reqDone <- err
	}()
	<-entered

	shutdownDone := make(chan struct{})
	go func() {
		shutdown([]*http.Server{srv}, stopBackground, group, st, lock)
		close(shutdownDone)
	}()

	// 排空期间：shutdown 卡在第 3 步，后台任务还在跑。
	time.Sleep(200 * time.Millisecond)
	if err := bgCtx.Err(); err != nil {
		t.Fatalf("请求还没做完，后台任务的 ctx 就被取消了: %v", err)
	}
	select {
	case <-shutdownDone:
		t.Fatal("请求还没做完，shutdown 就返回了")
	default:
	}

	close(release)
	if err := <-reqDone; err != nil {
		t.Fatalf("进行中的请求应当正常完成: %v", err)
	}
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown 没有结束")
	}

	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "请求做完" || got[1] != "后台任务停止" {
		t.Fatalf("关闭顺序不对: %v", got)
	}

	// 第 6 步：锁已经释放，下一个实例能拿到。
	again, err := store.LockDataDir(dir)
	if err != nil {
		t.Fatalf("shutdown 之后应能重新加锁: %v", err)
	}
	_ = again.Release()
}

func TestProbeURL(t *testing.T) {
	for addr, want := range map[string]string{
		"":               "http://127.0.0.1:8080/healthz",
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/healthz",
		"[::]:9000":      "http://127.0.0.1:9000/healthz",
		"127.0.0.1:8081": "http://127.0.0.1:8081/healthz",
	} {
		got, err := probeURL(addr, "/healthz")
		if err != nil || got != want {
			t.Errorf("probeURL(%q) = %q, %v；期望 %q", addr, got, err, want)
		}
	}
	if _, err := probeURL("8080", "/healthz"); err == nil {
		t.Error("没有冒号的地址应当报错")
	}
}

func TestHealthcheckExitCode(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	if code := healthcheck(addr, "/healthz"); code != 0 {
		t.Fatalf("/healthz 返回 200 时退出码应为 0，实际 %d", code)
	}
	status.Store(http.StatusServiceUnavailable)
	if code := healthcheck(addr, "/healthz"); code != 1 {
		t.Fatalf("/healthz 返回 503 时退出码应为 1，实际 %d", code)
	}
	srv.Close()
	if code := healthcheck(addr, "/healthz"); code != 1 {
		t.Fatalf("连不上时退出码应为 1，实际 %d", code)
	}
}

func TestHealthcheckReadyChecksReadyz(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("GUANDAN_ADDR", srv.Listener.Addr().String())
	var out, errOut strings.Builder
	c := cmdIO{out: &out, errOut: &errOut}
	if code := c.run("healthcheck", nil); code != 0 {
		t.Fatalf("/healthz 200 时应退出 0，实际 %d", code)
	}
	if code := c.run("healthcheck", []string{"--ready"}); code != 1 {
		t.Fatalf("/readyz 503 时应退出 1，实际 %d", code)
	}
}

func TestMetricsCommandPrintsLocalMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("guandan_build_info{build=\"x\"} 1\n"))
	}))
	defer srv.Close()
	t.Setenv("GUANDAN_METRICS_ADDR", srv.Listener.Addr().String())
	var out, errOut strings.Builder
	if code := (cmdIO{out: &out, errOut: &errOut}).run("metrics", nil); code != 0 {
		t.Fatalf("退出码 %d：%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "guandan_build_info") {
		t.Fatalf("输出 %q", out.String())
	}
}

// games 的事件经适配器原样进 Hub（主题、类型、版本号、数据都不丢）：#4 的 seat.taken 等由此推出去。
func TestGamesPublisherForwardsToHub(t *testing.T) {
	hub := realtime.NewHub()
	srv := httptest.NewServer(realtime.Handler(realtime.Options{Hub: hub, AllowOrigin: func(string) bool { return true }}))
	defer srv.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws",
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://x"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, _, _ = c.Read(ctx) // hello
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"op":"sub","topics":["game:8K2"]}`))
	if err := awaitSubscribed(ctx, c); err != nil {
		t.Fatal(err)
	}

	gamesPublisher{hub}.Publish(games.Event{Topic: games.GameTopic("8K2"), Type: games.EventSeatTaken, V: 7, Data: map[string]int{"missing": 1}})
	data, err := nextEvent(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"topic":"game:8K2","type":"seat.taken","v":7,"data":{"missing":1}}` {
		t.Fatalf("收到 %s", got)
	}
}

// 订阅牌局后，「已订阅」和「在看人数」（presence，#46）谁先到不固定：Hub 在 subscribe 里另起
// goroutine 发 presence，conn 随后才把 subscribed 放进发送队列，负载高时 presence 会抢在前面。
// 原来按「hello → subscribed」盲读一条，presence 先到时就把它当成了 subscribed，后面再把真正的
// subscribed 当成事件，测试偶发红（#232）。下面两个函数按类型认帧，不依赖先后。

// wsReader 是读帧要用的那一个方法；真连接和下面测试里的剧本都满足它。
type wsReader interface {
	Read(context.Context) (websocket.MessageType, []byte, error)
}

func frameType(data []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(data, &m)
	return m.Type
}

// awaitSubscribed 一直读到 subscribed 为止，中间的 presence 一并丢掉。
func awaitSubscribed(ctx context.Context, r wsReader) error {
	for {
		_, data, err := r.Read(ctx)
		if err != nil {
			return err
		}
		if frameType(data) == "subscribed" {
			return nil
		}
	}
}

// nextEvent 跳过连接层的帧（hello、subscribed、presence），返回下一条业务事件。
func nextEvent(ctx context.Context, r wsReader) ([]byte, error) {
	for {
		_, data, err := r.Read(ctx)
		if err != nil {
			return nil, err
		}
		switch frameType(data) {
		case "hello", "subscribed", realtime.EventPresence:
			continue
		}
		return data, nil
	}
}

// scriptedReader 按给定顺序吐帧，读完返回 io.EOF。用来确定性地造出 presence 抢先的那种顺序 ——
// 真连接上它只在负载高时偶尔出现，单独跑几百次也撞不上（#232）。
type scriptedReader [][]byte

func (s *scriptedReader) Read(context.Context) (websocket.MessageType, []byte, error) {
	if len(*s) == 0 {
		return 0, nil, io.EOF
	}
	data := (*s)[0]
	*s = (*s)[1:]
	return websocket.MessageText, data, nil
}

func TestSubscribeReadsTolerateFrameOrder(t *testing.T) {
	sub := []byte(`{"type":"subscribed","topics":["game:8K2"]}`)
	pres := []byte(`{"topic":"game:8K2","type":"presence","data":{"viewers":1}}`)
	ev := []byte(`{"topic":"game:8K2","type":"seat.taken","v":7}`)
	for name, frames := range map[string][][]byte{
		"subscribed 先到":                  {sub, pres, ev},
		"presence 抢在 subscribed 前（#232）": {pres, sub, ev},
		"没有 presence":                    {sub, ev},
	} {
		t.Run(name, func(t *testing.T) {
			r := scriptedReader(frames)
			ctx := context.Background()
			if err := awaitSubscribed(ctx, &r); err != nil {
				t.Fatalf("等 subscribed：%v", err)
			}
			// awaitSubscribed 必须正好停在 subscribed 之后：presence 抢先时不能只读一条就收手。
			if want := len(frames) - 1 - indexOf(frames, sub); len(r) != want {
				t.Fatalf("awaitSubscribed 之后还剩 %d 帧，应该剩 %d 帧（没读到 subscribed 就停了）", len(r), want)
			}
			got, err := nextEvent(ctx, &r)
			if err != nil {
				t.Fatalf("等事件：%v", err)
			}
			if string(got) != string(ev) {
				t.Fatalf("拿到的不是事件：%s", got)
			}
		})
	}
	// nextEvent 自己也要跳过 subscribed：没先等 subscribed（或者等的时候被 presence 打断）时，
	// 不能把 subscribed 当成事件交出去。
	t.Run("nextEvent 跳过 subscribed 和 presence", func(t *testing.T) {
		r := scriptedReader{pres, sub, ev}
		got, err := nextEvent(context.Background(), &r)
		if err != nil || string(got) != string(ev) {
			t.Fatalf("拿到 %s（err=%v），应该是事件", got, err)
		}
	})
}

func indexOf(frames [][]byte, f []byte) int {
	for i, x := range frames {
		if string(x) == string(f) {
			return i
		}
	}
	return -1
}
