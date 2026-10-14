package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/secheck"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// 小游戏身份（#84，docs/server-design.md §5.2）：微信 unionid / openid 只是指向 user_id 的关联（user_links 表），
// 不当作 user_id 用，也不返回给客户端，日志里也不出现明文。先认 unionid，没有时认 (appid, openid)；
// unionid 后来才出现时补写。不存 session_key。

const (
	// WxMinigameUA 是小游戏设备在 devices.ua 里的记法，「我的」设备列表里据此显示。
	WxMinigameUA = "wx-minigame"
	// DevWxAppID 是开发模式下假 code 用的 appid：关联记成 wx_openid:dev。
	DevWxAppID = "dev"
	// devWxCodePrefix 开头的 code 在开发模式（且没配真实 AppID）时直接当 openid 用。
	devWxCodePrefix = "dev:"

	providerUnionID      = "wx_unionid"
	providerOpenIDPrefix = "wx_openid:"

	// code2SessionURL 是微信的登录凭证校验接口。
	code2SessionURL = "https://api.weixin.qq.com/sns/jscode2session"
)

var (
	// ErrWxDisabled 表示没配 GUANDAN_WX_APPID / GUANDAN_WX_SECRET（也不是开发模式）。
	ErrWxDisabled = errors.New("小游戏登录未开启")
	// ErrWxCodeInvalid 表示 wx.login 的 code 无效、过期或已经用过。
	ErrWxCodeInvalid = errors.New("微信登录凭证无效")
	// ErrWxUnavailable 表示调微信接口失败（网络、限频、系统繁忙）。
	ErrWxUnavailable = errors.New("微信接口暂时不可用")
	// ErrWxRateLimited 表示这个 IP 新建身份太频繁（和 POST /api/me 共用配额）。
	ErrWxRateLimited = errors.New("创建身份太频繁")
)

// WxSession 是 code2Session 结果里我们要的部分。session_key 故意不在这里：不存、不传。
type WxSession struct {
	OpenID  string
	UnionID string // 小游戏没绑开放平台、或者未发布版本时为空
}

// WxExchanger 把 wx.login 的 code 换成 openid / unionid。生产用 Code2Session，测试和开发模式用假的。
type WxExchanger interface {
	Exchange(ctx context.Context, code string) (WxSession, error)
}

// SetWx 接上小游戏登录。appID 决定 openid 关联的 provider（wx_openid:<appid>）。不调用时小游戏登录关闭。
func (s *Service) SetWx(appID string, ex WxExchanger) {
	s.wxAppID, s.wx = appID, ex
}

// SetContentGuard 接上微信内容安全检测（#146）：小游戏新建身份时的称呼先送微信查。nil 时不查。
func (s *Service) SetContentGuard(g *secheck.Guard) { s.sec = g }

// AllowWxLogin 检查这个 IP 现在能不能调小游戏登录（每 IP 每分钟 20 次）：每次都要调一次微信接口，
// 微信那边对 code2Session 也有频率限制，不能让一个人把它用光。
func (s *Service) AllowWxLogin(ip string) bool { return s.wxLimiter.Allow(ip) }

// Code2Session 是真实的 code2Session 客户端。
type Code2Session struct {
	AppID, Secret string
	// Endpoint 为空时用微信的正式地址；测试里指向 httptest。
	Endpoint string
	Client   *http.Client
}

