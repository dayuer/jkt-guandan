package share

import (
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var start = time.Date(2026, 10, 14, 12, 30, 0, 0, time.UTC) // 雅加达 19:30
var stamp = time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)

func sample() Event {
	return Event{
		UID:      "g1@guandan.synon.ai",
		Sequence: 3,
		Start:    start,
		Title:    "掼蛋 · Pacific Place · 鼎泰丰包间",
		Location: "Pacific Place · 鼎泰丰包间（SCBD）",
		Details:  "经典 · 局头 老王\n打到 A 为止，新手友好。停车 P2。\nhttps://guandan.synon.ai/g/SEED01",
		URL:      "https://guandan.synon.ai/g/SEED01",
	}
}

// unfold 按 RFC 5545 还原折行，返回逻辑行。
func unfold(t *testing.T, ics string) []string {
	t.Helper()
	if !strings.HasSuffix(ics, "\r\n") {
		t.Fatal("文件应以 CRLF 结尾")
	}
	var lines []string
	for _, phys := range strings.Split(strings.TrimSuffix(ics, "\r\n"), "\r\n") {
		if len(phys) > 75 {
			t.Fatalf("物理行超过 75 字节（%d）：%q", len(phys), phys)
		}
		if !utf8.ValidString(phys) {
			t.Fatalf("折行拆开了 UTF-8 字符：%q", phys)
		}
		if strings.ContainsAny(phys, "\r\n") {
			t.Fatalf("行里出现了裸的 CR/LF：%q", phys)
		}
		if strings.HasPrefix(phys, " ") {
			lines[len(lines)-1] += phys[1:]
			continue
		}
		lines = append(lines, phys)
	}
	return lines
}

func prop(lines []string, name string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, name+":") || strings.HasPrefix(l, name+";") {
			out = append(out, l)
		}
	}
	return out
}

func TestICSStructure(t *testing.T) {
	lines := unfold(t, string(ICS(sample(), stamp)))
	for want, n := range map[string]int{
		"BEGIN:VCALENDAR": 1, "BEGIN:VTIMEZONE": 1, "TZID:Asia/Jakarta": 1, "BEGIN:VEVENT": 1,
		"UID:g1@guandan.synon.ai": 1, "SEQUENCE:3": 1, "DTSTAMP:20261013T080000Z": 1,
		"DTSTART;TZID=Asia/Jakarta:20261014T193000": 1, // UTC 12:30 = 雅加达 19:30
		"DTEND;TZID=Asia/Jakarta:20261014T223000":   1, // 三个小时
		"STATUS:CONFIRMED":                          1, "BEGIN:VALARM": 1, "TRIGGER:-PT60M": 1, "END:VCALENDAR": 1,
	} {
		got := 0
		for _, l := range lines {
			if l == want {
				got++
			}
		}
		if got != n {
			t.Errorf("「%s」出现 %d 次，期望 %d", want, got, n)
		}
	}
	if d := prop(lines, "DESCRIPTION"); len(d) != 2 || !strings.Contains(d[0], `局头 老王\n打到 A 为止，新手友好。`) {
		t.Errorf("DESCRIPTION 转义不对：%v", d)
	}
}

func TestICSCancelledHasNoAlarm(t *testing.T) {
	ev := sample()
	ev.Canceled = true
	lines := unfold(t, string(ICS(ev, stamp)))
	if len(prop(lines, "STATUS")) != 1 || prop(lines, "STATUS")[0] != "STATUS:CANCELLED" {
		t.Fatalf("取消的局应是 STATUS:CANCELLED：%v", prop(lines, "STATUS"))
	}
	if len(prop(lines, "TRIGGER")) != 0 {
		t.Fatal("取消的局不该再提醒")
	}
}

