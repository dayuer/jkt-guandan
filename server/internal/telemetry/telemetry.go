// Package telemetry 是埋点和前端错误上报（#117，h5-pwa-plan §8）。
//
// 两个接口都允许没有身份：落地页、详情页在报名前就要埋点。所以限流按 IP 和按设备各来一道，
// 请求体很小，事件名、环境、属性都走白名单，不收自由文本，不采集个人信息。
package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/ratelimit"
	"github.com/dayuer/jkt-guandan/server/internal/route"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// 限额与大小上限。
const (
	// MaxBodyBytes 是两个接口的请求体上限（比全局的 64 KB 小得多）。
	MaxBodyBytes = 8 << 10
	// MaxBatch 是一次最多收几条事件。
	MaxBatch = 20

	// 主要按设备限：办公室和运营商的出口 NAT 后面一个 IP 有很多人，IP 限得太死会丢漏斗数据，
	// 所以 IP 这一道放宽，只防单个来源刷爆。
	eventsPerDevicePerMin = 30
	eventsPerIPPerMin     = 120
	errorsPerDevicePerMin = 6
	errorsPerIPPerMin     = 60
	// 全站每小时最多存多少条前端错误：防止有人轮换设备把磁盘灌满。
	errorsGlobalPerHour = 5000

	maxMessageRunes = 300
	maxStackRunes   = 2000
	maxPathRunes    = 200

	errorEventName = "client_error"
)

// Envs 是允许的运行环境。
var Envs = []string{"wechat", "whatsapp", "browser", "pwa"}

// channels 是 share_channel 允许的分享渠道。
var channels = []string{"whatsapp", "wechat", "copy", "system", "poster"}

