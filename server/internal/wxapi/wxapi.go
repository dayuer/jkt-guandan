// Package wxapi 是调微信服务端接口的公共部分（#137 起；#146 的内容安全检查也用它）：
// access_token（stable_token，缓存到过期前 5 分钟再换）和带 access_token 的 POST JSON 调用。
//
// access_token 在查询串里，所以请求 URL、*url.Error 都不能打日志；这里返回的错误只带错误类型和 errcode。
package wxapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/metrics"
)

// DefaultBaseURL 是微信服务端接口的地址。测试里换成 httptest 的。
const DefaultBaseURL = "https://api.weixin.qq.com"

// tokenEarly：access_token 过期前这么久就换新的，免得用到一半过期。
const tokenEarly = 5 * time.Minute

// forceCooldown 是两次强制刷新之间的最短间隔。
//
// 微信官方文档《获取稳定版接口调用凭据》里和这个数有关的**原文只有两处**：
//
//	注意事项：「强制刷新模式每天限用 20 次且需间隔 30 秒。」
//	5.3 小节标题：「强制刷新模式，慎用，连续使用需要至少间隔30s」
//	https://developers.weixin.qq.com/doc/service/api/base/api_getstableaccesstoken.html
//
// 30 秒这个数照抄原文。**「间隔不到就不发 force_refresh、改走普通模式」是我们自己的
// 处理，不是文档要求** —— 文档只说「限用 20 次」「需间隔 30 秒」，没说间隔不到会怎样。
// 我们这么做的理由：这一模式每天只有 20 次，发出去不成功也照样算占一次，宁可省着用，
// 也不要去赌服务端的行为。
const forceCooldown = 30 * time.Second

// 几个要特别处理的 errcode。
const (
	ErrCodeBusy         = -1    // 系统繁忙
	ErrCodeTokenInvalid = 40001 // access_token 无效
	ErrCodeTokenBad     = 40014 // access_token 不合法
	ErrCodeTokenExpired = 42001 // access_token 过期
	ErrCodeRateLimited  = 45009 // 接口调用超过限额
)

// Error 是微信返回的非 0 errcode。
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("微信接口 errcode=%d %s", e.Code, e.Msg) }

// ErrUnavailable 表示没拿到微信的回应：网络错误、非 200、响应不是 JSON。值得重试。
var ErrUnavailable = errors.New("微信接口暂时不可用")

// Code 取出 err 里的 errcode；不是 *Error 时返回 0, false。
func Code(err error) (int, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return 0, false
}

// IsTokenError 说明这个 errcode 是 access_token 的问题（已作废缓存，下次会换新的）。
func IsTokenError(code int) bool {
	return code == ErrCodeTokenInvalid || code == ErrCodeTokenBad || code == ErrCodeTokenExpired
}

// Retryable 说明这个错误值得稍后重试：没拿到回应、系统繁忙、限频、access_token 失效。
func Retryable(err error) bool {
	if errors.Is(err, ErrUnavailable) {
		return true
	}
	code, ok := Code(err)
	return ok && (code == ErrCodeBusy || code == ErrCodeRateLimited || IsTokenError(code))
}

// TokenSource 提供 access_token。生产用 StableToken，测试用假的。
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	// Invalidate 在微信说 token 无效时调用，bad 是**这一次请求实际带出去的那个** token。
	// 下次 Token 会强制换新的 —— 但如果缓存里已经不是它了就不动（见 StableToken.Invalidate）。
	//
	// consumer 是使用方（「内容安全」「订阅消息」），code 是微信返回的错误码。两个都只用来
	// 打日志：强刷每天只有 20 次，额度用完内容安全和订阅消息会一起停，所以真刷的时候要留下
	// 记录，而且要能看出**是谁、因为什么**触发的（#197、#199）。
	Invalidate(bad, consumer string, code int)
}

// cause 是「这一次强刷是谁、因为什么触发的」，攒到真刷的那一刻再打日志。
type cause struct {
	consumer string // 使用方
	code     int    // 微信错误码
}

// Metrics 是 access_token 取用情况的计数。nil 表示不计（多数测试不需要）。
//
// 微信的强制刷新每天限 20 次，额度用完就只能等缓存自然过期 —— 内容安全和订阅消息都会
// 跟着受影响。所以「真的刷了几次」必须是能直接看到的数，不能靠猜（#197）。
type Metrics struct {
	// Fetches 按 kind 分：ok（普通取）/ force（真的带了 force_refresh）/ force_cooled
	// （想强刷但离上次不到 30 秒，退成普通模式）/ error（没取到）。
	Fetches *metrics.CounterVec
}

