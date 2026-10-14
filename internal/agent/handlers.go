package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/games"
	"github.com/dayuer/jkt-guandan/internal/identity"
	"github.com/dayuer/jkt-guandan/internal/ratelimit"
)

// 限流和期限（§12.3：蛋蛋每个用户每天 30 次）。没有身份也能用（07 页先说话、确认时才要称呼），
// 所以按设备和按 IP 各限一道：有设备的按设备算，所有请求再按 IP 算一道粗筛（同一个 Wi-Fi 后面的
// 人多，IP 的额度给宽一些）。
const (
	PerDevicePerDay = 30
	PerIPPerDay     = 60
	// MaxConcurrent 是同时在调模型的请求数上限：每个请求要占一条对外连接十几秒。
	MaxConcurrent = 4
	// DefaultDeadline 是一次请求的总期限。比服务器的 WriteTimeout（30 秒）短，超时时还来得及
	// 告诉前端「蛋蛋走神了」，让它退回表单。
	DefaultDeadline = 20 * time.Second
)

// Options 组装蛋蛋的路由。
type Options struct {
	// Model 为 nil 表示没配 key：POST /api/agent 返回 503 agent_disabled，前端用 08 表单。
	Model    Model
	Identity *identity.Service
	Clock    clock.Clock
	// Deadline 为 0 时用 DefaultDeadline（测试里调短）。
	Deadline time.Duration
}

// Register 挂上 POST /api/agent。路由单独在这里注册，#41 统一路由表时再并进去。
func Register(mux *http.ServeMux, opts Options) {
	mux.HandleFunc("POST /api/agent", newHandler(opts).draft)
}

func newHandler(opts Options) *handler {
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	if opts.Deadline == 0 {
		opts.Deadline = DefaultDeadline
	}
	h := &handler{
		opts:     opts,
		byDevice: ratelimit.New(PerDevicePerDay/(24*60.0), PerDevicePerDay, opts.Clock),
		byIP:     ratelimit.New(PerIPPerDay/(24*60.0), PerIPPerDay, opts.Clock),
		slots:    make(chan struct{}, MaxConcurrent),
	}
	h.device = h.deviceID
	return h
}

type handler struct {
	opts     Options
	byDevice *ratelimit.Limiter
	byIP     *ratelimit.Limiter
	slots    chan struct{}
	device   func(*http.Request) string // 当前设备 id，没有身份为空（测试里替换）
}

func (h *handler) draft(w http.ResponseWriter, r *http.Request) {
	if h.opts.Model == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_disabled", "蛋蛋还没开通，直接填表吧")
		return
	}

	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	text := games.Clean(body.Text)
	if n := utf8.RuneCountInString(text); n == 0 || n > MaxInputRunes {
		writeError(w, http.StatusBadRequest, "invalid_input", fmt.Sprintf("说一句话就行，1–%d 个字", MaxInputRunes))
		return
	}

	// 输入合法才扣额度：空着点发送不算一次。
	if device := h.device(r); device != "" && !h.byDevice.Allow(device) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "今天问蛋蛋的次数用完了，直接填表吧")
		return
	}
	if !h.byIP.Allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "问蛋蛋的人太多了，直接填表吧")
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "agent_busy", "蛋蛋忙不过来，直接填表吧")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.opts.Deadline)
	defer cancel()
	out := &stream{w: w, cancel: cancel}
	err := h.opts.Model.Stream(ctx, systemPrompt(h.opts.Clock.Now()), text, out.add)
	if err == nil && out.tooLong {
		err = errors.New("模型输出超长")
	}
	if err != nil {
		h.fail(w, r, out, err)
		return
	}
	reply, md, err := parseOutput(out.buf.String())
	if err != nil {
		h.fail(w, r, out, err)
		return
	}
	// 回复全程都在分隔符之前，流里已经推过了；模型一个字都没推（比如直接给了分隔符）时补一句。
	if out.sent == 0 && reply == "" {
		out.reply("好，按你说的拟了一局：")
	}
	out.event("draft", validate(md, h.opts.Clock.Now()))
}

