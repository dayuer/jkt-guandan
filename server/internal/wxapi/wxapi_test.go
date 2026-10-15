package wxapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/metrics"
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
//
//	第一个 40001 回来 → 该换 tok2；剩下 7 个是**带着 tok1 发出去的**，它们迟到回来时
//	缓存里已经是 tok2 了，不该再动它。改之前是 fetches=3 forced=2。
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

// metricsText 把注册表渲染成 Prometheus 文本（和 secheck 的测试一样）。
func metricsText(reg *metrics.Registry) string {
	var b strings.Builder
	reg.WriteTo(context.Background(), &b)
	return b.String()
}

// #197：**强刷要留下记录**。
//
// 微信的强制刷新每天只有 20 次，额度用完内容安全和订阅消息会一起停 —— 所以「真刷了几次」
// 必须能直接从日志和 metric 里看到，不能靠猜。冷却期内被挡回的也要分得清（那不是真刷）。
func TestForceRefreshIsLoggedAndCounted(t *testing.T) {
	var fetches, forced atomic.Int32
	var current atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["force_refresh"] == true {
				forced.Add(1)
			}
			// token 取得让人一眼认得出来 —— 下面要断言它**不出现**在日志里（#199）。
			tok := fmt.Sprintf("tok-secret-%d", fetches.Add(1))
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

	// JSON 输出（生产非 dev 就是 JSON）：字段名和值都能精确断言。
	var logs strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	clk := clock.NewFake(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	reg := &metrics.Registry{}
	// 一个 token 源、两个使用方 —— 就是 main.go 注入给内容安全和订阅消息的形状。
	tokens := &StableToken{AppID: "wxapp", Secret: "sec", BaseURL: srv.URL, Clock: clk, Metrics: NewMetrics(reg)}
	guard := &Client{BaseURL: srv.URL, Tokens: tokens, Name: "内容安全"}
	subscribe := &Client{BaseURL: srv.URL, Tokens: tokens, Name: "订阅消息"}
	ctx := context.Background()

	metric := func(kind string) string { return `guandan_wx_token_total{kind="` + kind + `"} ` }

	// ① 普通取一次
	if err := guard.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if m := metricsText(reg); !strings.Contains(m, metric("ok")+"1") {
		t.Errorf("普通取该记 ok=1，实际：%s", m)
	}
	if forced.Load() != 0 {
		t.Fatal("第一次不该强刷")
	}

	// ② 内容安全收到 40001 → 下一次真强刷：计数 + 一条带原因的 Warn
	current.Store("")
	if err := guard.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}
	if err := guard.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if m := metricsText(reg); !strings.Contains(m, metric("force")+"1") {
		t.Errorf("真强刷该记 force=1，实际：%s", m)
	}
	// 字段名是英文、consumer 和 code **分开**（#199）。
	for _, want := range []string{"强制刷新", `"consumer":"内容安全"`, `"code":40001`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("强刷的日志该带上 %q，实际：%s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "原因") {
		t.Errorf("字段名要用英文（原来是中文的「原因」），实际：%s", logs.String())
	}

	// ③ 只过 5 秒，订阅消息又收到 40001：被冷却挡回，走普通模式 —— 该记 force_cooled，
	//    **force 还是 1**（没真刷），日志也要区分得开。
	//
	// 这里**不能 `logs.Reset()`**：那会把第 ② 段那条 Warn 一起抹掉，下面「两种日志都不要
	// 出现 token」就只剩 Info 可查了（第一版就是这么写漏的 —— 故意把 token 打进 Warn 也没红）。
	// 改成记住当前位置，③ 的断言只看新增的那一段。
	phase3 := logs.Len()
	clk.Advance(5 * time.Second)
	current.Store("")
	if err := subscribe.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}
	if err := subscribe.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	m := metricsText(reg)
	if !strings.Contains(m, metric("force_cooled")+"1") {
		t.Errorf("冷却期内该记 force_cooled=1，实际：%s", m)
	}
	if !strings.Contains(m, metric("force")+"1") {
		t.Errorf("冷却期内被挡回**不该**算真强刷（force 该还是 1），实际：%s", m)
	}
	last := logs.String()[phase3:]
	if !strings.Contains(last, `"consumer":"订阅消息"`) || !strings.Contains(last, "30 秒") {
		t.Errorf("挡回的日志该说明原因（谁触发、为什么按普通模式取），实际：%s", last)
	}
	if !strings.Contains(last, `"code":40001`) {
		t.Errorf("挡回的日志也要带错误码字段，实际：%s", last)
	}
	if strings.Contains(last, "强制刷新") {
		t.Errorf("挡回不是强刷，不该打「强制刷新」那条 Warn，实际：%s", last)
	}

	// #199：**两条日志里都不能出现 token 的值**。access_token 是长期凭证，进了
	// `docker logs` 就等于泄出去了；假 token 起成 `tok-secret-N` 就是为了这里能搜到。
	// 查的是**全程**：Warn（②，上面那条真强刷）和 Info（③，挡回）都要覆盖。
	all := logs.String()
	for _, leak := range []string{"tok-secret", "access_token="} {
		if strings.Contains(all, leak) {
			t.Errorf("日志里不该出现 %q（token 不能进日志），实际：%s", leak, all)
		}
	}
	if !strings.Contains(all, "强制刷新") || !strings.Contains(all, "不到 30 秒") {
		t.Errorf("上面那两条日志都该在全文里（不然这个泄露断言等于没查 Warn），实际：%s", all)
	}

	// ④ 隔够 30 秒再强刷一次：force 变 2，累计额度看得见。
	clk.Advance(30 * time.Second)
	current.Store("")
	if err := guard.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}
	if err := guard.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if m := metricsText(reg); !strings.Contains(m, metric("force")+"2") {
		t.Errorf("再刷一次该记 force=2（额度用了几次要累计可数），实际：%s", m)
	}
}

