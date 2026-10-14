package games

import (
	"testing"
	"time"
)

// dayRange 是 02 屏筛选的边界逻辑，直接测函数本身比走 HTTP 精确。
func TestDayRangeTodayAndTomorrow(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, jakarta) // WIB 中午
	today := startOfDay(now)

	from, to := dayRange(now, "today")
	if !from.Equal(today) {
		t.Fatalf("今天：起点应当是当天 00:00，实际 %v", from)
	}
	if !to.Equal(today.AddDate(0, 0, 1)) {
		t.Fatalf("今天：终点应当是次日 00:00，实际 %v", to)
	}

	from, to = dayRange(now, "tomorrow")
	if !from.Equal(today.AddDate(0, 0, 1)) {
		t.Fatalf("明天：起点应当是次日 00:00，实际 %v", from)
	}
	if !to.Equal(today.AddDate(0, 0, 2)) {
		t.Fatalf("明天：终点应当是后天 00:00，实际 %v", to)
	}
}

// 「明天」的区间不能包含今天——`from` 必须真的被用上。
func TestDayRangeTomorrowExcludesToday(t *testing.T) {
	now := time.Date(2026, 10, 14, 23, 0, 0, 0, jakarta)
	todayEvening := now.Add(30 * time.Minute) // 今天 23:30 的一局

	from, _ := dayRange(now, "tomorrow")
	if !todayEvening.Before(from) {
		t.Fatalf("今晚的局不该落进「明天」：%v 不早于 %v", todayEvening, from)
	}
}

// 周末在周六和周日当天都要返回「当前这个」周末，而不是跳到下一个。
func TestDayRangeWeekendOnSaturdayAndSunday(t *testing.T) {
	// 从某天开始往后找第一个周六和第一个周日，不写死日期。
	saturday := time.Date(2026, 1, 1, 12, 0, 0, 0, jakarta)
	for saturday.Weekday() != time.Saturday {
		saturday = saturday.AddDate(0, 0, 1)
	}
	sunday := saturday.AddDate(0, 0, 1)

	for _, day := range []time.Time{saturday, sunday} {
		from, to := dayRange(day, "weekend")
		if from.Weekday() != time.Saturday {
			t.Fatalf("%v 查周末，起点应当是周六，实际 %v", day.Weekday(), from.Weekday())
		}
		if to.Weekday() != time.Monday {
			t.Fatalf("%v 查周末，终点应当是周一，实际 %v", day.Weekday(), to.Weekday())
		}
		// 今天这一整天必须落在区间里。
		if day.Before(from) || !day.Before(to) {
			t.Fatalf("%v 当天的局应当落在「本周末」区间 [%v, %v) 里", day.Weekday(), from, to)
		}
	}
}

// 工作日查周末：返回即将到来的那个周末。
func TestDayRangeWeekendFromWeekday(t *testing.T) {
	wednesday := time.Date(2026, 1, 1, 12, 0, 0, 0, jakarta)
	for wednesday.Weekday() != time.Wednesday {
		wednesday = wednesday.AddDate(0, 0, 1)
	}
	from, _ := dayRange(wednesday, "weekend")
	if from.Weekday() != time.Saturday {
		t.Fatalf("起点应当是周六，实际 %v", from.Weekday())
	}
	if !from.After(wednesday) {
		t.Fatalf("周三查周末，起点应当在未来，实际 %v", from)
	}
}

// 不传 day 时是「未来 7 天」。
func TestDayRangeDefaultIsNextSevenDays(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, jakarta)
	from, to := dayRange(now, "")
	if !from.Equal(now) {
		t.Fatalf("默认起点应当是现在，实际 %v", from)
	}
	if !to.Equal(now.AddDate(0, 0, 7)) {
		t.Fatalf("默认终点应当是 7 天后，实际 %v", to)
	}
}
