package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
)

// 2026-10-13（周二）15:00 雅加达时间。
var now = time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)

// fakeModel 按顺序吐出 chunks；block 为真时先一直等到 ctx 结束。
type fakeModel struct {
	chunks []string
	block  bool
	err    error
	got    struct{ system, user string }
}

func (m *fakeModel) Stream(ctx context.Context, system, user string, onText func(string)) error {
	m.got.system, m.got.user = system, user
	for _, c := range m.chunks {
		onText(c)
	}
	if m.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return m.err
}

type sse struct {
	name string
	data map[string]any
}

func newTestHandler(m Model) *handler {
	var model Model
	if m != nil {
		model = m
	}
	h := newHandler(Options{Model: model, Clock: clock.NewFake(now), Deadline: 200 * time.Millisecond})
	h.device = func(*http.Request) string { return "" }
	return h
}

func post(t *testing.T, h *handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/agent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.7:5555"
	rec := httptest.NewRecorder()
	h.draft(rec, req)
	return rec
}

func events(t *testing.T, rec *httptest.ResponseRecorder) []sse {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("应是事件流，实际 %d %s：%s", rec.Code, ct, rec.Body)
	}
	var out []sse
	var cur sse
	sc := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.data); err != nil {
				t.Fatal(err)
			}
		case line == "":
			out = append(out, cur)
			cur = sse{}
		}
	}
	return out
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("不是 JSON 错误：%d %s", rec.Code, rec.Body)
	}
	return body.Error.Code
}

