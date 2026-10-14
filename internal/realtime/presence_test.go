package realtime_test

import (
	"context"
	"encoding/json"
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
	if m := readAny(t, tab2); m["type"] != "subscribed" {
		t.Fatalf("先是订阅回执：%v", m)
	}
	if m := readAny(t, tab2); m["type"] != realtime.EventPresence || m["data"].(map[string]any)["viewers"] != float64(1) {
		t.Fatalf("回执后应紧跟当前人数 1：%v", m)
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
