package wxapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
)

// access_token：缓存到过期前 5 分钟；快过期时换新的；接口说 token 无效时作废，下次带 force_refresh 重新拿。
func TestStableTokenCacheRefreshAndInvalidate(t *testing.T) {
	var tokenCalls, forced atomic.Int32
	var lastToken atomic.Value
	var code atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["appid"] != "wxapp" || body["secret"] != "sec" || body["grant_type"] != "client_credential" {
				t.Errorf("stable_token 请求 %v", body)
			}
			if body["force_refresh"] == true {
				forced.Add(1)
			}
			fmt.Fprintf(w, `{"access_token":"tok%d","expires_in":7200}`, tokenCalls.Add(1))
		case "/x":
			lastToken.Store(r.URL.Query().Get("access_token"))
			fmt.Fprintf(w, `{"errcode":%d,"errmsg":"m","n":7}`, code.Load())
		}
	}))
	defer srv.Close()

	clk := clock.NewFake(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	c := &Client{BaseURL: srv.URL, Tokens: &StableToken{AppID: "wxapp", Secret: "sec", BaseURL: srv.URL, Clock: clk}}
	ctx := context.Background()
	var out struct{ N int }
	for i := 0; i < 3; i++ {
		if err := c.PostJSON(ctx, "/x", map[string]string{}, &out); err != nil || out.N != 7 {
			t.Fatalf("%v %+v", err, out)
		}
	}
	if tokenCalls.Load() != 1 || lastToken.Load() != "tok1" {
		t.Fatalf("token 应缓存：取了 %d 次", tokenCalls.Load())
	}

	clk.Advance(2*time.Hour - 4*time.Minute) // 进入过期前 5 分钟
	_ = c.PostJSON(ctx, "/x", nil, nil)
	if tokenCalls.Load() != 2 || lastToken.Load() != "tok2" {
		t.Fatalf("快过期时应刷新：取了 %d 次，用的 %v", tokenCalls.Load(), lastToken.Load())
	}

	code.Store(ErrCodeTokenInvalid)
	err := c.PostJSON(ctx, "/x", nil, nil)
	if got, ok := Code(err); !ok || got != ErrCodeTokenInvalid || !Retryable(err) {
		t.Fatalf("40001 应返回 *Error 且可重试：%v", err)
	}
	code.Store(0)
	_ = c.PostJSON(ctx, "/x", nil, nil)
	if tokenCalls.Load() != 3 || forced.Load() != 1 || lastToken.Load() != "tok3" {
		t.Fatalf("40001 后应强制刷新：取了 %d 次，强制 %d 次", tokenCalls.Load(), forced.Load())
	}
}

// 两个使用方共用**同一个** StableToken（#159）：内容安全（#146）和订阅消息（#137）就是这种关系，
// main.go 建一次注入给两边。共用一个缓存时 token 只取一次；一方因为 40001 强刷之后，另一边
// 下一次拿到的就是换过的那一个 —— 也就是**强刷只重取一次**，而不是两边各取一次、互相把对方顶掉。
func TestSharedStableTokenRefreshesOnce(t *testing.T) {
	var tokenCalls atomic.Int32
	var lastToken atomic.Value
	var tokenInvalid atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			fmt.Fprintf(w, `{"access_token":"tok%d","expires_in":7200}`, tokenCalls.Add(1))
		case "/x":
			lastToken.Store(r.URL.Query().Get("access_token"))
			if tokenInvalid.Load() {
				fmt.Fprint(w, `{"errcode":40001,"errmsg":"invalid credential"}`)
				return
			}
			fmt.Fprint(w, `{"errcode":0}`)
		default:
			t.Errorf("不该请求 %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	clk := clock.NewFake(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	// 一个 token 源、两个使用方各自的 Client —— 就是 main.go 注入给内容安全和订阅消息的形状。
	tokens := &StableToken{AppID: "wxapp", Secret: "sec", BaseURL: srv.URL, Clock: clk}
	guard := &Client{BaseURL: srv.URL, Tokens: tokens}
	subscribe := &Client{BaseURL: srv.URL, Tokens: tokens}
	ctx := context.Background()

	// 两边各发一次：共用一个缓存，所以只该取一次 token。
	if err := guard.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := subscribe.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := tokenCalls.Load(); n != 1 {
		t.Fatalf("两边共用一个缓存，token 只该取一次，实际 %d 次", n)
	}

	// 内容安全那边收到 40001（强刷的触发条件）：客户端作废缓存，但**不**立刻重取。
	tokenInvalid.Store(true)
	if err := guard.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}
	if n := tokenCalls.Load(); n != 1 {
		t.Fatalf("作废本身不该立刻重取，实际 %d 次", n)
	}

	// 订阅消息下一次拿到的必须是换过的 token —— 而且只重取这一次。
	tokenInvalid.Store(false)
	if err := subscribe.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := lastToken.Load(); got != "tok2" {
		t.Fatalf("强刷之后订阅消息该拿到换过的 tok2，实际 %v", got)
	}
	if err := guard.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := tokenCalls.Load(); n != 2 {
		t.Fatalf("两边共用，强刷只该重取一次，实际一共 %d 次", n)
	}
}

