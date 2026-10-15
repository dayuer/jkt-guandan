// Package secheck 是小游戏的微信内容安全检测（#146，docs/server-design.md §14）：
// 用户写的称呼、地点、备注在写入前同步送微信的游戏专用版 msg_sec_check，判为 risky 就拒收。
//
//   - 只查小游戏用户：请求者在 user_links 里有 wx_openid:<appid> 才查，没有（H5 测试用户）跳过；
//   - 微信出错、超时（1 秒）：放行，记指标和日志；
//   - 日志只记场景、字段名、结论、label、耗时、errcode，不记原文，也不记 openid。
//
// Guard 为 nil 时整体关闭（没配 GUANDAN_WX_APPID / GUANDAN_WX_SECRET，也不是开发模式）。
package secheck

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/metrics"
	"github.com/dayuer/jkt-guandan/server/internal/store"
	"github.com/dayuer/jkt-guandan/server/internal/wxapi"
)

// 场景值（微信文档）：1 资料，2 评论，3 论坛，4 社交日志，5 聊天。
const (
	SceneProfile = 1 // 称呼
	SceneForum   = 3 // 牌局的地点、备注
)

// DefaultTimeout 是单次检测的超时。微信一般 500ms 内返回，文档建议 1 秒。
const DefaultTimeout = time.Second

// msgSecCheckPath 是小游戏的游戏专用版内容安全接口：针对游戏场景加强了辱骂、低俗、营销引流的识别。
const msgSecCheckPath = "/wxa/game/content_spam/msg_sec_check"

// maxContent 是接口接受的最长文本（字符）。我们的字段都远短于它，超过时截断只是兜底。
const maxContent = 2500

// Verdict 是一次检测的结论。
type Verdict struct {
	Risky bool
	// Suggest 是微信给的原值（pass / risky / review），只用于指标和日志。
	Suggest string
	// Label 是命中的类别：100 正常、10001 营销广告、20001 时政、20002 色情、20003 辱骂、
	// 20006 违法犯罪、20012 低俗、21000 其他。
	Label int
}

// Checker 检测一段文本。生产用 WxChecker，开发模式用 DevChecker，测试用假的。
type Checker interface {
	Check(ctx context.Context, openid string, scene int, content string) (Verdict, error)
}

// WxChecker 调微信的游戏专用版 msg_sec_check（access_token 由 wxapi.Client 管）。
type WxChecker struct {
	API *wxapi.Client
}

// Check 调 POST /wxa/game/content_spam/msg_sec_check。
func (c WxChecker) Check(ctx context.Context, openid string, scene int, content string) (Verdict, error) {
	if r := []rune(content); len(r) > maxContent {
		content = string(r[:maxContent])
	}
	var out struct {
		Result struct {
			Suggest string `json:"suggest"`
			Label   int    `json:"label"`
		} `json:"result"`
	}
	err := c.API.PostJSON(ctx, msgSecCheckPath, map[string]any{
		"openid": openid, "version": 2, "scene": scene, "content": content,
	}, &out)
	if err != nil {
		return Verdict{}, err
	}
	switch out.Result.Suggest {
	case "risky", "pass", "review":
	default:
		return Verdict{}, fmt.Errorf("%w（suggest 不认识）", wxapi.ErrUnavailable)
	}
	return Verdict{Risky: out.Result.Suggest == "risky", Suggest: out.Result.Suggest, Label: out.Result.Label}, nil
}

// DevRiskyWord 出现在文本里时，开发模式的假检测器判为 risky，方便在开发者工具里测前端的提示。
const DevRiskyWord = "内容安全测试"

// DevChecker 是开发模式（没配真实 AppID）的假检测器：含 DevRiskyWord 判 risky（label 21000），其余 pass。
type DevChecker struct{}

func (DevChecker) Check(_ context.Context, _ string, _ int, content string) (Verdict, error) {
	if strings.Contains(content, DevRiskyWord) {
		return Verdict{Risky: true, Suggest: "risky", Label: 21000}, nil
	}
	return Verdict{Suggest: "pass", Label: 100}, nil
}

// Message 是被拦时给用户看的提示。不说具体命中了什么：label 只进日志。
const Message = "这段内容不适合发布，请修改后再试"

// ErrUnsafe 是 *UnsafeError 的哨兵，errors.Is 用。
var ErrUnsafe = errors.New("内容没有通过微信内容安全检测")

// UnsafeError 说明哪个字段被微信判为 risky。
type UnsafeError struct {
	Field string
	Label int
}

func (e *UnsafeError) Error() string {
	return fmt.Sprintf("%s：%s（label %d）", ErrUnsafe, e.Field, e.Label)
}
func (e *UnsafeError) Is(target error) bool { return target == ErrUnsafe }