// §10.6：地点、称呼、备注都是用户输入；插入换行也伪造不出额外的日历字段。
func TestICSInjection(t *testing.T) {
	ev := sample()
	ev.Location = "Kemang\r\nATTENDEE:mailto:evil@example.com\nX-INJECT:1"
	ev.Title = "掼蛋 · a;b,c\\d"
	ev.Details = "line1\r\nEND:VEVENT\r\nBEGIN:VEVENT"
	lines := unfold(t, string(ICS(ev, stamp)))
	for _, bad := range []string{"ATTENDEE", "X-INJECT"} {
		if len(prop(lines, bad)) != 0 {
			t.Fatalf("注入出了 %s：%v", bad, lines)
		}
	}
	if n := len(prop(lines, "BEGIN")); n != 5 { // VCALENDAR、VTIMEZONE、STANDARD、VEVENT、VALARM
		t.Fatalf("BEGIN 行数 %d，备注里的 BEGIN:VEVENT 不该变成真的组件", n)
	}
	if got := prop(lines, "SUMMARY")[0]; got != `SUMMARY:掼蛋 · a\;b\,c\\d` {
		t.Fatalf("SUMMARY 转义：%s", got)
	}
	if got := prop(lines, "LOCATION")[0]; got != `LOCATION:Kemang\nATTENDEE:mailto:evil@example.com\nX-INJECT:1` {
		t.Fatalf("LOCATION 转义：%s", got)
	}
}

// 长的中文备注要按字节折行，并且不能把一个汉字拆开。
func TestICSFoldsLongUTF8(t *testing.T) {
	ev := sample()
	ev.Details = strings.Repeat("打到A为止新手友好，", 20)
	lines := unfold(t, string(ICS(ev, stamp))) // unfold 里检查每个物理行 ≤75 字节、是合法 UTF-8
	want := "DESCRIPTION:" + escapeText(ev.Details)
	if got := prop(lines, "DESCRIPTION")[0]; got != want {
		t.Fatalf("折行后还原不一致：\n%s\n%s", got, want)
	}
}

// §9：.ics 转义要有模糊测试。任意输入都不能多出属性、不能破坏行结构。
func FuzzICS(f *testing.F) {
	f.Add("Pacific Place", "老王", "打到 A 为止")
	f.Add("a\r\nATTENDEE:x", "b;c,d\\", "\n\nEND:VEVENT")
	f.Add(strings.Repeat("鼎", 40), "\x00\x7f", "\xff\xfe")
	f.Fuzz(func(t *testing.T, venue, host, note string) {
		ev := sample()
		ev.Title = "掼蛋 · " + venue
		ev.Location = venue
		ev.Details = host + "\n" + note
		lines := unfold(t, string(ICS(ev, stamp)))
		// 属性名只能是固定的这些。
		allowed := map[string]bool{}
		for _, n := range []string{"BEGIN", "END", "VERSION", "PRODID", "CALSCALE", "METHOD", "TZID",
			"DTSTART", "TZOFFSETFROM", "TZOFFSETTO", "TZNAME", "UID", "SEQUENCE", "DTSTAMP", "DTEND",
			"SUMMARY", "LOCATION", "DESCRIPTION", "URL", "STATUS", "ACTION", "TRIGGER"} {
			allowed[n] = true
		}
		for _, l := range lines {
			name := l
			if i := strings.IndexAny(l, ":;"); i >= 0 {
				name = l[:i]
			}
			if !allowed[name] {
				t.Fatalf("出现了不该有的属性 %q（输入 %q %q %q）", name, venue, host, note)
			}
		}
		if len(lines) != 32 {
			t.Fatalf("逻辑行数 %d，应固定为 32", len(lines))
		}
	})
}

func TestGoogleCalendarURL(t *testing.T) {
	u, err := url.Parse(GoogleCalendarURL(sample()))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "calendar.google.com" || q.Get("action") != "TEMPLATE" {
		t.Fatalf("链接 %s", u)
	}
	if q.Get("dates") != "20261014T123000Z/20261014T153000Z" || q.Get("ctz") != "Asia/Jakarta" {
		t.Fatalf("时间 %s %s", q.Get("dates"), q.Get("ctz"))
	}
	if q.Get("text") != sample().Title || !strings.Contains(q.Get("details"), "局头 老王") {
		t.Fatalf("内容 %v", q)
	}
}