// Exchange 调 code2Session。appid、secret、code 都在查询串里（微信的接口就是这样），所以这个 URL 不能打日志。
func (c Code2Session) Exchange(ctx context.Context, code string) (WxSession, error) {
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = code2SessionURL
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	q := url.Values{
		"appid":      {c.AppID},
		"secret":     {c.Secret},
		"js_code":    {code},
		"grant_type": {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return WxSession{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error 会带上完整 URL（含 secret），只记错误类型。
		slog.Warn("调 code2Session 失败", "err_type", fmt.Sprintf("%T", err))
		return WxSession{}, ErrWxUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("code2Session 返回非 200", "status", resp.StatusCode)
		return WxSession{}, ErrWxUnavailable
	}
	var body struct {
		OpenID  string `json:"openid"`
		UnionID string `json:"unionid"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&body); err != nil {
		slog.Warn("code2Session 响应不是 JSON")
		return WxSession{}, ErrWxUnavailable
	}
	switch body.ErrCode {
	case 0:
	case 40029, 40163, 40226: // code 无效、code 已被使用、高风险用户被拦截
		return WxSession{}, ErrWxCodeInvalid
	default:
		slog.Warn("code2Session 返回错误", "errcode", body.ErrCode, "errmsg", body.ErrMsg)
		return WxSession{}, ErrWxUnavailable
	}
	if body.OpenID == "" {
		slog.Warn("code2Session 没有返回 openid")
		return WxSession{}, ErrWxUnavailable
	}
	return WxSession{OpenID: body.OpenID, UnionID: body.UnionID}, nil
}

// DevWxExchanger 只在开发模式、没配真实 AppID 时用：`dev:<任意>` 里的 <任意> 直接当 openid，
// 小游戏开发者工具连本机服务时不用真的 AppSecret 也能登录。不是这个格式的 code 一律无效。
type DevWxExchanger struct{}

// Exchange 解析 dev:<openid>。
func (DevWxExchanger) Exchange(_ context.Context, code string) (WxSession, error) {
	openid, ok := strings.CutPrefix(code, devWxCodePrefix)
	if !ok || strings.TrimSpace(openid) == "" {
		return WxSession{}, ErrWxCodeInvalid
	}
	return WxSession{OpenID: openid}, nil
}

// exchangeWx 调换 code；没接上时返回 ErrWxDisabled。
func (s *Service) exchangeWx(ctx context.Context, code string) (WxSession, error) {
	if s.wx == nil {
		return WxSession{}, ErrWxDisabled
	}
	if code == "" || len(code) > 256 {
		return WxSession{}, ErrWxCodeInvalid
	}
	return s.wx.Exchange(ctx, code)
}

// WxEnabled 说明小游戏登录是否可用。
func (s *Service) WxEnabled() bool { return s.wx != nil }

// wxLinks 是这次微信身份对应的关联：openid 那条一定有，unionid 那条只在有 unionid 时有。
func (s *Service) wxLinks(sess WxSession) [][2]string {
	links := [][2]string{{providerOpenIDPrefix + s.wxAppID, sess.OpenID}}
	if sess.UnionID != "" {
		// unionid 排在前面：查找时先认它（#84）。
		links = append([][2]string{{providerUnionID, sess.UnionID}}, links...)
	}
	return links
}

// wxLinkedUser 按 unionid、再按 openid 找已关联的用户，顺着合并关系走到最终用户。
// 找不到（或者那个用户已经注销）返回空串。
func (s *Service) wxLinkedUser(ctx context.Context, tx *store.Tx, sess WxSession) (string, error) {
	for _, l := range s.wxLinks(sess) {
		var userID string
		err := tx.QueryRowContext(ctx,
			`SELECT user_id FROM user_links WHERE provider = ? AND subject = ?`, l[0], l[1]).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", err
		}
		root, deleted, err := resolveMergedTx(ctx, tx, userID)
		if err != nil {
			return "", err
		}
		if deleted {
			// 注销时关联已经删了；万一有残留（比如老的 admin 删除），当作没关联过，后面 attachWxLinks 会清掉它。
			return "", nil
		}
		return root, nil
	}
	return "", nil
}

// backfillWxLinks 给已经认出来的用户补写缺的关联（比如以前只有 openid，这次有了 unionid）。
// 已经被占用的（这个 subject 关联着别人、或者这个用户在该 provider 下已经有别的关联）跳过，不抢。
func (s *Service) backfillWxLinks(ctx context.Context, tx *store.Tx, userID string, sess WxSession, now time.Time) error {
	for _, l := range s.wxLinks(sess) {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO user_links (provider, subject, user_id, created_at) VALUES (?, ?, ?, ?)`,
			l[0], l[1], userID, now.Unix()); err != nil {
			return err
		}
	}
	return nil
}

// attachWxLinks 把这个微信身份的关联都挂到 userID 上：这个 subject 原来关联着谁都先摘掉（认领切换，#84 §C），
// 这个用户在同一个 provider 下原有的别的关联也摘掉（一个 provider 只能有一条）。被摘掉的用户的其他数据不动。
func (s *Service) attachWxLinks(ctx context.Context, tx *store.Tx, userID string, sess WxSession, now time.Time) error {
	for _, l := range s.wxLinks(sess) {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM user_links WHERE provider = ? AND (subject = ? OR user_id = ?)`,
			l[0], l[1], userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_links (provider, subject, user_id, created_at) VALUES (?, ?, ?, ?)`,
			l[0], l[1], userID, now.Unix()); err != nil {
			return err
		}
	}
	return nil
}