// Field 是一个要检测的字段：Name 是接口里的字段名（venue、note、nickname），回给前端定位输入框。
type Field struct {
	Name, Text string
}

// Metrics 是检测的指标。为 nil 时不记。
type Metrics struct {
	// Checks 按结论分类：pass、risky、review、error、timeout、no_openid。
	Checks   metrics.CounterVec
	Duration *metrics.Histogram
}

// NewMetrics 建指标并登记到 r。
func NewMetrics(r *metrics.Registry) *Metrics {
	m := &Metrics{Duration: metrics.NewHistogram(.05, .1, .25, .5, 1, 2)}
	r.CounterVec("guandan_wx_sec_check_total", "微信内容安全检测次数，按结论分类（error、timeout 时放行）。", "outcome", &m.Checks)
	r.Histogram("guandan_wx_sec_check_duration_seconds", "微信内容安全检测耗时。", m.Duration)
	return m
}

func (m *Metrics) inc(outcome string) {
	if m != nil {
		m.Checks.Inc(outcome)
	}
}

func (m *Metrics) observe(d time.Duration) {
	if m != nil {
		m.Duration.Observe(d)
	}
}

// Guard 在写入前检测用户写的文本。
type Guard struct {
	Checker Checker
	// AppID 决定去 user_links 里找哪一条：wx_openid:<AppID>。开发模式是 "dev"（identity.DevWxAppID）。
	AppID string
	// Store 用来按 user_id 找 openid。
	Store *store.Store
	// Timeout 为 0 时用 DefaultTimeout。
	Timeout time.Duration
	Metrics *Metrics
}

// CheckUser 检测 userID 写的这些字段。这个用户没有小游戏 openid 时跳过（H5 测试用户）。
// 有字段被判 risky 时返回 *UnsafeError（按 fields 的顺序取第一个）；微信出错、超时、查 openid 出错都放行。
func (g *Guard) CheckUser(ctx context.Context, userID string, scene int, fields ...Field) error {
	if g == nil || userID == "" || !hasText(fields) {
		return nil
	}
	var openid string
	err := g.Store.Read().QueryRowContext(ctx,
		`SELECT subject FROM user_links WHERE provider = ? AND user_id = ?`,
		"wx_openid:"+g.AppID, userID).Scan(&openid)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		g.Metrics.inc("no_openid")
		return nil
	case err != nil:
		slog.Warn("内容安全检测：查 openid 失败，放行", "err", err)
		g.Metrics.inc("error")
		return nil
	}
	return g.CheckOpenID(ctx, openid, scene, fields...)
}

// CheckOpenID 和 CheckUser 一样，只是 openid 已经在手上（小游戏登录时刚换出来）。
// 多个字段并发检测，总耗时约等于一次。
func (g *Guard) CheckOpenID(ctx context.Context, openid string, scene int, fields ...Field) error {
	if g == nil || openid == "" {
		return nil
	}
	verdicts := make([]Verdict, len(fields))
	var wg sync.WaitGroup
	for i, f := range fields {
		if strings.TrimSpace(f.Text) == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			verdicts[i] = g.checkOne(ctx, openid, scene, f.Name, f.Text)
		}()
	}
	wg.Wait()
	for i, v := range verdicts {
		if v.Risky {
			return &UnsafeError{Field: fields[i].Name, Label: v.Label}
		}
	}
	return nil
}

// checkOne 检测一个字段；出错、超时都当作 pass。日志里没有原文和 openid。
func (g *Guard) checkOne(ctx context.Context, openid string, scene int, field, text string) Verdict {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	v, err := g.Checker.Check(cctx, openid, scene, text)
	elapsed := time.Since(start)
	g.Metrics.observe(elapsed)
	ms := elapsed.Milliseconds()

	if err != nil {
		outcome := "error"
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			outcome = "timeout"
		}
		g.Metrics.inc(outcome)
		code, _ := wxapi.Code(err)
		// 不打 err 本身：wxapi.Error 里有微信的 errmsg，不保证不回显内容。
		slog.Warn("内容安全检测失败，放行", "scene", scene, "field", field, "outcome", outcome,
			"errcode", code, "unavailable", errors.Is(err, wxapi.ErrUnavailable), "ms", ms)
		return Verdict{}
	}
	g.Metrics.inc(v.Suggest)
	if v.Risky {
		slog.Info("内容安全检测：拦截", "scene", scene, "field", field, "label", v.Label, "ms", ms)
	} else {
		slog.Debug("内容安全检测：通过", "scene", scene, "field", field, "suggest", v.Suggest, "label", v.Label, "ms", ms)
	}
	return v
}

func hasText(fields []Field) bool {
	for _, f := range fields {
		if strings.TrimSpace(f.Text) != "" {
			return true
		}
	}
	return false
}
