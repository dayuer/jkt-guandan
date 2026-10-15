package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/config"
	"github.com/dayuer/jkt-guandan/server/internal/store"
	"github.com/dayuer/jkt-guandan/server/internal/wxapi"
)

// 小游戏订阅消息（#137，docs/server-design.md §5.3「订阅消息通道」）。
//
// 小游戏收不到 Web Push，提醒改走 subscribeMessage.send：按 (appid, openid) 发，openid 从 user_links
// （wx_openid:<appid>）取，不存别处、不打日志。一次性订阅：用户每同意一次某个模板，就能发一条，
// 剩余次数记在 wx_subscribe_quota。

const (
	subscribeSendPath = "/cgi-bin/message/subscribe/send"
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

// WxMessage 是一条订阅消息。
type WxMessage struct {
	OpenID     string
	TemplateID string
	Page       string
	Data       map[string]string
}

// WxSender 发一条订阅消息。微信返回非 0 errcode 时是 *wxapi.Error；没拿到回应时包着 wxapi.ErrUnavailable。
// 测试里换成假的。
type WxSender interface {
	SendSubscribe(ctx context.Context, msg WxMessage) error
}

// wxErrNotSubscribed：用户没有订阅（或者次数在微信那边已经用完）。
const wxErrNotSubscribed = 43101

// SubscribeClient 是真实的 subscribeMessage.send，建在 wxapi.Client 上（access_token 的缓存、作废都在那里）。
type SubscribeClient struct {
	API *wxapi.Client
}

// SendSubscribe 调 subscribeMessage.send。
func (c *SubscribeClient) SendSubscribe(ctx context.Context, msg WxMessage) error {
	data := make(map[string]map[string]string, len(msg.Data))
	for k, v := range msg.Data {
		data[k] = map[string]string{"value": v}
	}
	return c.API.PostJSON(ctx, subscribeSendPath, map[string]any{
		"touser": msg.OpenID, "template_id": msg.TemplateID, "page": msg.Page, "data": data, "lang": "zh_CN",
	}, nil)
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
	tokens := &wxapi.StableToken{AppID: cfg.WxAppID, Secret: cfg.WxSecret, Clock: clk}
	return &WxChannel{AppID: cfg.WxAppID, Templates: tmpls, Sender: &SubscribeClient{API: &wxapi.Client{Tokens: tokens}}}
}

// devWxAppID 要和 identity.DevWxAppID 一致（开发模式的小游戏登录把 openid 记在 wx_openid:dev 下）。
const devWxAppID = "dev"

// DevWxSender 是开发模式的假发送器：只记日志（模板和事项，不记 openid），一律成功。
type DevWxSender struct{}

func (DevWxSender) SendSubscribe(_ context.Context, m WxMessage) error {
	slog.Info("开发模式：订阅消息不真发", "template", m.TemplateID, "page", m.Page, "data", m.Data)
	return nil
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

// logWxError 只记错误（errcode 或错误类型），不记 openid（§5.2：openid 不出现在日志里）。
// wxapi 返回的错误不含请求 URL，所以也没有 access_token。
func logWxError(kind string, err error) {
	slog.Warn("订阅消息发送失败", "kind", kind, "err", err)
}