func NewMetrics(r *metrics.Registry) *Metrics {
	m := &Metrics{Fetches: &metrics.CounterVec{}}
	if r != nil {
		r.CounterVec("guandan_wx_token_total",
			"微信 access_token 取用次数，按 kind 分（ok 普通取 / force 真的强刷 / force_cooled 间隔不到 30 秒退成普通模式 / error 没取到）。"+
				"这是**进程启动以来**的累计值：重启会清零，而微信是按自然日算额度的 —— 想知道「最近一天用了几次」用 increase(guandan_wx_token_total{kind=\"force\"}[1d])，并且记住它只是**下限**（重启丢掉的看不到）。",
			"kind", m.Fetches)
	}
	return m
}

func (m *Metrics) inc(kind string) {
	if m != nil && m.Fetches != nil {
		m.Fetches.Inc(kind)
	}
}

// StableToken 调 cgi-bin/stable_token 拿 access_token，缓存到过期前 5 分钟。
// 不用 cgi-bin/token：它每调一次都让之前的 token 失效，和别的服务共用 AppID 时会互相踢。
type StableToken struct {
	AppID, Secret string
	// BaseURL 为空时用 DefaultBaseURL。
	BaseURL string
	HTTP    *http.Client
	Clock   clock.Clock
	// Metrics 为 nil 时不计数。生产在 main.go 接上（#197）。
	Metrics *Metrics

	mu         sync.Mutex
	token      string
	expires    time.Time
	force      bool
	forceCause cause     // 触发这次强刷的原因，留给真正刷的那一刻打日志（#197、#199）
	lastForced time.Time // 上一次真的带 force_refresh=true 出去的时刻，冷却用
}

func (s *StableToken) now() time.Time {
	if s.Clock == nil {
		return time.Now()
	}
	return s.Clock.Now()
}

// Token 返回缓存的 token；快过期或被作废了就换新的。
func (s *StableToken) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && !s.force && now.Before(s.expires.Add(-tokenEarly)) {
		return s.token, nil
	}
	// 离上一次强刷不到 30 秒就不带 force_refresh（见 forceCooldown：这是我们自己的取舍，
	// 文档只写了「每天限用 20 次且需间隔 30 秒」）。普通模式会返回平台认为当前有效的那个，
	// 文档 5.2 的返回示例注释也印证了：「如果仍然有效，会返回上次的 token，并给出所剩有效时间」。
	force := s.force && now.Sub(s.lastForced) >= forceCooldown
	cooled := s.force && !force
	from := s.forceCause
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	err := postJSON(ctx, httpClient(s.HTTP), baseURL(s.BaseURL)+"/cgi-bin/stable_token", map[string]any{
		"grant_type": "client_credential", "appid": s.AppID, "secret": s.Secret, "force_refresh": force,
	}, &out)
	if err != nil {
		s.Metrics.inc("error")
		if force || cooled {
			// 走到这儿说明「缓存里那个 token 已经被微信判成无效了」（否则不会置 force），
			// 而这一次没换到新的。两件事都得做（#201）：
			//
			// 1. **记下冷却时刻**。不记的话 30 秒那条管不到失败，下一个请求会立刻再强刷
			//    一次 —— 额度用完时就一直撞 45009。`-1`（系统繁忙）、网络抖动这类临时
			//    错误也算在内。
			// 2. **把缓存里那个坏 token 丢掉**，让下一次重新取而不是直接把它返回出去。
			//    只做第 1 件是不够的：缓存里那个坏 token 还在，下一次 `Token()` 会认为它
			//    还有效、直接返回，调用方继续拿它去撞 40001，照样回不到普通模式。
			// 注意 `cooled` 也走这里：那一次发的是**普通模式**的请求，失败也把冷却往后推
			// 30 秒。可以接受 —— 取不到 token 的时候，本来也不该马上换一种姿势再撞一次。
			s.lastForced = now
			s.token, s.expires = "", time.Time{}
		}
		if code, ok := Code(err); ok && code == ErrCodeRateLimited {
			// 45009 官方原文：「调用超过天级别频率限制。可调用 clear_quota 接口恢复调用额度。」
			//
			// **很可能是**强刷那每天 20 次的额度用完了 —— 但这是推断，不是官方说法：错误表里
			// 45009 只写了「天级别频率限制」，没有和强刷次数对应起来，50 万次的日总额度用完
			// 报的也是它。不管是哪一种，继续带 force_refresh 都只会继续撞，所以清掉标记、
			// 下一次走普通模式去取。
			s.force = false
			slog.Warn("微信 access_token 强刷撞上 45009（很可能是当天强刷额度用完），退回普通模式",
				"consumer", from.consumer, "code", code)
		}
		return "", err
	}
	if out.AccessToken == "" {
		s.Metrics.inc("error")
		return "", fmt.Errorf("stable_token 没有返回 access_token: %w", ErrUnavailable)
	}
	s.token, s.force, s.forceCause = out.AccessToken, false, cause{}
	switch {
	case force:
		// 真的刷了。每天只有 20 次，额度用完会直接影响内容安全和订阅消息 ——
		// 这条 Warn 就是「用掉了一次」的账（#197）。
		// 字段名和其他地方一致用英文，consumer 和 code 分开，不要拼进一句话里（#199）：
		// token 本身、以及任何带 token 的 URL 都不进日志，这两个字段都不含 token。
		s.lastForced = now
		s.Metrics.inc("force")
		slog.Warn("微信 access_token 强制刷新（每天限 20 次）", "consumer", from.consumer, "code", from.code)
	case cooled:
		// 想刷但离上次不到 30 秒，按 forceCooldown 的取舍退成普通模式（我们自己定的，不是文档要求）。
		s.Metrics.inc("force_cooled")
		slog.Info("微信 access_token 想强刷，但离上次不到 30 秒：这一次按普通模式取", "consumer", from.consumer, "code", from.code)
	default:
		s.Metrics.inc("ok")
	}
	s.expires = now.Add(time.Duration(out.ExpiresIn) * time.Second)
	return s.token, nil
}