// wxBanned 查这个用户还能不能从小游戏登录（§13.6）：用户本人被封禁，或者他名下有被封的小游戏设备，都不行。
//
// 只看 ua = wx-minigame 的设备（#145）：小游戏重新 wx.login 认回的是同一个用户，封掉的那台小游戏设备
// 不拦的话，删掉小游戏再进就能拿到一台没被封的新设备。H5 设备被封不连带：那是按设备的处置，
// 同一个人别的设备（包括小游戏）照常可用；H5 那边清掉 cookie 只能拿到新的匿名身份，本来就绕不回原用户。
func wxBanned(ctx context.Context, tx *store.Tx, userID string) (bool, error) {
	var banned bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM users   WHERE id = ? AND banned_at IS NOT NULL)
		    OR EXISTS (SELECT 1 FROM devices WHERE user_id = ? AND ua = ? AND banned_at IS NOT NULL)`,
		userID, userID, WxMinigameUA).Scan(&banned)
	return banned, err
}

// WxLoginResult 是小游戏登录的结果。NeedNickname 为真时其他字段都是零值。
type WxLoginResult struct {
	Identity     Identity
	DeviceToken  string
	NeedNickname bool
}

// WxLogin 是 POST /api/wx/login（#84 §A）。
//
//   - 这个微信身份已经关联了用户：给他新挂一台小游戏设备，补写缺的关联；用户被封、或者他名下有被封的
//     小游戏设备时返回 ErrBanned，不新建设备（#145）；
//   - 没关联、也没给称呼：NeedNickname；
//   - 没关联、给了称呼（和 POST /api/me 同一套校验）：新建用户、设备和关联。
//
// allowCreate 只在真的要新建用户时调用（和 POST /api/me 共用每 IP 的创建配额）。
// 返回的 DeviceToken 由接口层放进响应体：小游戏没有 cookie（§10.3 的唯一例外）。
func (s *Service) WxLogin(ctx context.Context, code, nickname string, allowCreate func() bool) (WxLoginResult, error) {
	var clean string
	if nickname != "" {
		var err error
		if clean, err = NormalizeNickname(nickname); err != nil {
			return WxLoginResult{}, err
		}
	}
	// 先调微信，再开写事务：网络请求不能占着唯一的写连接。
	sess, err := s.exchangeWx(ctx, code)
	if err != nil {
		return WxLoginResult{}, err
	}
	// 称呼送微信内容安全检测（#146，场景 1 资料）。也在写事务之前：网络请求不占写连接。
	// 小游戏只在拿到 need_nickname 之后才带称呼，所以带了称呼基本就是要新建身份；认回老用户时称呼用不上，查了也无妨。
	if clean != "" {
		if err := s.sec.CheckOpenID(ctx, sess.OpenID, secheck.SceneProfile, secheck.Field{Name: "nickname", Text: clean}); err != nil {
			return WxLoginResult{}, err
		}
	}

	deviceToken := newToken(DeviceTokenBytes)
	now := s.clk.Now()
	var res WxLoginResult
	err = s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		userID, err := s.wxLinkedUser(ctx, tx, sess)
		if err != nil {
			return err
		}

		if userID != "" {
			banned, err := wxBanned(ctx, tx, userID)
			if err != nil {
				return err
			}
			if banned {
				return ErrBanned
			}
			if err := s.backfillWxLinks(ctx, tx, userID, sess, now); err != nil {
				return err
			}
		} else {
			if clean == "" {
				res.NeedNickname = true
				return nil
			}
			if allowCreate != nil && !allowCreate() {
				return ErrWxRateLimited
			}
			userID = newID(now)
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO users (id, nickname, created_at) VALUES (?, ?, ?)`,
				userID, clean, now.Unix()); err != nil {
				return err
			}
			if err := s.attachWxLinks(ctx, tx, userID, sess, now); err != nil {
				return err
			}
		}

		device, err := insertDevice(ctx, tx, now, userID, deviceToken, WxMinigameUA)
		if err != nil {
			return err
		}
		user, err := loadUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		res = WxLoginResult{Identity: Identity{User: user, Device: device}, DeviceToken: deviceToken}
		return nil
	})
	if err != nil {
		return WxLoginResult{}, err
	}
	return res, nil
}

