package realtime_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/dayuer/jkt-guandan/internal/clock"
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

// subscribeGame 订阅一个牌局，读到订阅回执和第一条人数，返回人数。两者的先后不固定（人数由
// 防抖的 flush 发出，可能比回执先到），所以不能先用 subscribe 读回执：它会跳过 presence，把第一条
// 人数吞掉（#74：TestPresenceCountsDistinctDevices 因此偶发超时）。
func subscribeGame(t *testing.T, c *websocket.Conn, topic string) int {
	t.Helper()
	send(t, c, map[string]any{"op": "sub", "topics": []string{topic}})
	acked, viewers := false, -1
	for !acked || viewers < 0 {
		m := readAny(t, c)
		switch m["type"] {
		case "subscribed":
			acked = true
		case realtime.EventPresence:
			if m["topic"] == topic && viewers < 0 {
				viewers = int(m["data"].(map[string]any)["viewers"].(float64))
			}
		}
	}
	return viewers
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
// 用假时钟推进防抖（#74）：每一步先等 Hub 里的人数到位，再推进一个间隔，读到的就是这一步的结果。
func TestPresenceCountsDistinctDevices(t *testing.T) {
	e := newEnv(t, 0, 0)
	clk := clock.NewFake(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	e.hub.SetClock(clk)
	const every = 2 * time.Second
	e.hub.SetPresenceInterval(every)
	const topic = "game:8K2"
	settle := func(want int) {
		t.Helper()
		eventually(t, fmt.Sprintf("人数到 %d", want), func() bool { return e.hub.Viewers(topic) == want })
		clk.Advance(every)
	}

	tab1 := e.dialAs(t, "d1")
	if n := subscribeGame(t, tab1, topic); n != 1 {
		t.Fatalf("第一条应是 1，实际 %d", n)
	}

	// 同一设备的第二个标签页：人数不变，不广播给别人；它自己在下一次 flush 时收到当前人数。
	// 时钟不走就只有回执，推进一个间隔后才是人数，顺序是确定的。
	tab2 := e.dialAs(t, "d1")
	send(t, tab2, map[string]any{"op": "sub", "topics": []string{topic}})
	if m := readAny(t, tab2); m["type"] != "subscribed" {
		t.Fatalf("时钟没走，应先只有回执：%v", m)
	}
	settle(1)
	if m := readAny(t, tab2); m["type"] != realtime.EventPresence || m["data"].(map[string]any)["viewers"] != float64(1) {
		t.Fatalf("新标签页应收到当前人数 1：%v", m)
	}

	other := e.dialAs(t, "d2")
	subscribe(t, other, topic)
	anon1, anon2 := e.dialAs(t, ""), e.dialAs(t, "")
	subscribe(t, anon1, topic)
	subscribe(t, anon2, topic)
	settle(4)
	waitViewers(t, tab1, topic, 4) // d1、d2、两条匿名连接

	// 关掉一个标签页不算少一人；关掉另一台设备才少。
	_ = tab2.Close(websocket.StatusNormalClosure, "")
	_ = other.Close(websocket.StatusNormalClosure, "")
	settle(3)
	waitViewers(t, tab1, topic, 3)

	// 退订也算离开。
	send(t, anon1, map[string]any{"op": "unsub", "topics": []string{topic}})
	settle(2)
	waitViewers(t, tab1, topic, 2)
}

// #46：人数频繁变化时，同一牌局两次广播之间至少隔 presenceInterval，中间的变化合并成一次。
// 用假时钟确定性地推进（#74：原来按墙钟数窗口里的消息，CI 负载高时会不稳定）。
func TestPresenceIsDebounced(t *testing.T) {
	e := newEnv(t, 0, 0)
	clk := clock.NewFake(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	e.hub.SetClock(clk)
	const every = 2 * time.Second
	e.hub.SetPresenceInterval(every)
	const topic = "game:BUSY"

	watcher := e.dialAs(t, "w")
	if n := subscribeGame(t, watcher, topic); n != 1 { // 第一次立即发
		t.Fatalf("第一条应是 1，实际 %d", n)
	}

	// 间隔之内 3 个人进来、走掉 1 个（同一 IP 每秒最多 5 条新连接）：时钟不走，一条都不发。
	var conns []*websocket.Conn
	for i := 0; i < 3; i++ {
		c := e.dialAs(t, "")
		subscribe(t, c, topic)
		conns = append(conns, c)
	}
	_ = conns[0].Close(websocket.StatusNormalClosure, "")
	eventually(t, "人数稳定在 3", func() bool { return e.hub.Viewers(topic) == 3 })

	clk.Advance(every - time.Nanosecond) // 还差一点：不发
	clk.Advance(time.Nanosecond)         // 满一个间隔：合并成一条
	if n := nextViewers(t, watcher); n != 3 {
		t.Fatalf("间隔内的变化应合并成一条 3，实际 %d", n)
	}

	// 下一个间隔：再进来一个，满间隔后发 4。
	// 读到的下一条必须是 4，说明上一条之后没有多发、也没有提前发。
	extra := e.dialAs(t, "")
	subscribe(t, extra, topic)
	clk.Advance(every)
	if n := nextViewers(t, watcher); n != 4 {
		t.Fatalf("下一条应是 4，实际 %d", n)
	}
}

// nextViewers 读到下一条 presence，返回人数（其他消息跳过）。
func nextViewers(t *testing.T, c *websocket.Conn) int {
	t.Helper()
	for {
		m := readAny(t, c)
		if m["type"] == realtime.EventPresence {
			return int(m["data"].(map[string]any)["viewers"].(float64))
		}
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
	subscribeGame(t, watcher, topic)
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