// 错误：errcode 非 0 是 *Error（只有限频、繁忙、token 问题可重试）；没拿到回应包着 ErrUnavailable；
// 错误文字里不带 URL（access_token 在查询串里）。
func TestErrors(t *testing.T) {
	var code atomic.Int32
	var status atomic.Int32
	status.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			fmt.Fprint(w, `{"access_token":"tok-secret","expires_in":7200}`)
			return
		}
		w.WriteHeader(int(status.Load()))
		fmt.Fprintf(w, `{"errcode":%d}`, code.Load())
	}))
	c := &Client{BaseURL: srv.URL, Tokens: &StableToken{BaseURL: srv.URL}}
	ctx := context.Background()

	for c0, retry := range map[int]bool{ErrCodeBusy: true, ErrCodeRateLimited: true, 47003: false, 43101: false} {
		code.Store(int32(c0))
		err := c.PostJSON(ctx, "/x", nil, nil)
		if got, _ := Code(err); got != c0 || Retryable(err) != retry {
			t.Errorf("errcode %d：%v retry=%v", c0, err, Retryable(err))
		}
	}
	code.Store(0)
	status.Store(502)
	if err := c.PostJSON(ctx, "/x", nil, nil); !errors.Is(err, ErrUnavailable) || !Retryable(err) {
		t.Fatalf("502 应是 ErrUnavailable：%v", err)
	}
	srv.Close()
	err := c.PostJSON(ctx, "/x", nil, nil)
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "tok-secret") || strings.Contains(err.Error(), "http") {
		t.Fatalf("网络错误应是 ErrUnavailable 且不带 URL：%v", err)
	}
}

// 等到 cond 成立，或者超时判失败。
func waitForCond(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(what)
}

