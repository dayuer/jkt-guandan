package notify

import (
	"context"
	"io"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// SendOptions 是一次推送的参数（RFC 8030）。
type SendOptions struct {
	// Topic 让推送服务合并同一条提醒的多次投递（§5.3：进程崩溃后重发时，设备上只显示一次）。
	Topic string
	// TTL 是推送服务替离线设备保留这条消息的时间。
	TTL time.Duration
}

// Sender 把一条已经加密前的消息发给一个订阅，返回推送服务的 HTTP 状态码。
// 网络错误时 status 为 0、err 非空。测试里换成假的。
type Sender interface {
	Send(ctx context.Context, sub Subscription, payload []byte, opts SendOptions) (status int, err error)
}

// WebPushSender 用 webpush-go 发送：RFC 8291 加密 + RFC 8292 VAPID。
type WebPushSender struct {
	PublicKey, PrivateKey, Subject string
	Client                         *http.Client
}

func (s *WebPushSender) Send(ctx context.Context, sub Subscription, payload []byte, opts SendOptions) (int, error) {
	ws := &webpush.Subscription{Endpoint: sub.Endpoint, Keys: webpush.Keys{P256dh: sub.Keys.P256dh, Auth: sub.Keys.Auth}}
	resp, err := webpush.SendNotificationWithContext(ctx, payload, ws, &webpush.Options{
		HTTPClient:      s.Client,
		Subscriber:      s.Subject,
		Topic:           opts.Topic,
		TTL:             int(opts.TTL / time.Second),
		Urgency:         webpush.UrgencyNormal,
		VAPIDPublicKey:  s.PublicKey,
		VAPIDPrivateKey: s.PrivateKey,
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

// NewSender 用服务的 VAPID 密钥建一个发送器。每个请求 10 秒超时（§5.3、§12.2：只用自己配置过超时的 client）。
func (s *Service) NewSender() *WebPushSender {
	return &WebPushSender{
		PublicKey: s.publicKey, PrivateKey: s.privateKey, Subject: s.subject,
		Client: &http.Client{Timeout: 10 * time.Second},
	}
}
