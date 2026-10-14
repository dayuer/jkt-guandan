package realtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/dayuer/jkt-guandan/internal/realtime"
)

// dialAs 以某台设备（device 为空表示没有身份）连上来并读掉 hello。
func (e *env) dialAs(t *testing.T, device string) *websocket.Conn {
	t.Helper()
	user := ""
	if device != "" {
		user = "u-" + device
	}
	c, _, err := e.dialDevice(t, user, device, site)
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	if m := readAny(t, c); m["type"] != "hello" {
		t.Fatalf("第一条应是 hello，实际 %v", m)
	}
	return c
}

// waitViewers 一直读到一条 presence 说 viewers == want。
func waitViewers(t *testing.T, c *websocket.Conn, topic string, want int) {
	t.Helper()
	for {
		m := readAny(t, c)
		if m["type"] != realtime.EventPresence {
			continue
		}
		if m["topic"] != topic {
			t.Fatalf("presence 主题不对：%v", m)
		}
		if _, hasV := m["v"]; hasV {
			t.Fatalf("presence 不该带版本号：%v", m)
		}
		if n := m["data"].(map[string]any)["viewers"]; n == float64(want) {
			return
		}
	}
}

// #46：按设备计数，同一设备多个标签页算一个；没有身份的连接每条算一个。
func TestPresenceCountsDistinctDevices(t *testing.T) {
	e := newEnv(t, 0, 0)
	e.hub.SetPresenceInterval(20 * time.Millisecond)
	const topic = "game:8K2"

	tab1 := e.dialAs(t, "d1")
	subscribe(t, tab1, topic)
	waitViewers(t, tab1, topic, 1)

	// 同一设备的第二个标签页：人数不变，不广播，但它自己订阅后马上收到当前人数。
	tab2 := e.dialAs(t, "d1")
	send(t, tab2, map[string]any{"op": "sub", "topics": []string{topic}})
	gotAck, gotViewers := false, false
	for !gotAck || !gotViewers {
		m := readAny(t, tab2)
		switch {
		case m["type"] == "subscribed" && !gotAck:
			gotAck = true
		case m["type"] == realtime.EventPresence && !gotViewers && m["data"].(map[string]any)["viewers"] == float64(1):
			gotViewers = true
		default:
			t.Fatalf("订阅后应只收到回执和一条当前人数 1：%v", m)
		}
	}
	if n := e.hub.Viewers(topic); n != 1 {
		t.Fatalf("同一设备两个标签页应算 1 人，实际 %d", n)
	}

	other := e.dialAs(t, "d2")
	subscribe(t, other, topic)
	anon1, anon2 := e.dialAs(t, ""), e.dialAs(t, "")
	subscribe(t, anon1, topic)
	subscribe(t, anon2, topic)
	waitViewers(t, tab1, topic, 4) // d1、d2、两条匿名连接

	// 关掉一个标签页不算少一人；关掉另一台设备才少。
	_ = tab2.Close(websocket.StatusNormalClosure, "")
	_ = other.Close(websocket.StatusNormalClosure, "")
	waitViewers(t, tab1, topic, 3)
	eventually(t, "人数降到 3", func() bool { return e.hub.Viewers(topic) == 3 })

	// 退订也算离开。
	send(t, anon1, map[string]any{"op": "unsub", "topics": []string{topic}})
	waitViewers(t, tab1, topic, 2)
}

