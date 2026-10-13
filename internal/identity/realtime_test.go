package identity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/dayuer/jkt-guandan/internal/realtime"
)

type hubPublisher struct{ hub *realtime.Hub }

func (p hubPublisher) Publish(topic, eventType string, data any) {
	p.hub.Publish(realtime.Event{Topic: topic, Type: eventType, Data: data})
}

// dialWS 用某个「浏览器」的 cookie 连 /ws。
func (f *fixture) dialWS(client *http.Client) *websocket.Conn {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(f.srv.URL, "https")+"/ws", &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: http.Header{"Origin": {testOrigin}},
	})
	if err != nil {
		f.t.Fatalf("连 /ws: %v", err)
	}
	f.t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func wsRead(t *testing.T, c *websocket.Conn, within time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("%v 内没有收到消息: %v", within, err)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

// 12 屏（原型 H1）：微信里那一页订阅 user:{id}；在另一个浏览器认领成功后，1 秒内收到 claim.redeemed，
// 页面随即显示「✓ 已在浏览器中打开」。凭码兑换也一样。
func TestClaimRedeemedEventReachesIssuingPage(t *testing.T) {
	for _, via := range []string{"token", "code"} {
		t.Run(via, func(t *testing.T) {
			hub := realtime.NewHub()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = hub.Shutdown(ctx)
			})
			f := newFixtureWithHub(t, false, hub)
			userID := f.createIdentity(f.http, "老王")

			wechat := f.dialWS(f.http)
			if m := wsRead(t, wechat, time.Second); m["type"] != "hello" || m["user_id"] != userID {
				t.Fatalf("hello 应带上 cookie 认出的用户：%v", m)
			}
			b, _ := json.Marshal(map[string]any{"op": "sub", "topics": []string{"user:" + userID}})
			if err := wechat.Write(context.Background(), websocket.MessageText, b); err != nil {
				t.Fatal(err)
			}
			if m := wsRead(t, wechat, time.Second); m["type"] != "subscribed" {
				t.Fatalf("订阅自己的 user 主题：%v", m)
			}

			claim := f.do(http.MethodPost, "/api/claim", nil)
			body := map[string]string{"token": claim.str("claim_token")}
			if via == "code" {
				body = map[string]string{"code": claim.str("code")}
			}
			start := time.Now()
			if r := f.doWith(f.newBrowser(), http.MethodPost, "/api/claim/redeem", body); r.status != http.StatusOK {
				t.Fatalf("兑换失败：%d %s", r.status, r.raw)
			}
			m := wsRead(t, wechat, time.Second)
			if m["type"] != "claim.redeemed" || m["topic"] != "user:"+userID {
				t.Fatalf("应收到 claim.redeemed：%v", m)
			}
			data, _ := m["data"].(map[string]any)
			if data["device_id"] == "" || data["ua"] != "test-browser" {
				t.Fatalf("事件数据 %v", data)
			}
			t.Logf("兑换后 %v 收到事件", time.Since(start).Round(time.Millisecond))
		})
	}
}

// 兑换失败（token 无效）时事务回滚，不发事件。
func TestFailedRedeemPublishesNothing(t *testing.T) {
	hub := realtime.NewHub()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
	})
	f := newFixtureWithHub(t, false, hub)
	userID := f.createIdentity(f.http, "老王")
	ws := f.dialWS(f.http)
	wsRead(t, ws, time.Second) // hello
	b, _ := json.Marshal(map[string]any{"op": "sub", "topics": []string{"user:" + userID}})
	_ = ws.Write(context.Background(), websocket.MessageText, b)
	wsRead(t, ws, time.Second) // subscribed

	if r := f.doWith(f.newBrowser(), http.MethodPost, "/api/claim/redeem",
		map[string]string{"token": "not-a-real-token"}); r.status == http.StatusOK {
		t.Fatal("假 token 不该兑换成功")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, data, err := ws.Read(ctx); err == nil {
		t.Fatalf("兑换失败不该有事件：%s", data)
	}
}