// WxLink 是 POST /api/wx/link 的 6 位认领码路径（#84 §B2）：H5 用户签发的认领码，在小游戏里输入，
// 把这个微信身份关联到那个用户，并给小游戏挂一台设备。
//
// 防护和 POST /api/claim/redeem 凭码兑换完全一样：码格式、全站失败预算、签发和兑换同网段、每 IP 限流（在 handler 里，
// 和凭码兑换共用一个限流器）；所有失败都是同一个 ErrClaimInvalid。
//
// 这个微信身份已经关联了另一个用户、又没有 confirm：返回 ClaimConflictError（带认领码主人的称呼），码不消耗，
// 关联不动。确认后再提交一次（要重新 wx.login 拿新的 wx_code，旧的已经用过），这时把关联切过去，原用户的数据不动。
func (s *Service) WxLink(ctx context.Context, claimCode, wxCode, clientNet string, confirm bool) (Identity, string, error) {
	if s.wx == nil {
		return Identity{}, "", ErrWxDisabled
	}
	if !isClaimCode(claimCode) || s.codeBudget.suspended() {
		s.codeBudget.recordFailure()
		return Identity{}, "", ErrClaimInvalid
	}
	sess, err := s.exchangeWx(ctx, wxCode)
	if err != nil {
		return Identity{}, "", err
	}

	deviceToken := newToken(DeviceTokenBytes)
	now := s.clk.Now()
	var result Identity
	err = s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		rowid, ownerID, err := findLiveClaimByCode(ctx, tx, claimCode, clientNet, now)
		if err != nil {
			return err
		}
		owner, err := loadUser(ctx, tx, ownerID)
		if err != nil {
			return err
		}

		linked, err := s.wxLinkedUser(ctx, tx, sess)
		if err != nil {
			return err
		}
		if linked != "" && linked != ownerID && !confirm {
			return &ClaimConflictError{OtherNickname: owner.Nickname}
		}

		// 认领码的主人、以及这个微信身份原来关联的人（切换认领时），任一边被封或有被封的小游戏设备都拒绝（#145）：
		// 否则把被封的微信身份切到另一个号上，就又拿回一台没被封的小游戏设备。
		for _, uid := range []string{ownerID, linked} {
			if uid == "" {
				continue
			}
			banned, err := wxBanned(ctx, tx, uid)
			if err != nil {
				return err
			}
			if banned {
				return ErrBanned
			}
		}

		if err := consumeClaimRow(ctx, tx, rowid, now); err != nil {
			return err
		}
		if err := s.attachWxLinks(ctx, tx, ownerID, sess, now); err != nil {
			return err
		}
		device, err := insertDevice(ctx, tx, now, ownerID, deviceToken, WxMinigameUA)
		if err != nil {
			return err
		}
		result = Identity{User: owner, Device: device}
		s.publishClaimRedeemed(tx, ownerID, device)
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrClaimInvalid) {
			s.codeBudget.recordFailure()
		}
		return Identity{}, "", err
	}
	return result, deviceToken, nil
}

// resolveMergedTx 在写事务里顺着 merged_into 走到最终用户，并报告它是否已经注销。
func resolveMergedTx(ctx context.Context, tx *store.Tx, userID string) (string, bool, error) {
	for i := 0; i < 8; i++ {
		var (
			mergedInto sql.NullString
			deletedAt  sql.NullInt64
		)
		err := tx.QueryRowContext(ctx,
			`SELECT merged_into, deleted_at FROM users WHERE id = ?`, userID).Scan(&mergedInto, &deletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return "", true, nil
		}
		if err != nil {
			return "", false, err
		}
		if !mergedInto.Valid || mergedInto.String == "" {
			return userID, deletedAt.Valid, nil
		}
		userID = mergedInto.String
	}
	return "", false, fmt.Errorf("身份合并链过长，起点 %s", userID)
}