// #201：**强刷失败之后不能一直强刷**。
//
// 原来 `Token()` 在 postJSON 失败时直接 return：`force` 留在 true、`lastForced` 也不更新。
// 后果是之后**每一次**取 token 都带 `force_refresh=true`，永远退不回普通模式 —— 而普通模式
// 其实能拿到平台认为当前有效的那个。强刷额度用完（45009）时，内容安全和订阅消息的**每一次**
// 调用都会跟着失败，一直到额度恢复或者重启。
//
// 审计时是拿临时测试复现的：缓存 tok0，40001 作废；假服务端对 force_refresh=true 回 45009、
// 普通模式正常给 token；之后每隔一分钟调一次，四次全是 45009，一次普通模式都没走。
func TestFailedForceRefreshDoesNotForceForever(t *testing.T) {
	var fetches, forceSent, normalSent atomic.Int32
	var current atomic.Value
	var quotaOut atomic.Bool // 强刷额度用完：只有 force_refresh=true 会撞 45009

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["force_refresh"] == true {
				forceSent.Add(1)
				if quotaOut.Load() {
					fmt.Fprint(w, `{"errcode":45009,"errmsg":"reach max api daily quota limit"}`)
					return
				}
			} else {
				normalSent.Add(1)
			}
			tok := fmt.Sprintf("tok-secret-%d", fetches.Add(1))
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
	c := &Client{BaseURL: srv.URL, Tokens: tokens, Name: "内容安全"}
	ctx := context.Background()

	// ① 先正常拿一个 token
	if err := c.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}

	// ② 这个 token 在服务端失效了（比如被别处强刷顶掉）+ 强刷额度也用完了
	current.Store("")
	quotaOut.Store(true)
	if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}

	// ③ 下一次取 token 会强刷，而强刷撞上 45009
	err := c.PostJSON(ctx, "/x", nil, nil)
	if code, ok := Code(err); !ok || code != ErrCodeRateLimited {
		t.Fatalf("应该撞上 45009（额度用完），实际：%v", err)
	}

	// ④ 之后每一次调用都该能用：退回普通模式，拿到平台当前有效的那个 token。
	//    改之前这里是「一直强刷」——四次全 45009，一次都成功不了。
	okN := 0
	for i := 0; i < 4; i++ {
		clk.Advance(time.Minute)
		if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
			okN++
		}
	}
	if okN != 4 {
		t.Errorf("强刷失败后该退回普通模式、之后每个调用都能成，实际只成了 %d/4 次"+
			"（额度用完不该让两个使用方一直挂到重启）", okN)
	}
	if normalSent.Load() == 0 {
		t.Errorf("一次普通模式都没走（force 发了 %d 次），说明还在无脑强刷", forceSent.Load())
	}
}

