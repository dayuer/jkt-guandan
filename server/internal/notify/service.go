// Package notify 负责 Web Push（docs/server-design.md §5.3）：VAPID 密钥、推送订阅，
// 以及把 outbox 里的提醒发出去的后台任务。
//
// games 只往 outbox 里写「给谁、什么事、什么时候发」，不写文案；文案在发送时按提醒类型和牌局的
// 当前状态生成，所以局头改了地点，还没发出的提醒用的也是新地点。
package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

const (
	kvVAPIDPublic  = "vapid_public"
	kvVAPIDPrivate = "vapid_private"
)

// Service 持有 VAPID 密钥，管理订阅。用 New 创建。
type Service struct {
	st         *store.Store
	clk        clock.Clock
	publicKey  string
	privateKey string
	subject    string // VAPID JWT 的 sub：mailto:管理员邮箱，或者本站地址
}

// New 读出 VAPID 密钥，第一次启动时生成并写进 kv 表（§5.3）。
// **密钥一旦更换，所有已有订阅都会失效**；它在数据库里，所以跟着备份走（§13.3）。
// subject 是 VAPID 的联系方式：mailto:… 或 https://…。
func New(ctx context.Context, st *store.Store, clk clock.Clock, subject string) (*Service, error) {
	if clk == nil {
		clk = clock.System{}
	}
	s := &Service{st: st, clk: clk, subject: subject}
	if err := s.loadOrCreateVAPID(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// PublicKey 是 VAPID 公钥（base64url），前端订阅时作为 applicationServerKey。
func (s *Service) PublicKey() string { return s.publicKey }

func (s *Service) loadOrCreateVAPID(ctx context.Context) error {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("生成 VAPID 密钥: %w", err)
	}
	// 已经有了就不覆盖（INSERT OR IGNORE），然后统一读出库里的那一对。
	err = s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		for k, v := range map[string]string{kvVAPIDPublic: pub, kvVAPIDPrivate: priv} {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO kv (k, v) VALUES (?, ?)`, k, []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("保存 VAPID 密钥: %w", err)
	}
	var pubB, privB []byte
	if err := s.st.Read().QueryRowContext(ctx, `SELECT v FROM kv WHERE k = ?`, kvVAPIDPublic).Scan(&pubB); err != nil {
		return fmt.Errorf("读取 VAPID 公钥: %w", err)
	}
	if err := s.st.Read().QueryRowContext(ctx, `SELECT v FROM kv WHERE k = ?`, kvVAPIDPrivate).Scan(&privB); err != nil {
		return fmt.Errorf("读取 VAPID 私钥: %w", err)
	}
	s.publicKey, s.privateKey = string(pubB), string(privB)
	return nil
}

// Subscription 是一条推送订阅（浏览器 PushSubscription.toJSON() 的内容）。
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// 业务错误。
var (
	ErrBadSubscription = errors.New("订阅不合法")
	ErrNotFound        = errors.New("订阅不存在")
)

// pushHosts 是允许的推送服务。服务端会往订阅里的 endpoint 发 POST，如果任由客户端填，
// 就能让服务器去请求内网地址（SSRF，比如 127.0.0.1:9090/metrics）。所以只接受主流浏览器的推送服务。
var pushHosts = []string{
	"fcm.googleapis.com",                // Chrome、Edge（安卓）、大多数 Chromium
	"updates.push.services.mozilla.com", // Firefox
	"web.push.apple.com",                // Safari / iOS 主屏幕 PWA
	".push.apple.com",
	".notify.windows.com", // Edge（Windows）
}

func allowedEndpoint(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil {
		return false
	}
	for _, h := range pushHosts {
		if strings.HasPrefix(h, ".") {
			if strings.HasSuffix(host, h) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

// validKey 检查 base64url 编码的密钥：只有 base64url 字符，长度在合理范围内。
func validKey(k string, min, max int) bool {
	if len(k) < min || len(k) > max {
		return false
	}
	for _, c := range k {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '=') {
			return false
		}
	}
	return true
}

// Subscribe 保存（或更新）一条订阅，并记一条 push_enabled 埋点（h5-pwa-plan §8 的提醒开启率）。
// 同一个 endpoint 换了用户或设备（比如认领后在同一个浏览器里），就挂到新的用户和设备上。
func (s *Service) Subscribe(ctx context.Context, userID, deviceID string, sub Subscription) error {
	if !allowedEndpoint(sub.Endpoint) || !validKey(sub.Keys.P256dh, 80, 100) || !validKey(sub.Keys.Auth, 16, 32) {
		return ErrBadSubscription
	}
	now := s.clk.Now().Unix()
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_id, device_id, fail_count, created_at)
			VALUES (?, ?, ?, ?, ?, 0, ?)
			ON CONFLICT(endpoint) DO UPDATE SET
			  p256dh = excluded.p256dh, auth = excluded.auth,
			  user_id = excluded.user_id, device_id = excluded.device_id, fail_count = 0`,
			sub.Endpoint, sub.Keys.P256dh, sub.Keys.Auth, userID, deviceID, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO events (at, user_id, name) VALUES (?, ?, 'push_enabled')`, now, userID)
		return err
	})
}

// Unsubscribe 删除这个用户的一条订阅。
func (s *Service) Unsubscribe(ctx context.Context, userID, endpoint string) error {
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		r, err := tx.ExecContext(ctx,
			`DELETE FROM push_subscriptions WHERE endpoint = ? AND user_id = ?`, endpoint, userID)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// subscriptionsFor 读出一批用户的订阅。
func (s *Service) subscriptionsFor(ctx context.Context, userIDs []string) (map[string][]Subscription, error) {
	out := make(map[string][]Subscription)
	for _, u := range userIDs {
		if _, done := out[u]; done {
			continue
		}
		rows, err := s.st.Read().QueryContext(ctx,
			`SELECT endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = ?`, u)
		if err != nil {
			return nil, err
		}
		var subs []Subscription
		for rows.Next() {
			var sub Subscription
			if err := rows.Scan(&sub.Endpoint, &sub.Keys.P256dh, &sub.Keys.Auth); err != nil {
				rows.Close()
				return nil, err
			}
			subs = append(subs, sub)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		out[u] = subs
	}
	return out, nil
}