// gameCodeRE 是牌局短码的形状（6 位 Crockford base32）。
var gameCodeRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{6}$`)

// Options 组装埋点的路由。
type Options struct {
	Store *store.Store
	Clock clock.Clock
}

// Routes 是埋点的路由。没有身份也能用，被封禁的身份由中间件 403。
func Routes(opts Options) []route.Route {
	// 漏填会让两个接口一直 500（#123 复审抓到过）：启动时就报错，别等到第一个请求。
	if opts.Store == nil {
		panic("telemetry.Routes: Options.Store 不能为空")
	}
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	h := &handler{
		opts:         opts,
		eventsByIP:   ratelimit.New(eventsPerIPPerMin, eventsPerIPPerMin, opts.Clock),
		eventsByDev:  ratelimit.New(eventsPerDevicePerMin, eventsPerDevicePerMin, opts.Clock),
		errorsByIP:   ratelimit.New(errorsPerIPPerMin, errorsPerIPPerMin, opts.Clock),
		errorsByDev:  ratelimit.New(errorsPerDevicePerMin, errorsPerDevicePerMin, opts.Clock),
		errorsGlobal: ratelimit.New(errorsGlobalPerHour/60.0, errorsGlobalPerHour, opts.Clock),
	}
	return []route.Route{
		{Method: http.MethodPost, Pattern: "/api/events", Access: route.Public, Handler: http.HandlerFunc(h.events)},
		{Method: http.MethodPost, Pattern: "/api/client-errors", Access: route.Public, Handler: http.HandlerFunc(h.clientError)},
	}
}

type handler struct {
	opts                    Options
	eventsByIP, eventsByDev *ratelimit.Limiter
	errorsByIP, errorsByDev *ratelimit.Limiter
	errorsGlobal            *ratelimit.Limiter
}

type eventIn struct {
	Name  string            `json:"name"`
	Props map[string]string `json:"props"`
}

// events 收一批埋点。非法的单条（名字不在白名单、属性不合规）丢掉并计数，不让一条坏数据拖垮整批；
// 整体不合法（不是 JSON、环境不对、太大、条数超限）才拒绝。
func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Env    string    `json:"env"`
		Events []eventIn `json:"events"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !slices.Contains(Envs, body.Env) {
		writeError(w, http.StatusBadRequest, "invalid_input", "env 只能是 "+strings.Join(Envs, " / "))
		return
	}
	if len(body.Events) == 0 || len(body.Events) > MaxBatch {
		writeError(w, http.StatusBadRequest, "invalid_input", "events 需要 1–20 条")
		return
	}
	if !h.allow(w, r, h.eventsByIP, h.eventsByDev) {
		return
	}

	type row struct{ name, props string }
	var rows []row
	for _, e := range body.Events {
		props, ok := cleanProps(e)
		if !ok {
			continue
		}
		rows = append(rows, row{e.Name, props})
	}

	userID := identity.UserID(r.Context())
	now := h.opts.Clock.Now().Unix()
	if len(rows) > 0 {
		err := h.opts.Store.Write(r.Context(), func(ctx context.Context, tx *store.Tx) error {
			for _, e := range rows {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO events (at, user_id, name, env, props) VALUES (?, ?, ?, ?, ?)`,
					now, nullIfEmpty(userID), e.name, body.Env, nullIfEmpty(e.props)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			slog.Error("写埋点失败", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "服务出错了，请稍后再试")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"accepted": len(rows), "dropped": len(body.Events) - len(rows)})
}

// cleanProps 校验一条事件，返回要存的 props（JSON，可为空）。属性只认白名单里的键，值有固定形状：
// game 是牌局短码，channel 是分享渠道，不收自由文本。
func cleanProps(e eventIn) (string, bool) {
	if !slices.Contains(store.FunnelEvents, e.Name) {
		return "", false
	}
	out := map[string]string{}
	for k, v := range e.Props {
		switch {
		case k == "game" && gameCodeRE.MatchString(v):
			out[k] = v
		case k == "channel" && e.Name == "share_channel" && slices.Contains(channels, v):
			out[k] = v
		default:
			return "", false
		}
	}
	if len(out) == 0 {
		return "", true
	}
	b, _ := json.Marshal(out)
	return string(b), true
}

// clientError 收一条前端错误。消息和堆栈截断，地址只留路径（查询串里可能有 claim token）。
func (h *handler) clientError(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Env     string `json:"env"`
		Message string `json:"message"`
		Stack   string `json:"stack"`
		URL     string `json:"url"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !slices.Contains(Envs, body.Env) {
		writeError(w, http.StatusBadRequest, "invalid_input", "env 只能是 "+strings.Join(Envs, " / "))
		return
	}
	msg := truncate(redact(strings.TrimSpace(body.Message)), maxMessageRunes)
	if msg == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "缺少 message")
		return
	}
	if !h.allow(w, r, h.errorsByIP, h.errorsByDev) {
		return
	}
	if !h.errorsGlobal.Allow("all") {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "上报太频繁，请稍后再试")
		return
	}

	props, _ := json.Marshal(map[string]string{
		"message": msg,
		"stack":   truncate(redact(body.Stack), maxStackRunes),
		"path":    truncate(pathOnly(body.URL), maxPathRunes),
	})
	userID := identity.UserID(r.Context())
	err := h.opts.Store.Write(r.Context(), func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO events (at, user_id, name, env, props) VALUES (?, ?, ?, ?, ?)`,
			h.opts.Clock.Now().Unix(), nullIfEmpty(userID), errorEventName, body.Env, string(props))
		return err
	})
	if err != nil {
		slog.Error("写前端错误失败", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "服务出错了，请稍后再试")
		return
	}
	slog.Warn("前端错误", "env", body.Env, "message", msg)
	w.WriteHeader(http.StatusNoContent)
}

// allow 先按 IP 再按设备扣额度（没有身份就只按 IP）。IP 被拦时不扣设备的。
func (h *handler) allow(w http.ResponseWriter, r *http.Request, byIP, byDev *ratelimit.Limiter) bool {
	if !byIP.Allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "上报太频繁，请稍后再试")
		return false
	}
	if ident := identity.FromContext(r.Context()); ident != nil && !byDev.Allow(ident.Device.ID) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "上报太频繁，请稍后再试")
		return false
	}
	return true
}

// decode 读 JSON 请求体，上限 MaxBodyBytes；失败时已经写好响应。
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "请求体太大")
			return false
		}
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return false
	}
	return true
}

// secretRE 匹配地址或报错文字里的令牌参数（?claim=…、&token=…）：消息和堆栈里常带完整地址。
var secretRE = regexp.MustCompile(`(?i)\b(claim|token|code)=[^&\s"'<>)#]+`)

// redact 把令牌参数的值抹掉，先抹再截断，免得截断把半截令牌留下来。
func redact(s string) string { return secretRE.ReplaceAllString(s, "$1=[redacted]") }

// pathOnly 只留地址里的路径。解析不了就返回空串。
func pathOnly(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	// 去掉协议和主机：http://host/path → /path
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			raw = rest[j:]
		} else {
			raw = "/"
		}
	}
	return raw
}

// truncate 按字符数截断，不会切坏一个汉字。
func truncate(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes])
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