// #46：人数频繁变化时，同一牌局两次广播之间至少隔 presenceInterval，中间的变化合并成一次。
func TestPresenceIsDebounced(t *testing.T) {
	e := newEnv(t, 0, 0)
	const every = 300 * time.Millisecond
	e.hub.SetPresenceInterval(every)
	const topic = "game:BUSY"

	watcher := e.dialAs(t, "w")
	subscribe(t, watcher, topic)
	waitViewers(t, watcher, topic, 1) // 第一次立即发

	// 窗口内 4 个人进来又走掉 2 个（同一 IP 每秒最多 5 条新连接）。
	var conns []*websocket.Conn
	for i := 0; i < 4; i++ {
		c := e.dialAs(t, "")
		subscribe(t, c, topic)
		conns = append(conns, c)
	}
	for _, c := range conns[:2] {
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
	eventually(t, "人数稳定在 3", func() bool { return e.hub.Viewers(topic) == 3 })

	// 收集接下来 2.5 个间隔里的 presence：只该有 1～2 条，且相隔不短于间隔，最后一条是 3。
	var got []int
	var at []time.Time
	ctx, cancel := context.WithTimeout(context.Background(), every*5/2)
	defer cancel()
	for {
		_, data, err := watcher.Read(ctx)
		if err != nil {
			break
		}
		var m struct {
			Type string `json:"type"`
			Data struct {
				Viewers int `json:"viewers"`
			} `json:"data"`
		}
		_ = json.Unmarshal(data, &m)
		if m.Type == realtime.EventPresence {
			got = append(got, m.Data.Viewers)
			at = append(at, time.Now())
		}
	}
	if len(got) == 0 || len(got) > 2 || got[len(got)-1] != 3 {
		t.Fatalf("窗口内的变化应合并成 1～2 条、最后是 3，实际 %v", got)
	}
	if len(at) == 2 && at[1].Sub(at[0]) < every*8/10 {
		t.Fatalf("两次广播只隔了 %v", at[1].Sub(at[0]))
	}
}

// presence 只给牌局主题：首页 feed 不广播人数。
func TestFeedHasNoPresence(t *testing.T) {
	e := newEnv(t, 0, 0)
	e.hub.SetPresenceInterval(10 * time.Millisecond)
	c := e.dialAs(t, "d1")
	subscribe(t, c, "feed:SCBD")
	time.Sleep(50 * time.Millisecond)
	e.hub.Publish(realtime.Event{Topic: "feed:SCBD", Type: "feed.game_added"})
	if m := readAny(t, c); m["type"] != "feed.game_added" {
		t.Fatalf("feed 主题不该收到 presence：%v", m)
	}
}

// 被踢下线（4001）的设备马上不算在看的人：踢的那一刻就不计，不等连接真正关掉。
func TestPresenceDropsKickedDevice(t *testing.T) {
	e := newEnv(t, 0, 0)
	e.hub.SetPresenceInterval(20 * time.Millisecond)
	const topic = "game:KICK"
	watcher := e.dialAs(t, "w")
	subscribe(t, watcher, topic)
	victim := e.dialAs(t, "v")
	subscribe(t, victim, topic)
	waitViewers(t, watcher, topic, 2)

	e.hub.KickDevice("v")
	if n := e.hub.Viewers(topic); n != 1 {
		t.Fatalf("踢掉之后应只剩 1 人，实际 %d", n)
	}
	waitViewers(t, watcher, topic, 1)
}

// 一条 sub 订满 20 个牌局：不会因为 presence 把发送队列写满而被踢掉，每个牌局恰好收到一条人数
// （#62 复审：原来问候一条、立即广播一条，最多 42 条挤进 32 格的队列）。
func TestSubscribeManyGamesAtOnce(t *testing.T) {
	e := newEnv(t, 0, 0)
	e.hub.SetPresenceInterval(10 * time.Millisecond)
	c := e.dialAs(t, "d1")
	topics := make([]string, realtime.MaxTopicsPerConn)
	for i := range topics {
		topics[i] = fmt.Sprintf("game:MANY%02d", i)
	}
	send(t, c, map[string]any{"op": "sub", "topics": topics})

	got := map[string]int{}
	acked := false
	for !acked || len(got) < len(topics) {
		m := readAny(t, c)
		switch m["type"] {
		case "subscribed":
			acked = true
		case realtime.EventPresence:
			topic := m["topic"].(string)
			got[topic]++
			if n := m["data"].(map[string]any)["viewers"]; n != float64(1) || got[topic] > 1 {
				t.Fatalf("%s：第 %d 条，viewers=%v", topic, got[topic], n)
			}
		default:
			t.Fatalf("意外的消息：%v", m)
		}
	}
	// 之后不该再有重复的 presence；连接还活着：再发一个 sub 能收到回执。
	time.Sleep(50 * time.Millisecond)
	send(t, c, map[string]any{"op": "sub", "topics": []string{topics[0]}})
	if m := readAny(t, c); m["type"] != "subscribed" {
		t.Fatalf("应只剩回执，实际 %v", m)
	}
}