// fail 处理模型出错、超时或输出不合格：还没开始推流时回 503 JSON，已经开始推了就发一个 error 事件。
// 两种情况前端都退回 08 表单（§12.5）。
func (h *handler) fail(w http.ResponseWriter, r *http.Request, out *stream, err error) {
	msg := "蛋蛋走神了，直接填表吧"
	reason := "error"
	if errors.Is(err, context.DeadlineExceeded) {
		reason = "timeout"
	}
	if r.Context().Err() != nil {
		return // 用户自己走了，不用回
	}
	slog.Warn("蛋蛋没能出草稿", "reason", reason, "err", err)
	if !out.started {
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", msg)
		return
	}
	out.event("error", map[string]string{"code": "agent_unavailable", "message": msg})
}

func (h *handler) deviceID(r *http.Request) string {
	if h.opts.Identity == nil {
		return ""
	}
	token, ok := h.opts.Identity.Cookies().Read(r)
	if !ok {
		return ""
	}
	ident, err := h.opts.Identity.Current(r.Context(), token)
	if err != nil || ident == nil {
		return ""
	}
	return ident.Device.ID
}

// stream 收模型的输出：分隔符之前的回复边收边推给前端，之后的草稿 JSON 攒着最后解析。
// 第一次推的时候才写响应头，所以模型一开始就失败时还能回一个普通的 503。
type stream struct {
	w       http.ResponseWriter
	cancel  context.CancelFunc
	buf     strings.Builder
	sent    int  // buf 里已经推出去的字节数
	runes   int  // 已经推出去的字数
	done    bool // 已经看到分隔符，后面的不再推
	started bool
	tooLong bool
}

func (s *stream) add(chunk string) {
	s.buf.WriteString(chunk)
	if s.buf.Len() > maxOutputBytes {
		s.tooLong = true
		s.cancel()
		return
	}
	if s.done {
		return
	}
	all := s.buf.String()
	end := len(all)
	if i := strings.Index(all, separator); i >= 0 {
		end, s.done = i, true
	} else {
		// 末尾可能是分隔符的前半截（「=」「==」），先不推，等下一片。
		for k := len(separator) - 1; k > 0; k-- {
			if strings.HasSuffix(all, separator[:k]) {
				end = len(all) - k
				break
			}
		}
		// 末尾的空白也先留着：后面要是分隔符，它就不属于回复。
		for end > s.sent && strings.ContainsRune(" \n\r\t", rune(all[end-1])) {
			end--
		}
	}
	if end <= s.sent {
		return
	}
	piece := all[s.sent:end]
	s.sent = end
	if s.runes == 0 {
		piece = strings.TrimLeft(piece, " \n\r\t")
	}
	if s.done {
		piece = strings.TrimRight(piece, " \n\r\t")
	}
	if room := MaxReplyRunes - s.runes; room <= 0 {
		return
	} else if utf8.RuneCountInString(piece) > room {
		piece = string([]rune(piece)[:room])
	}
	if piece != "" {
		s.reply(piece)
	}
}

func (s *stream) reply(text string) {
	s.runes += utf8.RuneCountInString(text)
	s.event("reply", map[string]string{"text": text})
}

// event 写一条 server-sent event 并立刻刷出去。
func (s *stream) event(name string, data any) {
	if !s.started {
		s.started = true
		hd := s.w.Header()
		hd.Set("Content-Type", "text/event-stream; charset=utf-8")
		hd.Set("Cache-Control", "no-store")
		hd.Set("X-Accel-Buffering", "no") // 让 nginx 别攒着
		s.w.WriteHeader(http.StatusOK)
	}
	b, _ := json.Marshal(data)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b)
	_ = http.NewResponseController(s.w).Flush()
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// clientIP 取对端 IP（httpapi 最外层已经按可信代理换成了真实地址，§12.3）。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
