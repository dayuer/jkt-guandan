package notify

import (
	"fmt"
	"time"
)

var jakarta = mustLoad("Asia/Jakarta")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// Payload 是推给 Service Worker 的 JSON（#10 的前端按它显示通知）。
// tag = dedupe_key：同一条提醒重复送达时，设备上只显示一次（§5.3）。
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
	URL   string `json:"url"`
	Kind  string `json:"kind"`
	Game  string `json:"game"`
}

// gameInfo 是生成文案需要的牌局信息，发送前从库里读出当前状态。
type gameInfo struct {
	Code     string
	Venue    string
	StartsAt time.Time
	Missing  int
}

// when 是「10月14日 19:30」这样的雅加达时间。
func when(t time.Time) string {
	l := t.In(jakarta)
	return fmt.Sprintf("%d月%d日 %02d:%02d", l.Month(), l.Day(), l.Hour(), l.Minute())
}

// message 按提醒类型（games.Reminder*，§5.3 的表）生成标题和正文。
func message(kind string, g gameInfo) (title, body string) {
	at := when(g.StartsAt) + " · " + g.Venue
	switch kind {
	case "t_minus_1h":
		return "还有 1 小时开局", at
	case "full":
		return "成局了！4 人已齐", at
	case "seat_left":
		return "有人退出了，还差 " + fmt.Sprint(g.Missing) + " 人", at
	case "promoted":
		return "你从候补补上座位了", at
	case "host_seat_taken":
		return "有人报名了你的局", at
	case "host_waitlist":
		return "有人进了你这局的候补", at
	case "host_transferred":
		return "你成了这局的局头", at
	case "host_changed":
		return "这局换局头了", at
	case "rescheduled":
		return "局头改了时间", "改到 " + at
	case "cancelled":
		return "这局取消了", at
	case "kicked":
		return "局头把你移出了这局", at
	case "invited":
		return "有人邀请你打掼蛋", at
	default:
		return "掼蛋局有更新", at
	}
}
