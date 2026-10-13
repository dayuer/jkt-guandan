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

func (p hubPublisher) KickDevice(deviceID string) { p.hub.KickDevice(deviceID) }

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

func newHubFixture(t *testing.T) *fixture {
	t.Helper()
	hub := realtime.NewHub()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
	})
	return newFixtureWithHub(t, false, hub)
}

// subscribeUser 用某个浏览器连 /ws 并订阅 user:{id}，返回连接。
func (f *fixture) subscribeUser(client *http.Client, userID string) *websocket.Conn {
	f.t.Helper()
	c := f.dialWS(client)
	wsRead(f.t, c, time.Second) // hello
	b, _ := json.Marshal(map[string]any{"op": "sub", "topics": []string{"user:" + userID}})
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		f.t.Fatal(err)
	}
	if m := wsRead(f.t, c, time.Second); m["type"] != "subscribed" || len(m["topics"].([]any)) != 1 {
		f.t.Fatalf("订阅 user:%s：%v", userID, m)
	}
	return c
}

// expectRevoked 断言连接在 1 秒内以 4001 关闭，并且关闭前没有收到任何事件。
func expectRevoked(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err == nil {
		t.Fatalf("身份失效后不该再收到事件：%s", data)
	}
	if code := websocket.CloseStatus(err); code != realtime.StatusIdentityRevoked {
		t.Fatalf("应在 1 秒内以 4001 断开，实际 %d（%v）", code, err)
	}
}

// #43：「退出其他设备」之后，被退出的那台设备（可能是被盗的会话）已经建立的连接立刻断开，
// 之后的认领事件也收不到（§10.3）。
func TestSignedOutDeviceIsDisconnected(t *testing.T) {
	f := newHubFixture(t)
	userID := f.createIdentity(f.http, "老王")
	claim := f.do(http.MethodPost, "/api/claim", nil)
	stolen := f.newBrowser()
	if r := f.doWith(stolen, http.MethodPost, "/api/claim/redeem", map[string]string{"token": claim.str("claim_token")}); r.status != http.StatusOK {
		t.Fatalf("兑换：%d %s", r.status, r.raw)
	}
	var stolenDevice string
	if err := f.st.Read().QueryRow(`SELECT id FROM devices WHERE user_id = ? ORDER BY rowid DESC LIMIT 1`, userID).Scan(&stolenDevice); err != nil {
		t.Fatal(err)
	}
	stolenWS := f.subscribeUser(stolen, userID)
	mineWS := f.subscribeUser(f.http, userID)

	if r := f.do(http.MethodDelete, "/api/me/devices/"+stolenDevice, nil); r.status/100 != 2 {
		t.Fatalf("退出设备：%d %s", r.status, r.raw)
	}
	// 退出之后再发生一次认领：只有自己的设备收得到。
	next := f.do(http.MethodPost, "/api/claim", nil)
	if r := f.doWith(f.newBrowser(), http.MethodPost, "/api/claim/redeem", map[string]string{"token": next.str("claim_token")}); r.status != http.StatusOK {
		t.Fatalf("再次兑换：%d %s", r.status, r.raw)
	}
	expectRevoked(t, stolenWS)
	if m := wsRead(t, mineWS, time.Second); m["type"] != "claim.redeemed" {
		t.Fatalf("自己的设备应照常收到事件：%v", m)
	}
}

// #43：设备确认切换到别的身份之后，它原来以旧身份建立的连接立刻断开。
func TestSwitchedDeviceIsDisconnected(t *testing.T) {
	f := newHubFixture(t)
	f.createIdentity(f.http, "老王")
	claim := f.do(http.MethodPost, "/api/claim", nil)

	other := f.newBrowser()
	lily := f.createIdentity(other, "Lily")
	lilyWS := f.subscribeUser(other, lily)

	r := f.doWith(other, http.MethodPost, "/api/claim/redeem",
		map[string]any{"token": claim.str("claim_token"), "confirm": true})
	if r.status != http.StatusOK {
		t.Fatalf("确认切换：%d %s", r.status, r.raw)
	}
	expectRevoked(t, lilyWS)
}

// 心跳复查：设备还在、还属于握手时的用户才有效。
func TestStillValid(t *testing.T) {
	f := newFixture(t, false)
	userID := f.createIdentity(f.http, "老王")
	var deviceID string
	if err := f.st.Read().QueryRow(`SELECT id FROM devices WHERE user_id = ?`, userID).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if !f.svc.StillValid(ctx, userID, deviceID) {
		t.Fatal("刚建的设备应有效")
	}
	if f.svc.StillValid(ctx, "someone-else", deviceID) {
		t.Fatal("设备不属于这个用户时应无效")
	}
	if f.svc.StillValid(ctx, userID, "no-such-device") {
		t.Fatal("不存在的设备应无效")
	}
}
