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
	"net/http"
	"sync"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
)

// DefaultBaseURL 是微信服务端接口的地址。测试里换成 httptest 的。
const DefaultBaseURL = "https://api.weixin.qq.com"

// tokenEarly：access_token 过期前这么久就换新的，免得用到一半过期。
const tokenEarly = 5 * time.Minute

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
	// Invalidate 在微信说 token 无效时调用，下次 Token 强制换新的。
	Invalidate()
}

// StableToken 调 cgi-bin/stable_token 拿 access_token，缓存到过期前 5 分钟。
// 不用 cgi-bin/token：它每调一次都让之前的 token 失效，和别的服务共用 AppID 时会互相踢。
type StableToken struct {
	AppID, Secret string
	// BaseURL 为空时用 DefaultBaseURL。
	BaseURL string
	HTTP    *http.Client
	Clock   clock.Clock

	mu      sync.Mutex
	token   string
	expires time.Time
	force   bool
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
	if s.token != "" && !s.force && s.now().Before(s.expires.Add(-tokenEarly)) {
		return s.token, nil
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	err := postJSON(ctx, httpClient(s.HTTP), baseURL(s.BaseURL)+"/cgi-bin/stable_token", map[string]any{
		"grant_type": "client_credential", "appid": s.AppID, "secret": s.Secret, "force_refresh": s.force,
	}, &out)
	if err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("stable_token 没有返回 access_token: %w", ErrUnavailable)
	}
	s.token, s.force = out.AccessToken, false
	s.expires = s.now().Add(time.Duration(out.ExpiresIn) * time.Second)
	return s.token, nil
}

// Invalidate 作废缓存，下次带 force_refresh 重新拿。
func (s *StableToken) Invalidate() {
	s.mu.Lock()
	s.force = true
	s.mu.Unlock()
}

// Client 调带 access_token 的接口。
type Client struct {
	Tokens TokenSource
	// BaseURL 为空时用 DefaultBaseURL。
	BaseURL string
	HTTP    *http.Client
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
		c.Tokens.Invalidate()
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