// #191：**迟到的 40001 不该把刚换上的新 token 再强刷掉**。
//
// 并发的样子：8 个请求都带着 tok1 出去，tok1 在服务端失效。
//   第一个 40001 回来 → 该换 tok2；剩下 7 个是**带着 tok1 发出去的**，它们迟到回来时
//   缓存里已经是 tok2 了，不该再动它。改之前是 fetches=3 forced=2。
func TestStaleTokenErrorDoesNotForceRefreshAgain(t *testing.T) {
	var fetches, forced, blocked atomic.Int32
	var current atomic.Value // 服务端认为有效的那个
	gate := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["force_refresh"] == true {
				forced.Add(1)
			}
			tok := fmt.Sprintf("tok%d", fetches.Add(1))
			current.Store(tok) // 换出去的就成了当前有效的
			fmt.Fprintf(w, `{"access_token":%q,"expires_in":7200}`, tok)
			return
		}
		if got := r.URL.Query().Get("access_token"); got != current.Load() {
			if blocked.Add(1) <= 8 {
				<-gate // 卡在这里，模拟「带旧 token 发出去的请求现在才回来」
			}
			fmt.Fprint(w, `{"errcode":40001,"errmsg":"invalid credential"}`)
			return
		}
		fmt.Fprint(w, `{"errcode":0}`)
	}))
	defer srv.Close()

	clk := clock.NewFake(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	tokens := &StableToken{AppID: "wxapp", Secret: "sec", BaseURL: srv.URL, Clock: clk}
	c := &Client{BaseURL: srv.URL, Tokens: tokens}
	ctx := context.Background()

	if err := c.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	current.Store("") // tok1 在服务端失效

	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { errs <- c.PostJSON(ctx, "/x", nil, nil) }()
	}
	waitForCond(t, func() bool { return blocked.Load() == 8 }, "8 个请求都该带着 tok1 卡在服务端")

	// 放出一个：它带回 40001 → 作废缓存里那个 tok1
	gate <- struct{}{}
	if err := <-errs; err == nil {
		t.Fatal("40001 应该报错")
	}

	// 下一次调用换到新 token（这里得真强刷，否则后面白说）
	if err := c.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatalf("换过 token 之后应该成功：%v", err)
	}
	if n, f := fetches.Load(), forced.Load(); n != 2 || f != 1 {
		t.Fatalf("这会儿应该只换过一次：fetches=%d forced=%d，期望 2/1", n, f)
	}

	// 再放掉剩下 7 个迟到的 40001：它们带的是 tok1，不该动已经换上的新 token
	close(gate)
	for i := 0; i < 7; i++ {
		if err := <-errs; err == nil {
			t.Fatal("迟到的这几个也该报 40001")
		}
	}

	if err := c.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if n, f := fetches.Load(), forced.Load(); n != 2 || f != 1 {
		t.Fatalf("迟到的 40001 不该再强刷：fetches=%d forced=%d，期望 2/1", n, f)
	}
}

// #191：两次强刷之间要隔开。微信官方文档（获取稳定版接口调用凭据）写的是
// 「强制刷新模式每天限用 20 次且需间隔 30 秒」—— 间隔不到 30 秒服务端根本不会刷新，
// 白占一次当天的配额，所以 30 秒内我们自己不发 force_refresh=true。
func TestForceRefreshRespectsCooldown(t *testing.T) {
	var fetches, forced atomic.Int32
	var lastForce atomic.Bool
	var current atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f := body["force_refresh"] == true
			if f {
				forced.Add(1)
			}
			lastForce.Store(f)
			tok := fmt.Sprintf("tok%d", fetches.Add(1))
			current.Store(tok)
			fmt.Fprintf(w, `{"access_token":%q,"expires_in":7200}`, tok)
			return
		}
		if got := r.URL.Query().Get("access_token"); got != current.Load() {
			fmt.Fprint(w, `{"errcode":40001,"errmsg":"invalid credential"}`)
			return
		}
		fmt.Fprint(w, `{"errcode":0}`)
	}))
	defer srv.Close()

	clk := clock.NewFake(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	tokens := &StableToken{AppID: "wxapp", Secret: "sec", BaseURL: srv.URL, Clock: clk}
	c := &Client{BaseURL: srv.URL, Tokens: tokens}
	ctx := context.Background()

	if err := c.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if lastForce.Load() {
		t.Fatal("第一次取 token 不该带 force_refresh")
	}

	// tok1 作废 → 该强刷一次
	current.Store("")
	if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}
	if err := c.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if n, f := fetches.Load(), forced.Load(); n != 2 || f != 1 {
		t.Fatalf("这会儿应该强刷过一次：fetches=%d forced=%d", n, f)
	}

	// 只过了 5 秒，新 token 又作废：不该再发 force_refresh=true
	clk.Advance(5 * time.Second)
	current.Store("")
	if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}
	_ = c.PostJSON(ctx, "/x", nil, nil)
	if f := forced.Load(); f != 1 {
		t.Fatalf("30 秒内不该再强刷，实际 forced=%d", f)
	}
	if lastForce.Load() {
		t.Fatal("30 秒内的这一次不该带 force_refresh=true")
	}

	// 隔够 30 秒再作废：这次该真强刷
	clk.Advance(30 * time.Second)
	current.Store("")
	if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}
	if err := c.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if f := forced.Load(); f != 2 {
		t.Fatalf("间隔够了就该真强刷，实际 forced=%d", f)
	}
}
