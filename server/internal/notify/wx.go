package notify

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
	"github.com/dayuer/jkt-guandan/server/internal/config"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// 小游戏订阅消息（#137，docs/server-design.md §5.3「订阅消息通道」）。
//
// 小游戏收不到 Web Push，提醒改走 subscribeMessage.send：按 (appid, openid) 发，openid 从 user_links
// （wx_openid:<appid>）取，不存别处、不打日志。一次性订阅：用户每同意一次某个模板，就能发一条，
// 剩余次数记在 wx_subscribe_quota。

const (
	stableTokenURL   = "https://api.weixin.qq.com/cgi-bin/stable_token"
	subscribeSendURL = "https://api.weixin.qq.com/cgi-bin/message/subscribe/send"
	// tokenEarly：access_token 过期前这么久就换新的，免得发到一半过期。
	tokenEarly = 5 * time.Minute
	// MaxWxQuota 是每个模板最多攒的次数。客户端可以反复上报，没必要无限累加。
	MaxWxQuota = 50
	// wxThingMax 是模板里 thing 类型字段的上限（微信规定 20 个字符以内）。
	wxThingMax = 20
)

// wxKindTemplate 是提醒类型到模板用途的对应（§5.3 的提醒类型表）。不在表里的提醒不走订阅消息。
var wxKindTemplate = map[string]string{
	"full":        config.WxTmplFull,
	"seat_left":   config.WxTmplFull,
	"t_minus_1h":  config.WxTmplRemind,
	"rescheduled": config.WxTmplRemind,
}

// TokenSource 提供 access_token。生产用 StableToken，测试用假的。
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	// Invalidate 在微信说 token 无效（40001 等）时调用，下次 Token 强制换新的。
	Invalidate()
}