// Invalidate 作废缓存，下次带 force_refresh 重新拿 —— 条件是 bad 还是缓存里的那个。
//
// 并发时 N 个请求都带着 tok1 出去、tok1 失效：第一个 40001 回来就该换 tok2，剩下几个是
// **带着 tok1 发出去的**，它们迟到回来时缓存里已经是 tok2 了，不该再把它顶掉。按文档强刷
// 会让上一个立刻失效，这么顶来顶去会连锁，也很快用完每天 20 次。
func (s *StableToken) Invalidate(bad, consumer string, code int) {
	if bad == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != bad {
		return
	}
	if !s.force {
		// 记下**最先**把它弄脏的那次：同一轮里两个使用方都收到 40001 时，
		// 真正刷的是同一次，报第一个就够了。
		s.forceCause = cause{consumer: consumer, code: code}
	}
	s.force = true
}

// Client 调带 access_token 的接口。
type Client struct {
	Tokens TokenSource
	// Name 是这个使用方的名字（「内容安全」「订阅消息」），只用于日志：强刷是有配额的，
	// 出事时要能看出是谁触发的（#197）。空着会记成「未命名使用方」。
	Name string
	// BaseURL 为空时用 DefaultBaseURL。
	BaseURL string
	HTTP    *http.Client
}

func (c *Client) name() string {
	if c.Name == "" {
		return "未命名使用方"
	}
	return c.Name
}

// PostJSON 调 POST <BaseURL><path>?access_token=…，请求体是 req 的 JSON，响应解到 resp（可以为 nil）。
// errcode 非 0 返回 *Error；access_token 的问题会顺带作废缓存。没拿到回应返回包着 ErrUnavailable 的错误。
func (c *Client) PostJSON(ctx context.Context, path string, req, resp any) error {
	token, err := c.Tokens.Token(ctx)
	if err != nil {
		return err
	}
	err = postJSON(ctx, httpClient(c.HTTP), baseURL(c.BaseURL)+path+"?access_token="+token, req, resp)
	if code, ok := Code(err); ok && IsTokenError(code) {
		// 把用过的那个 token 一起带回去：这样「旧 token 的迟到 40001」不会把新 token 顶掉。
		// 顺便带上是谁、什么错 —— 真刷的那一刻要打进日志（#197）。
		c.Tokens.Invalidate(token, c.name(), code)
	}
	return err
}

func postJSON(ctx context.Context, client *http.Client, url string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := client.Do(r)
	if err != nil {
		// *url.Error 带着完整 URL（可能含 access_token / secret），只报类型。
		return fmt.Errorf("%w（%T）", ErrUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%w（HTTP %d）", ErrUnavailable, res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("%w（读响应 %T）", ErrUnavailable, err)
	}
	var head struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("%w（响应不是 JSON）", ErrUnavailable)
	}
	if head.ErrCode != 0 {
		return &Error{Code: head.ErrCode, Msg: head.ErrMsg}
	}
	if resp != nil {
		if err := json.Unmarshal(raw, resp); err != nil {
			return fmt.Errorf("%w（响应格式不对）", ErrUnavailable)
		}
	}
	return nil
}

func baseURL(s string) string {
	if s == "" {
		return DefaultBaseURL
	}
	return s
}

func httpClient(c *http.Client) *http.Client {
	if c == nil {
		return &http.Client{Timeout: 10 * time.Second}
	}
	return c
}