// #201 的第 (1) 件：强刷**失败**之后，30 秒内不能再出现 `force_refresh=true`。
//
// 为什么要单独测一条：上面那条用的是 45009，而 45009 那个分支本来就会清掉 `force` ——
// 冷却根本没机会起作用，于是把「失败也记 lastForced」整行删掉，上面那条测试照样绿
// （auditor 验过）。真正需要冷却兜住的是**临时错误**：`-1`（系统繁忙）、网络抖动这种，
// 它们不清 force，只有冷却能让下一个请求别再强刷。
func TestFailedForceRefreshRespectsCooldown(t *testing.T) {
	var forceSent, normalSent atomic.Int32
	var current atomic.Value
	var busy atomic.Bool // 强刷时回 -1（系统繁忙）；普通模式正常给 token

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["force_refresh"] == true {
				forceSent.Add(1)
				if busy.Load() {
					fmt.Fprint(w, `{"errcode":-1,"errmsg":"system error"}`)
					return
				}
			} else {
				normalSent.Add(1)
			}
			tok := fmt.Sprintf("tok-secret-%d", normalSent.Load()+forceSent.Load())
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
	c := &Client{BaseURL: srv.URL, Tokens: tokens, Name: "内容安全"}
	ctx := context.Background()

	// ① 正常拿一个 token
	if err := c.PostJSON(ctx, "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	// ② 它失效了 → 40001 → 作废
	current.Store("")
	if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}
	// ③ 强刷撞上临时错误 -1
	busy.Store(true)
	if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("强刷撞 -1 应该报错")
	}
	if forceSent.Load() != 1 {
		t.Fatalf("这会儿应该只强刷过一次，实际 %d", forceSent.Load())
	}

	// ④ 之后每秒调一次、共 4 次：**30 秒冷却之内不该再强刷**。
	//    删掉「失败也记 lastForced」这一行的话，这里每次都会强刷、每次都是 -1 —— 0/4。
	okN := 0
	for i := 0; i < 4; i++ {
		clk.Advance(time.Second)
		if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
			okN++
		}
	}
	if okN != 4 {
		t.Errorf("强刷失败后 30 秒内该走普通模式、每次都成，实际只成了 %d/4", okN)
	}
	if f := forceSent.Load(); f != 1 {
		t.Errorf("30 秒内不该再强刷（临时错误也要受冷却约束），实际强刷了 %d 次", f)
	}
	if normalSent.Load() == 0 {
		t.Error("一次普通模式都没走")
	}

	// ⑤ 隔够 30 秒再失败一次：那次该重新强刷（冷却不是永久的）
	busy.Store(false)
	current.Store("")
	if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("40001 应该报错")
	}
	busy.Store(true)
	clk.Advance(31 * time.Second)
	if err := c.PostJSON(ctx, "/x", nil, nil); err == nil {
		t.Fatal("这次强刷撞 -1 应该报错")
	}
	if f := forceSent.Load(); f != 2 {
		t.Errorf("隔够 30 秒之后该重新强刷，实际一共 %d 次", f)
	}
}
