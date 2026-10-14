// Package share 负责分享相关的输出（docs/server-design.md §3、§5.4）：
// 日历文件 .ics、Google Calendar 链接；以后的 /g/:code OG 注入、OG 图和海报也放在这里。
package share

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dayuer/jkt-guandan/internal/games"
	"github.com/dayuer/jkt-guandan/internal/textclean"
)

// Duration 是日历事件的时长。牌局没有结束时间字段，按一场掼蛋的常见时长算。
const Duration = 3 * time.Hour

// ReminderBefore 是日历里的提醒时间。日历是推送不可用时的兜底（方案 §6），提醒要和 t_minus_1h 一致。
const ReminderBefore = time.Hour

// Jakarta 是所有牌局的时区（§13.2）。tzdata 已经编进二进制。
var Jakarta = mustLoad("Asia/Jakarta")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// kindLabel 是牌局类型的中文名。
var kindLabel = map[string]string{
	"classic":  "经典",
	"no-level": "不升级",
	"teaching": "教学局",
}

// Event 是生成日历条目需要的全部信息，从 games.Detail 转出来，测试里也能直接构造。
type Event struct {
	UID      string // 牌局 id，加上本站域名
	Sequence int64  // 牌局版本号：改时间后重新导入，日历更新原有事件而不是新增一条（§5.4）
	Start    time.Time
	Title    string
	Location string
	Details  string
	URL      string // 牌局页面
	Canceled bool
}

// EventFor 把牌局转成日历事件。publicURL 是本站地址（GUANDAN_PUBLIC_URL）。
func EventFor(d *games.Detail, publicURL string) Event {
	g := d.Game
	// 渲染时再清一遍：修复前入库的旧数据里可能已有零宽和 BiDi 字符。
	g.Venue, g.Note = textclean.Clean(g.Venue), textclean.Clean(g.Note)
	hostName := textclean.Clean(d.HostName)
	host := "guandan"
	if u, err := url.Parse(publicURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	link := strings.TrimSuffix(publicURL, "/") + "/g/" + g.ShortCode

	kind := kindLabel[g.Kind]
	if kind == "" {
		kind = g.Kind
	}
	var details []string
	details = append(details, fmt.Sprintf("%s · 局头 %s", kind, hostName))
	if g.Note != "" {
		details = append(details, g.Note)
	}
	details = append(details, link)

	return Event{
		UID:      g.ID + "@" + host,
		Sequence: g.Version,
		Start:    g.StartsAt,
		Title:    "掼蛋 · " + g.Venue,
		Location: g.Venue + "（" + g.Area + "）",
		Details:  strings.Join(details, "\n"),
		URL:      link,
		Canceled: g.Status == games.StatusCancelled,
	}
}

const icsTimeLocal = "20060102T150405"

// ICS 生成一个只含一个事件的日历文件（RFC 5545）。行尾用 CRLF，长行按 75 字节折行，
// 所有文本字段都转义（§10.6：防止在称呼、地点、备注里插入换行，伪造出额外的日历字段）。
// now 是 DTSTAMP。
func ICS(ev Event, now time.Time) []byte {
	var b icsWriter
	b.line("BEGIN:VCALENDAR")
	b.line("VERSION:2.0")
	b.line("PRODID:-//jkt-guandan//guandan//ZH")
	b.line("CALSCALE:GREGORIAN")
	b.line("METHOD:PUBLISH")
	// TZID 引用的时区必须在文件里定义。雅加达是 WIB（UTC+7），没有夏令时。
	b.line("BEGIN:VTIMEZONE")
	b.line("TZID:Asia/Jakarta")
	b.line("BEGIN:STANDARD")
	b.line("DTSTART:19700101T000000")
	b.line("TZOFFSETFROM:+0700")
	b.line("TZOFFSETTO:+0700")
	b.line("TZNAME:WIB")
	b.line("END:STANDARD")
	b.line("END:VTIMEZONE")
	b.line("BEGIN:VEVENT")
	b.line("UID:" + escapeText(ev.UID))
	b.line(fmt.Sprintf("SEQUENCE:%d", ev.Sequence))
	b.line("DTSTAMP:" + now.UTC().Format(icsTimeLocal) + "Z")
	b.line("DTSTART;TZID=Asia/Jakarta:" + ev.Start.In(Jakarta).Format(icsTimeLocal))
	b.line("DTEND;TZID=Asia/Jakarta:" + ev.Start.Add(Duration).In(Jakarta).Format(icsTimeLocal))
	b.line("SUMMARY:" + escapeText(ev.Title))
	b.line("LOCATION:" + escapeText(ev.Location))
	b.line("DESCRIPTION:" + escapeText(ev.Details))
	if ev.URL != "" {
		b.line("URL:" + escapeURI(ev.URL))
	}
	if ev.Canceled {
		b.line("STATUS:CANCELLED")
	} else {
		b.line("STATUS:CONFIRMED")
		b.line("BEGIN:VALARM")
		b.line("ACTION:DISPLAY")
		b.line("DESCRIPTION:" + escapeText(ev.Title))
		b.line(fmt.Sprintf("TRIGGER:-PT%dM", int(ReminderBefore/time.Minute)))
		b.line("END:VALARM")
	}
	b.line("END:VEVENT")
	b.line("END:VCALENDAR")
	return []byte(b.String())
}

// escapeText 按 RFC 5545 §3.3.11 转义 TEXT：反斜杠、分号、逗号、换行；
// 回车和其他控制字符直接去掉（它们没有合法的表示，留着可能被当成行尾）。
func escapeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == ';':
			b.WriteString(`\;`)
		case r == ',':
			b.WriteString(`\,`)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20 || r == 0x7f:
			// 去掉 CR、TAB 以外的控制字符；TAB 也去掉，简单起见。
		case r == utf8.RuneError:
			// 非法 UTF-8 不输出
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeURI 用于 URL 属性（类型 URI，不做 TEXT 转义），只去掉控制字符，防止断行。
func escapeURI(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// icsWriter 负责行尾和折行：RFC 5545 §3.1 要求每行不超过 75 个字节（不含 CRLF），
// 超出时在下一行行首加一个空格续写；折行不能把一个 UTF-8 字符拆开。
type icsWriter struct{ strings.Builder }

func (w *icsWriter) line(s string) {
	const max = 75
	first := true
	for len(s) > 0 {
		limit := max
		if !first {
			limit = max - 1 // 续行开头的空格也算长度
			w.WriteByte(' ')
		}
		if len(s) <= limit {
			w.WriteString(s)
			break
		}
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		w.WriteString(s[:cut])
		w.WriteString("\r\n")
		s = s[cut:]
		first = false
	}
	w.WriteString("\r\n")
}

const googleTime = "20060102T150405Z"

// GoogleCalendarURL 是 Google Calendar 的「新建事件」模板链接（方案 §6）。
func GoogleCalendarURL(ev Event) string {
	q := url.Values{}
	q.Set("action", "TEMPLATE")
	q.Set("text", ev.Title)
	q.Set("dates", ev.Start.UTC().Format(googleTime)+"/"+ev.Start.Add(Duration).UTC().Format(googleTime))
	q.Set("details", ev.Details)
	q.Set("location", ev.Location)
	q.Set("ctz", "Asia/Jakarta")
	return "https://calendar.google.com/calendar/render?" + q.Encode()
}