// StableToken 调 cgi-bin/stable_token 拿 access_token，缓存到过期前 5 分钟。
// 用 stable_token 而不是 cgi-bin/token：后者每调一次都让之前的 token 失效，和别的服务共用 AppID 时会互相踢。
type StableToken struct {
	AppID, Secret string
	// Endpoint 为空时用微信的正式地址；测试里指向 httptest。
	Endpoint string
	Client   *http.Client
	Clock    clock.Clock

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
	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = stableTokenURL
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{
		"grant_type": "client_credential", "appid": s.AppID, "secret": s.Secret, "force_refresh": s.force,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("调 stable_token: %T", err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&out); err != nil {
		return "", fmt.Errorf("stable_token 响应不是 JSON（HTTP %d）", resp.StatusCode)
	}
	if out.ErrCode != 0 || out.AccessToken == "" {
		return "", fmt.Errorf("stable_token 返回 errcode=%d %s", out.ErrCode, out.ErrMsg)
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

// WxMessage 是一条订阅消息。
type WxMessage struct {
	OpenID     string
	TemplateID string
	Page       string
	Data       map[string]string
}

// WxSender 发一条订阅消息，返回微信的 errcode（0 成功）。网络错误时 err 非空。测试里换成假的。
type WxSender interface {
	SendSubscribe(ctx context.Context, msg WxMessage) (errcode int, err error)
}

// 微信 errcode 里要特别处理的几个。
const (
	wxErrTokenInvalid  = 40001 // access_token 无效
	wxErrTokenExpired  = 42001 // access_token 过期
	wxErrTokenBad      = 40014 // access_token 不合法
	wxErrNotSubscribed = 43101 // 用户没有订阅（或者次数在微信那边已经用完）
	wxErrBusy          = -1    // 系统繁忙
	wxErrRateLimited   = 45009 // 接口调用超过限额
)

// SubscribeClient 是真实的 subscribeMessage.send 客户端。
type SubscribeClient struct {
	Tokens TokenSource
	// Endpoint 为空时用微信的正式地址；测试里指向 httptest。
	Endpoint string
	Client   *http.Client
}

// SendSubscribe 调 subscribeMessage.send。access_token 在查询串里，所以 URL 不能打日志；
// token 无效时作废缓存，这条提醒按重试处理。
func (c *SubscribeClient) SendSubscribe(ctx context.Context, msg WxMessage) (int, error) {
	token, err := c.Tokens.Token(ctx)
	if err != nil {
		return 0, err
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = subscribeSendURL
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	data := make(map[string]map[string]string, len(msg.Data))
	for k, v := range msg.Data {
		data[k] = map[string]string{"value": v}
	}
	body, _ := json.Marshal(map[string]any{
		"touser": msg.OpenID, "template_id": msg.TemplateID, "page": msg.Page, "data": data, "lang": "zh_CN",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?access_token="+token, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error 带着完整 URL（含 access_token），只报类型。
		return 0, fmt.Errorf("调 subscribeMessage.send: %T", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("subscribeMessage.send 返回 HTTP %d", resp.StatusCode)
	}
	var out struct {
		ErrCode int `json:"errcode"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&out); err != nil {
		return 0, errors.New("subscribeMessage.send 响应不是 JSON")
	}
	switch out.ErrCode {
	case wxErrTokenInvalid, wxErrTokenExpired, wxErrTokenBad:
		c.Tokens.Invalidate()
	}
	return out.ErrCode, nil
}

// WxChannel 是订阅消息通道的配置：AppID（决定 openid 关联的 provider）、模板、发送器。
type WxChannel struct {
	AppID     string
	Templates map[string]config.WxTemplate // 按用途：config.WxTmplFull、config.WxTmplRemind
	Sender    WxSender
}

// template 返回这种提醒对应的模板；不走订阅消息时 ok 为 false。
func (c *WxChannel) template(kind string) (config.WxTemplate, bool) {
	if c == nil {
		return config.WxTemplate{}, false
	}
	t, ok := c.Templates[wxKindTemplate[kind]]
	return t, ok && t.ID != ""
}

// NewWxChannel 按配置建订阅消息通道。没配 AppID / Secret 或者一个模板都没配时返回 nil：通道关闭。
func NewWxChannel(cfg config.Config, clk clock.Clock) *WxChannel {
	tmpls := cfg.WxSubscribeTemplates()
	if len(tmpls) == 0 {
		return nil
	}
	if !cfg.WxEnabled() {
		// 开发模式、没配 AppID：openid 关联记在 wx_openid:dev 下（identity.DevWxAppID），发送只记日志。
		return &WxChannel{AppID: devWxAppID, Templates: tmpls, Sender: DevWxSender{}}
	}
	tokens := &StableToken{AppID: cfg.WxAppID, Secret: cfg.WxSecret, Clock: clk}
	return &WxChannel{AppID: cfg.WxAppID, Templates: tmpls, Sender: &SubscribeClient{Tokens: tokens}}
}

// devWxAppID 要和 identity.DevWxAppID 一致（开发模式的小游戏登录把 openid 记在 wx_openid:dev 下）。
const devWxAppID = "dev"

// DevWxSender 是开发模式的假发送器：只记日志（模板和事项，不记 openid），一律成功。
type DevWxSender struct{}

func (DevWxSender) SendSubscribe(_ context.Context, m WxMessage) (int, error) {
	slog.Info("开发模式：订阅消息不真发", "template", m.TemplateID, "page", m.Page, "data", m.Data)
	return 0, nil
}

// TemplateIDs 是通道里配置了的模板 ID，按用途索引（GET /api/config 下发给小游戏）。
func (c *WxChannel) TemplateIDs() map[string]string {
	out := map[string]string{}
	if c == nil {
		return out
	}
	for use, t := range c.Templates {
		if t.ID != "" {
			out[use] = t.ID
		}
	}
	return out
}

// ErrWxDisabled 表示订阅消息通道没开（没配 AppID / Secret 或模板）。
var ErrWxDisabled = errors.New("订阅消息未开启")

// AddWxQuota 记下用户在 wx.requestSubscribeMessage 里同意了的模板：每个模板次数 + 1（最多 MaxWxQuota）。
// 不是本站配置的模板 ID 忽略；同一次上报里重复的只算一次。返回实际记下的模板数。
func (s *Service) AddWxQuota(ctx context.Context, userID string, accepted []string) (int, error) {
	if s.wx == nil {
		return 0, ErrWxDisabled
	}
	known := map[string]bool{}
	for _, id := range s.wx.TemplateIDs() {
		known[id] = true
	}
	seen := map[string]bool{}
	var ids []string
	for _, id := range accepted {
		if known[id] && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	now := s.clk.Now().Unix()
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO wx_subscribe_quota (user_id, template_id, remaining, updated_at) VALUES (?, ?, 1, ?)
				ON CONFLICT(user_id, template_id) DO UPDATE SET
				  remaining = min(remaining + 1, ?), updated_at = excluded.updated_at`,
				userID, id, now, MaxWxQuota); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO events (at, user_id, name, env) VALUES (?, ?, 'wx_subscribe_accepted', 'minigame')`, now, userID)
		return err
	})
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

// SetWx 接上订阅消息通道。不调用（或传 nil）时通道关闭。
func (s *Service) SetWx(c *WxChannel) { s.wx = c }

// Wx 是接上的订阅消息通道，没开时为 nil。
func (s *Service) Wx() *WxChannel { return s.wx }

// wxPayload 生成订阅消息：事项、时间、地点三个字段，按模板配置的字段名填。
func wxPayload(t config.WxTemplate, kind string, g gameInfo) WxMessage {
	title, _ := message(kind, g)
	at := g.StartsAt.In(jakarta)
	return WxMessage{
		TemplateID: t.ID,
		// 点开消息回到小游戏并打开这一局（main.ts 读启动参数里的 g）。
		Page: "?g=" + g.Code,
		Data: map[string]string{
			t.Keys[0]: clip(title, wxThingMax),
			t.Keys[1]: fmt.Sprintf("%d年%d月%d日 %02d:%02d", at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute()),
			t.Keys[2]: clip(g.Venue, wxThingMax),
		},
	}
}

// clip 截到 n 个字符（不是字节）。微信对超长的 thing 字段直接报错，而不是截断。
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// logWxError 只记 errcode，不记 openid（§5.2：openid 不出现在日志里）。
func logWxError(kind string, code int, err error) {
	if err != nil {
		slog.Warn("订阅消息发送失败", "kind", kind, "err", err)
		return
	}
	slog.Warn("订阅消息发送失败", "kind", kind, "errcode", code)
}