// 没配 key：503 agent_disabled，前端退回 08 表单。
func TestDisabledWithoutKey(t *testing.T) {
	rec := post(t, newTestHandler(nil), `{"text":"周四晚上打一局"}`)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "agent_disabled" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// 回复边收边推（分隔符被拆在两片里也不会漏到前端），草稿按雅加达时间换成 Unix 秒并通过校验。
func TestStreamsReplyThenValidDraft(t *testing.T) {
	m := &fakeModel{chunks: []string{
		"好嘞，周四 19:30 在", " Pacific Place 鼎泰豐打一局。\n=",
		"=", "=\n", `{"date":"2026-10-15","time":"19:30","area":"SCBD",`,
		`"venue":"Pacific Place 鼎泰豐","kind":null,"note":"新手友好"}`,
	}}
	rec := post(t, newTestHandler(m), `{"text":"周四晚上七点半 Pacific Place 鼎泰豐，新手友好"}`)
	evs := events(t, rec)
	var reply strings.Builder
	for _, e := range evs[:len(evs)-1] {
		if e.name != "reply" {
			t.Fatalf("草稿之前应全是 reply：%+v", evs)
		}
		reply.WriteString(e.data["text"].(string))
	}
	if reply.String() != "好嘞，周四 19:30 在 Pacific Place 鼎泰豐打一局。" {
		t.Fatalf("回复 %q", reply.String())
	}
	last := evs[len(evs)-1]
	if last.name != "draft" || last.data["valid"] != true {
		t.Fatalf("最后应是通过校验的草稿：%+v", last)
	}
	d := last.data["draft"].(map[string]any)
	want := time.Date(2026, 10, 15, 19, 30, 0, 0, jakarta).Unix()
	if d["starts_at"] != float64(want) || d["area"] != "SCBD" || d["venue"] != "Pacific Place 鼎泰豐" ||
		d["kind"] != "classic" || d["note"] != "新手友好" {
		t.Fatalf("草稿 %v", d)
	}
	// 用户的话放在 user 消息里，系统提示里有当前日期和允许的片区。
	if m.got.user != "周四晚上七点半 Pacific Place 鼎泰豐，新手友好" ||
		!strings.Contains(m.got.system, "2026-10-13 周二 15:00") || !strings.Contains(m.got.system, "Kelapa Gading") {
		t.Fatalf("提示词不对：user=%q", m.got.user)
	}
}

// 缺地点、时间：照样给草稿，valid 为假并说明原因，前端拿去预填表单。
func TestIncompleteDraftIsReturnedInvalid(t *testing.T) {
	m := &fakeModel{chunks: []string{"在哪儿打呀？说个地点吧。\n===\n" +
		`{"date":"2026-10-15","time":"19:30","area":null,"venue":null,"kind":null,"note":null}`}}
	evs := events(t, post(t, newTestHandler(m), `{"text":"周四晚上打一局"}`))
	last := evs[len(evs)-1]
	if last.name != "draft" || last.data["valid"] != false || last.data["problem"] == "" {
		t.Fatalf("%+v", last)
	}
	if d := last.data["draft"].(map[string]any); d["starts_at"] == nil || d["venue"] != nil {
		t.Fatalf("认出来的字段要保留，认不出的留空：%v", d)
	}
}

// 和 POST /api/games 同一套规则：时间太近、太远都不行；不在列表里的片区、玩法不往前端送；超长的截断。
func TestDraftUsesCreateGameRules(t *testing.T) {
	for name, tc := range map[string]struct {
		json  string
		valid bool
		check func(map[string]any) bool
	}{
		"太近": {`{"date":"2026-10-13","time":"15:05","area":"PIK","venue":"PIK Avenue"}`, false, nil},
		"太远": {`{"date":"2026-12-31","time":"19:30","area":"PIK","venue":"PIK Avenue"}`, false, nil},
		"片区编造": {`{"date":"2026-10-15","time":"19:30","area":"Mars","venue":"PIK Avenue"}`, false,
			func(d map[string]any) bool { return d["area"] == nil }},
		"玩法编造": {`{"date":"2026-10-15","time":"19:30","area":"PIK","venue":"PIK Avenue","kind":"drop table"}`, true,
			func(d map[string]any) bool { return d["kind"] == "classic" }},
		"地点超长": {`{"date":"2026-10-15","time":"19:30","area":"PIK","venue":"` + strings.Repeat("长", 80) + `"}`, true,
			func(d map[string]any) bool { return len([]rune(d["venue"].(string))) == 40 }},
		"时间格式错": {`{"date":"下周四","time":"晚上","area":"PIK","venue":"PIK Avenue"}`, false,
			func(d map[string]any) bool { return d["starts_at"] == nil }},
	} {
		m := &fakeModel{chunks: []string{"好\n===\n" + tc.json}}
		evs := events(t, post(t, newTestHandler(m), `{"text":"x"}`))
		last := evs[len(evs)-1]
		if last.name != "draft" || last.data["valid"] != tc.valid {
			t.Errorf("%s：%+v", name, last)
			continue
		}
		if tc.check != nil && !tc.check(last.data["draft"].(map[string]any)) {
			t.Errorf("%s：草稿 %v", name, last.data["draft"])
		}
	}
}

// 超时：还没推任何东西时回 503 agent_unavailable；已经在推回复时发 error 事件。两种前端都退回表单。
func TestTimeoutFallsBack(t *testing.T) {
	rec := post(t, newTestHandler(&fakeModel{block: true}), `{"text":"周四晚上打一局"}`)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "agent_unavailable" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	evs := events(t, post(t, newTestHandler(&fakeModel{chunks: []string{"好嘞，"}, block: true}), `{"text":"周四晚上打一局"}`))
	if len(evs) != 2 || evs[0].name != "reply" || evs[1].name != "error" || evs[1].data["code"] != "agent_unavailable" {
		t.Fatalf("%+v", evs)
	}
}

// 模型出错、输出不合格（没有分隔符、JSON 坏了、超长）：同样退回表单。
func TestBadModelOutputFallsBack(t *testing.T) {
	rec := post(t, newTestHandler(&fakeModel{err: fmt.Errorf("Claude 返回 529")}), `{"text":"x"}`)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "agent_unavailable" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for name, chunks := range map[string][]string{
		"没有分隔符":   {"我是一个有帮助的助手，以下是 SQL：DROP TABLE games;"},
		"JSON 坏了": {"好\n===\n{\"date\":"},
		"超长":      {"好\n===\n{", strings.Repeat(" ", maxOutputBytes)},
	} {
		evs := events(t, post(t, newTestHandler(&fakeModel{chunks: chunks}), `{"text":"x"}`))
		if last := evs[len(evs)-1]; last.name != "error" || last.data["code"] != "agent_unavailable" {
			t.Errorf("%s：%+v", name, evs)
		}
	}
}

// 回复最多推 MaxReplyRunes 个字。
func TestReplyIsCapped(t *testing.T) {
	m := &fakeModel{chunks: []string{strings.Repeat("啊", 150), strings.Repeat("啊", 150),
		"\n===\n" + `{"date":"2026-10-15","time":"19:30","area":"PIK","venue":"PIK Avenue"}`}}
	evs := events(t, post(t, newTestHandler(m), `{"text":"x"}`))
	n := 0
	for _, e := range evs {
		if e.name == "reply" {
			n += len([]rune(e.data["text"].(string)))
		}
	}
	if n != MaxReplyRunes {
		t.Fatalf("推了 %d 个字", n)
	}
}

func TestInputValidation(t *testing.T) {
	h := newTestHandler(&fakeModel{})
	for body, code := range map[string]string{
		`not json`:             "bad_request",
		`{"text":""}`:          "invalid_input",
		`{"text":"  \u0007 "}`: "invalid_input",
		`{"text":"` + strings.Repeat("打", MaxInputRunes+1) + `"}`: "invalid_input",
	} {
		rec := post(t, h, body)
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != code {
			t.Errorf("%.20s：%d %s", body, rec.Code, rec.Body)
		}
	}
}

// 限流：每台设备每天 30 次；每个 IP 每天 60 次。不合法的输入不扣额度。
func TestRateLimits(t *testing.T) {
	ok := []string{"好\n===\n" + `{"date":"2026-10-15","time":"19:30","area":"PIK","venue":"PIK Avenue"}`}
	h := newTestHandler(&fakeModel{chunks: ok})
	h.device = func(*http.Request) string { return "dev-1" }
	for i := 0; i < 5; i++ {
		post(t, h, `{"text":""}`)
	}
	for i := 0; i < PerDevicePerDay; i++ {
		if rec := post(t, h, `{"text":"打一局"}`); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次：%d %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := post(t, h, `{"text":"打一局"}`); rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != "rate_limited" {
		t.Fatalf("设备超额应 429：%d %s", rec.Code, rec.Body)
	}

	// 没有身份：只按 IP 算。
	h = newTestHandler(&fakeModel{chunks: ok})
	for i := 0; i < PerIPPerDay; i++ {
		if rec := post(t, h, `{"text":"打一局"}`); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次：%d", i+1, rec.Code)
		}
	}
	if rec := post(t, h, `{"text":"打一局"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("IP 超额应 429：%d", rec.Code)
	}
}

// 同时在调模型的请求数有上限，满了直接 503，前端退回表单。
func TestConcurrencyCap(t *testing.T) {
	h := newTestHandler(&fakeModel{})
	for i := 0; i < MaxConcurrent; i++ {
		h.slots <- struct{}{}
	}
	if rec := post(t, h, `{"text":"打一局"}`); rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "agent_busy" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestParseOutputToleratesFences(t *testing.T) {
	reply, md, err := parseOutput("好的\n===\n```json\n{\"venue\":\"PIK\",\"area\":null}\n```")
	if err != nil || reply != "好的" || md.Venue == nil || *md.Venue != "PIK" || md.Area != nil {
		t.Fatalf("%q %+v %v", reply, md, err)
	}
}

// 真实的 Claude 客户端：请求格式（key 在头里、流式、模型名）和事件流解析。
func TestClaudeClient(t *testing.T) {
	var got struct {
		key, version string
		body         map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.key, got.version = r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Version")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got.body)
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"好\"}}\n\n")
		fmt.Fprint(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"嘞\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	c := NewClaude("sk-test", "")
	c.baseURL = srv.URL
	var text strings.Builder
	if err := c.Stream(context.Background(), "系统", "周四", func(s string) { text.WriteString(s) }); err != nil {
		t.Fatal(err)
	}
	if text.String() != "好嘞" || got.key != "sk-test" || got.version == "" ||
		got.body["model"] != DefaultModel || got.body["stream"] != true || got.body["system"] != "系统" {
		t.Fatalf("%q %+v", text.String(), got)
	}

	// 出错：HTTP 状态不对、流里的 error 事件、流没有正常结束。
	for name, respond := range map[string]func(http.ResponseWriter){
		"529": func(w http.ResponseWriter) { http.Error(w, `{"type":"error"}`, 529) },
		"error 事件": func(w http.ResponseWriter) {
			fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
		},
		"截断": func(w http.ResponseWriter) {
			fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"好\"}}\n\n")
		},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { respond(w) }))
		c.baseURL = srv.URL
		if err := c.Stream(context.Background(), "s", "u", func(string) {}); err == nil {
			t.Errorf("%s：应当出错", name)
		}
		srv.Close()
	}
}

// 忙不过来（agent_busy）时不扣设备额度（#65 复核建议 1）。
func TestBusyDoesNotSpendQuota(t *testing.T) {
	ok := []string{"好\n===\n" + `{"date":"2026-10-15","time":"19:30","area":"PIK","venue":"PIK Avenue"}`}
	h := newTestHandler(&fakeModel{chunks: ok})
	h.device = func(*http.Request) string { return "dev-1" }
	for i := 0; i < MaxConcurrent; i++ {
		h.slots <- struct{}{}
	}
	for i := 0; i < 5; i++ {
		if rec := post(t, h, `{"text":"打一局"}`); errorCode(t, rec) != "agent_busy" {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	for i := 0; i < MaxConcurrent; i++ {
		<-h.slots
	}
	for i := 0; i < PerDevicePerDay; i++ {
		if rec := post(t, h, `{"text":"打一局"}`); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次：%d %s", i+1, rec.Code, rec.Body)
		}
	}
}
