// Package identity 管免注册身份：用户、设备 token、claim token、认领码、身份合并
// （docs/server-design.md §5.2、§10.3）。
//
// SQL 通过 store.Write 的事务或 store.Read 的连接执行，不自己开数据库句柄。
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/ratelimit"
	"github.com/dayuer/jkt-guandan/internal/store"
)

const (
	// ClaimTTL 是 claim token 的有效期（§5.2）。
	ClaimTTL = 10 * time.Minute
	// DeviceTokenBytes 是 device_token 的随机字节数（§5.2）。
	DeviceTokenBytes = 32
	// ClaimTokenBytes 是 claim_token 的随机字节数（§5.2）。
	ClaimTokenBytes = 16
	// ClaimCodeMaxAttempts 是一个认领码最多能试几次（§5.2）。
	ClaimCodeMaxAttempts = 5
	// touchInterval 决定多久刷新一次设备的 last_seen。每次请求都写会白白占用唯一的写连接。
	touchInterval = time.Hour
)

var (
	// ErrNotFound 表示这个身份不存在（cookie 无效或已被删）。
	ErrNotFound = errors.New("身份不存在")
	// ErrClaimInvalid 是兑换失败时唯一对外的错误。
	// §10.3 要求不区分「没有这个码」「过期了」「次数用完了」——区分开就等于告诉攻击者码是否存在。
	ErrClaimInvalid = errors.New("兑换失败")
	// ErrDeviceNotFound 表示要退出的设备不属于当前用户。
	ErrDeviceNotFound = errors.New("设备不存在")
)

// User 是返回给接口层的用户信息。只有白名单字段（§10.4）。
type User struct {
	ID        string
	Nickname  string
	CreatedAt time.Time
}

// Device 是当前用户的一台设备。不包含 token_hash。
//
// §4 的 devices 表只记 last_seen，不记创建时间，所以这里也没有 CreatedAt。
type Device struct {
	ID        string
	UserAgent string
	LastSeen  time.Time
}

// Identity 是一次请求解析出来的身份。
type Identity struct {
	User   User
	Device Device
}

// Publisher 是身份系统用到的实时能力（internal/realtime 的 Hub）。都只在写事务提交后调用。
type Publisher interface {
	// Publish 把事件发给订阅者。
	Publish(topic, eventType string, data any)
	// KickDevice 立刻断开这台设备的实时连接：设备被退出、切换到别的身份之后，
	// 它已经建立的连接不能再收到 user:{旧id} 的事件（#43）。
	KickDevice(deviceID string)
}

// Service 是身份系统的业务入口。
type Service struct {
	st      *store.Store
	clk     clock.Clock
	cookies CookiePolicy
	pub     Publisher // 可以为 nil：不发实时事件

	games GameCleaner // 可以为 nil：注销时不清牌局侧的数据（§14）

	createLimiter *ratelimit.Limiter // POST /api/me：每 IP 每小时 20 次（§12.3）
	issueLimiter  *ratelimit.Limiter // POST /api/claim：每用户每小时 5 次（§5.2 修订）
	codeLimiter   *ratelimit.Limiter // 凭码兑换：每 IP 每分钟 10 次（§12.3）
	codeBudget    *codeFailureBudget
}

// NewService 建身份服务。dev 决定 cookie 策略（§10.3）。
func NewService(st *store.Store, clk clock.Clock, dev bool) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	return &Service{
		st:            st,
		clk:           clk,
		cookies:       CookiePolicyFor(dev),
		createLimiter: ratelimit.New(20.0/60, 20, clk),
		issueLimiter:  ratelimit.New(5.0/60, 5, clk),
		codeLimiter:   ratelimit.New(10, 10, clk),
		codeBudget:    newCodeFailureBudget(clk),
	}
}

// SetPublisher 接上实时事件（§5.5）。启动时调用一次；不调用就不发事件。
func (s *Service) SetPublisher(p Publisher) { s.pub = p }

// publishClaimRedeemed 登记一个提交后回调：认领成功后通知 user:{ownerID} 的订阅者，
// 发起认领的那一页（比如微信里的 12 屏）随即显示「✓ 已在浏览器中打开」（原型 H1）。
// 事务回滚时回调不执行，所以不会发出没真正成功的认领。
func (s *Service) publishClaimRedeemed(tx *store.Tx, ownerID string, device Device) {
	if s.pub == nil {
		return
	}
	tx.AfterCommit(func(context.Context) {
		s.pub.Publish("user:"+ownerID, "claim.redeemed", map[string]any{
			"device_id": device.ID,
			"ua":        device.UserAgent,
		})
	})
}

// kickDeviceAfterCommit 登记一个提交后回调，断开这台设备的实时连接（#43）。回滚时不执行。
func (s *Service) kickDeviceAfterCommit(tx *store.Tx, deviceID string) {
	if s.pub == nil {
		return
	}
	tx.AfterCommit(func(context.Context) { s.pub.KickDevice(deviceID) })
}

// ErrBanned 表示用户或设备被封禁（guandan admin ban）。
var ErrBanned = errors.New("identity: 已被封禁")

// ForRealtime 是 WebSocket 握手时的身份识别（§5.5）：没有身份返回空字符串；被封禁返回 ErrBanned，
// 握手随之 403。和心跳时的 StillValid 是同一套规则。
func (s *Service) ForRealtime(r *http.Request) (userID, deviceID string, err error) {
	ident := s.FromRequest(r)
	if ident == nil {
		return "", "", nil
	}
	banned, err := s.Banned(r.Context(), ident)
	if err != nil {
		return "", "", err
	}
	if banned {
		return "", "", ErrBanned
	}
	return ident.User.ID, ident.Device.ID, nil
}

// StillValid 是实时连接每次心跳时的复查（#43 的兜底）：这台设备还在，并且（顺着合并关系）
// 还属于握手时的那个用户，用户和设备都没有被封禁。读连接池上两次查询，开销很小。
func (s *Service) StillValid(ctx context.Context, userID, deviceID string) bool {
	var owner string
	err := s.st.Read().QueryRowContext(ctx, `SELECT user_id FROM devices WHERE id = ?`, deviceID).Scan(&owner)
	if err != nil {
		// 查不到就是设备被删了；读失败（库出问题）时也断开，客户端会重连并重新认身份。
		return false
	}
	root, err := s.resolveMergedUser(ctx, owner)
	if err != nil || root.ID != userID {
		return false
	}
	// 被 guandan admin ban 封禁：那是另一个进程，通知不到 Hub，靠这里在下一次心跳时断开。
	banned, err := s.st.IsBanned(ctx, userID, deviceID)
	return err == nil && !banned
}

// FromRequest 用请求里的设备 cookie 认出身份；没有或无效时返回 nil。
func (s *Service) FromRequest(r *http.Request) *Identity {
	token, ok := s.cookies.Read(r)
	if !ok {
		return nil
	}
	ident, err := s.Current(r.Context(), token)
	if err != nil {
		return nil
	}
	return ident
}

// Cookies 让接口层用同一份 cookie 策略读写（§10.3）。
func (s *Service) Cookies() CookiePolicy { return s.cookies }

// AllowCreate 检查这个 IP 现在能不能创建身份。
func (s *Service) AllowCreate(ip string) bool { return s.createLimiter.Allow(ip) }

// AllowCodeRedeem 检查这个 IP 现在能不能试认领码。
func (s *Service) AllowCodeRedeem(ip string) bool { return s.codeLimiter.Allow(ip) }

// AllowClaim 检查这个用户现在能不能签发新的交接令牌（每用户每小时 5 次）。
func (s *Service) AllowClaim(userID string) bool { return s.issueLimiter.Allow(userID) }

// Banned 判断这个身份（合并后的最终用户，或者这台设备）是否被封禁（§13.6 guandan admin ban）。
func (s *Service) Banned(ctx context.Context, ident *Identity) (bool, error) {
	return s.st.IsBanned(ctx, ident.User.ID, ident.Device.ID)
}

// Current 用设备 token 解析出身份。没有有效身份时返回 ErrNotFound。
func (s *Service) Current(ctx context.Context, deviceToken string) (*Identity, error) {
	if deviceToken == "" {
		return nil, ErrNotFound
	}

	var (
		deviceID, userAgent string
		lastSeen            int64
		userID, nickname    string
		userCreated         int64
		mergedInto          sql.NullString
	)
	err := s.st.Read().QueryRowContext(ctx, `
		SELECT d.id, COALESCE(d.ua, ''), d.last_seen,
		       u.id, u.nickname, u.created_at, u.merged_into
		  FROM devices d
		  JOIN users u ON u.id = d.user_id
		 WHERE d.token_hash = ?`, hashToken(deviceToken)).
		Scan(&deviceID, &userAgent, &lastSeen,
			&userID, &nickname, &userCreated, &mergedInto)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("按设备查身份: %w", err)
	}

	// 被合并过的身份统一按最终的那个用户处理（§10.4）。
	if mergedInto.Valid && mergedInto.String != "" {
		root, err := s.resolveMergedUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		userID, nickname, userCreated = root.ID, root.Nickname, root.CreatedAt.Unix()
	}

	ident := &Identity{
		User: User{ID: userID, Nickname: nickname, CreatedAt: time.Unix(userCreated, 0)},
		Device: Device{
			ID:        deviceID,
			UserAgent: userAgent,
			LastSeen:  time.Unix(lastSeen, 0),
		},
	}
	if err := s.touchDevice(ctx, deviceID, ident.Device.LastSeen); err != nil {
		// 刷新 last_seen 失败不该让整个请求失败。
		return ident, nil
	}
	return ident, nil
}

// CreateOrGet 处理「首次填称呼」。已经认出身份的请求直接返回现有身份，不新建用户。
//
// 返回的 deviceToken 只在这次真的新建了设备时非空，由接口层写进 Set-Cookie；
// 它不会出现在任何响应体里（§10.3）。
func (s *Service) CreateOrGet(ctx context.Context, current *Identity, nickname, userAgent string) (Identity, string, error) {
	clean, err := NormalizeNickname(nickname)
	if err != nil {
		return Identity{}, "", err
	}

	if current != nil {
		// 已经是本人了：刷新一下称呼对应的设备和 last_seen，返回原身份。
		if err := s.touchDevice(ctx, current.Device.ID, current.Device.LastSeen); err != nil {
			return Identity{}, "", err
		}
		return *current, "", nil
	}

	deviceToken := newToken(DeviceTokenBytes)
	now := s.clk.Now()

	var created Identity
	err = s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		userID := newID(now)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO users (id, nickname, created_at) VALUES (?, ?, ?)`,
			userID, clean, now.Unix()); err != nil {
			return err
		}
		device, err := insertDevice(ctx, tx, now, userID, deviceToken, userAgent)
		if err != nil {
			return err
		}
		created = Identity{
			User:   User{ID: userID, Nickname: clean, CreatedAt: now},
			Device: device,
		}
		return nil
	})
	if err != nil {
		return Identity{}, "", err
	}
	return created, deviceToken, nil
}

// IssueClaim 签发一次性交接令牌和 6 位认领码（§5.2 修订）。
//
// clientNet 是签发时的网络前缀（netPrefix 的结果），兑换时必须来自同一个网络。
//
// 签发时做两件事：把这个用户之前还没用的码作废（同一时刻只留一个有效码），
// 以及在写事务里保证新码和现有的有效码不重复。
func (s *Service) IssueClaim(ctx context.Context, userID, clientNet string) (token, code string, expiresAt time.Time, err error) {
	token = newToken(ClaimTokenBytes)
	now := s.clk.Now()
	expiresAt = now.Add(ClaimTTL)

	err = s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		// 同一用户只保留一个有效码：把旧的标成已经用过（也就是不再可用）。
		// 这里写 used_at 而不是只改 expires_at，是为了把它的号码从
		// claim_code_live 唯一索引里腾出来。
		if _, err := tx.ExecContext(ctx,
			`UPDATE claim_tokens SET used_at = ? WHERE user_id = ? AND used_at IS NULL`,
			now.Unix(), userID); err != nil {
			return err
		}

		// 6 位码的空间是 10^6，重试几次就够；一直撞上就报错，不静默返回重复的码。
		// 查重条件必须和 claim_code_live 索引的谓词一致（used_at IS NULL），
		// 否则会出现「查重通过但插入撞唯一索引」。
		for attempt := 0; attempt < 8; attempt++ {
			candidate, err := newClaimCode()
			if err != nil {
				return err
			}
			var taken int
			if err := tx.QueryRowContext(ctx,
				`SELECT count(*) FROM claim_tokens WHERE code = ? AND used_at IS NULL`,
				candidate).Scan(&taken); err != nil {
				return err
			}
			if taken > 0 {
				continue
			}
			code = candidate
			break
		}
		if code == "" {
			return errors.New("连续 8 次都生成了已被占用的认领码")
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO claim_tokens (token_hash, code, user_id, expires_at, used_at, attempts, issued_net)
			 VALUES (?, ?, ?, ?, NULL, 0, ?)`,
			hashToken(token), code, userID, expiresAt.Unix(), nullIfEmpty(clientNet))
		return err
	})
	if err != nil {
		return "", "", time.Time{}, err
	}
	return token, code, expiresAt, nil
}

// ClaimConflictError 表示这台设备已经有一个别人的身份，需要用户明确确认才能切换（§5.2 修订）。
//
// 抛出它的时候 token 还没有被消耗，两个身份和这台设备的状态都没变。
type ClaimConflictError struct {
	// OtherNickname 是 token 对应身份的称呼，用来在确认屏上告诉用户「这个链接属于 X」。
	OtherNickname string
}

func (e *ClaimConflictError) Error() string {
	return "认领链接属于另一个身份，需要确认"
}

// RedeemByToken 用 claim token 兑换身份。三种情况：
//
//   - 这台设备还没有身份：直接成为 token 对应的身份；
//   - 已经有同一个身份：照常兑换，只是新挂一台设备；
//   - 已经有**另一个**身份：**不合并、也不自动切换**，返回 ClaimConflictError（token 不消耗）。
//     用户在前端确认后带上 confirm 再提交一次，这时才把这台设备的 user_id 切过去。
//     原来的身份原样保留——它的座位、设备、订阅都不动，也不会被删除。
//
// 只有前端的 POST 会走到这里；抓链接预览的机器人只发 GET，不会消耗一次性 token（§10.3）。
func (s *Service) RedeemByToken(ctx context.Context, token, userAgent, currentDeviceID, currentUserID string, confirm bool) (Identity, string, error) {
	if token == "" {
		return Identity{}, "", ErrClaimInvalid
	}

	deviceToken := newToken(DeviceTokenBytes)
	// issuedToken 只有在真的新挂了一台设备时才非空：切换已有设备的那条路不需要新凭证，
	// 原来那个 cookie 继续用（换成一个没入库的 token 会让用户立刻掉线）。
	issuedToken := ""
	now := s.clk.Now()

	var result Identity
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		// 先只读一下这个 token 属于谁——不消耗，好让「需要确认」这条路可以原样返回。
		var ownerID string
		err := tx.QueryRowContext(ctx, `
			SELECT user_id FROM claim_tokens
			 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
			hashToken(token), now.Unix()).Scan(&ownerID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrClaimInvalid
		}
		if err != nil {
			return err
		}

		if currentUserID != "" && currentUserID != ownerID {
			if !confirm {
				owner, err := loadUser(ctx, tx, ownerID)
				if err != nil {
					return err
				}
				return &ClaimConflictError{OtherNickname: owner.Nickname}
			}
			// 用户确认过了：只把这台设备切过去，不做任何数据迁移。
			if err := consumeClaimToken(ctx, tx, token, now); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE devices SET user_id = ? WHERE id = ?`, ownerID, currentDeviceID); err != nil {
				return err
			}
			// 这台设备上的推送订阅是以旧身份开的：删掉，前端以新身份重新订阅。否则旧身份的提醒会继续发到
			// 这台已经换了人的设备上（#48 复审）。
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM push_subscriptions WHERE device_id = ?`, currentDeviceID); err != nil {
				return err
			}
			// 这台设备原来的连接认的是旧身份，断开它，让它以新身份重连（#43）。
			s.kickDeviceAfterCommit(tx, currentDeviceID)
			user, err := loadUser(ctx, tx, ownerID)
			if err != nil {
				return err
			}
			device, err := loadDevice(ctx, tx, currentDeviceID)
			if err != nil {
				return err
			}
			result = Identity{User: user, Device: device}
			s.publishClaimRedeemed(tx, ownerID, device)
			return nil
		}

		// 同一个人在自己的设备上兑换自己的 token：沿用原来的设备记录，
		// 不再新挂一条闲置的设备（#36）。cookie 也不用换——原来那个继续有效，
		// 换成一个新 token 反而会让用户手里的 cookie 作废。
		if currentUserID == ownerID && currentDeviceID != "" {
			if err := consumeClaimToken(ctx, tx, token, now); err != nil {
				return err
			}
			user, err := loadUser(ctx, tx, ownerID)
			if err != nil {
				return err
			}
			device, err := loadDevice(ctx, tx, currentDeviceID)
			if err != nil {
				return err
			}
			result = Identity{User: user, Device: device}
			return nil
		}

		// 这台设备还没有身份：消耗 token，给它挂一条新记录。
		if err := consumeClaimToken(ctx, tx, token, now); err != nil {
			return err
		}
		device, err := insertDevice(ctx, tx, now, ownerID, deviceToken, userAgent)
		if err != nil {
			return err
		}
		issuedToken = deviceToken
		user, err := loadUser(ctx, tx, ownerID)
		if err != nil {
			return err
		}
		result = Identity{User: user, Device: device}
		s.publishClaimRedeemed(tx, ownerID, device)
		return nil
	})
	if err != nil {
		return Identity{}, "", err
	}
	return result, issuedToken, nil
}

// consumeClaimToken 消耗一次性 token：这一条语句同时保证「只能用一次」和「过期作废」（§5.2）。
func consumeClaimToken(ctx context.Context, tx *store.Tx, token string, now time.Time) error {
	var ownerID string
	err := tx.QueryRowContext(ctx, `
		UPDATE claim_tokens SET used_at = ?
		 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?
		RETURNING user_id`,
		now.Unix(), hashToken(token), now.Unix()).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrClaimInvalid
	}
	return err
}

// RedeemByCode 用 6 位认领码兑换身份。只允许还没有身份的设备使用（§5.2 修订）。
//
// 多层防护：
//   - 只查「有效」的记录（未使用且未过期），所以旧记录不会挡住新的同号码；
//   - 签发和兑换必须来自同一个网络前缀；
//   - 单个码最多试 5 次；
//   - 全站失败太多时整体暂停凭码兑换（s.codeBudget）。
//
// 所有失败都返回同一个 ErrClaimInvalid，不透露码是否存在（§10.3）。
func (s *Service) RedeemByCode(ctx context.Context, code, clientNet, userAgent string) (Identity, string, error) {
	if !isClaimCode(code) || s.codeBudget.suspended() {
		s.codeBudget.recordFailure()
		return Identity{}, "", ErrClaimInvalid
	}

	deviceToken := newToken(DeviceTokenBytes)
	now := s.clk.Now()

	var result Identity
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		var (
			rowid     int64
			userID    string
			attempts  int
			issuedNet sql.NullString
		)
		// 只看有效的记录：用过的、过期的都不参与，也就挡不住后续的同号码。
		err := tx.QueryRowContext(ctx, `
			SELECT rowid, user_id, attempts, issued_net
			  FROM claim_tokens
			 WHERE code = ? AND used_at IS NULL AND expires_at > ?
			 ORDER BY rowid DESC LIMIT 1`, code, now.Unix()).
			Scan(&rowid, &userID, &attempts, &issuedNet)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrClaimInvalid
		}
		if err != nil {
			return err
		}

		// 只能在签发时的同一个网络里兑换。
		if issuedNet.Valid && issuedNet.String != "" && issuedNet.String != clientNet {
			return ErrClaimInvalid
		}

		// 已经试满 5 次的码直接作废，连尝试都不再记。
		if attempts >= ClaimCodeMaxAttempts {
			return ErrClaimInvalid
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE claim_tokens SET attempts = attempts + 1 WHERE rowid = ?`, rowid); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE claim_tokens SET used_at = ? WHERE rowid = ?`, now.Unix(), rowid); err != nil {
			return err
		}

		device, err := insertDevice(ctx, tx, now, userID, deviceToken, userAgent)
		if err != nil {
			return err
		}
		user, err := loadUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		result = Identity{User: user, Device: device}
		s.publishClaimRedeemed(tx, userID, device)
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

// loadDevice 读一台设备。
func loadDevice(ctx context.Context, tx *store.Tx, deviceID string) (Device, error) {
	var (
		d    Device
		seen int64
	)
	err := tx.QueryRowContext(ctx,
		`SELECT id, COALESCE(ua, ''), last_seen FROM devices WHERE id = ?`, deviceID).
		Scan(&d.ID, &d.UserAgent, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, err
	}
	d.LastSeen = time.Unix(seen, 0)
	return d, nil
}

// Devices 列出某个用户的全部设备，供 10「我的」显示（§10.3）。
func (s *Service) Devices(ctx context.Context, userID string) ([]Device, error) {
	rows, err := s.st.Read().QueryContext(ctx, `
		SELECT id, COALESCE(ua, ''), last_seen
		  FROM devices WHERE user_id = ? ORDER BY last_seen DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("列出设备: %w", err)
	}
	defer rows.Close()

	var out []Device
	for rows.Next() {
		var (
			d    Device
			seen int64
		)
		if err := rows.Scan(&d.ID, &d.UserAgent, &seen); err != nil {
			return nil, err
		}
		d.LastSeen = time.Unix(seen, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// SignOutDevice 让当前用户退出另一台设备（§10.3）。只能退自己的设备。
func (s *Service) SignOutDevice(ctx context.Context, userID, deviceID string) error {
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		var owned int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM devices WHERE id = ? AND user_id = ?`, deviceID, userID).Scan(&owned); err != nil {
			return err
		}
		if owned == 0 {
			return ErrDeviceNotFound
		}
		// 先删这台设备的推送订阅：push_subscriptions.device_id 引用 devices(id)，订阅还在时删不掉设备；
		// 被退出的设备（可能是被盗的会话）也不能再收到推送（§10.3，#48 复审）。
		if _, err := tx.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE device_id = ?`, deviceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM devices WHERE id = ? AND user_id = ?`, deviceID, userID); err != nil {
			return err
		}
		// 被退出的设备（可能是被盗的会话）立刻断开实时连接（§10.3、#43）。
		s.kickDeviceAfterCommit(tx, deviceID)
		return nil
	})
}

// insertDevice 给用户新挂一台设备。
func insertDevice(ctx context.Context, tx *store.Tx, now time.Time, userID, deviceToken, userAgent string) (Device, error) {
	d := Device{
		ID:        newID(now),
		UserAgent: userAgent,
		LastSeen:  now,
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO devices (id, user_id, token_hash, ua, last_seen) VALUES (?, ?, ?, ?, ?)`,
		d.ID, userID, hashToken(deviceToken), nullIfEmpty(userAgent), now.Unix())
	if err != nil {
		return Device{}, err
	}
	return d, nil
}

// loadUser 在读事务里取用户。
func loadUser(ctx context.Context, tx *store.Tx, userID string) (User, error) {
	var (
		u       User
		created int64
	)
	err := tx.QueryRowContext(ctx, `SELECT id, nickname, created_at FROM users WHERE id = ?`, userID).
		Scan(&u.ID, &u.Nickname, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = time.Unix(created, 0)
	return u, nil
}

// resolveMergedUser 顺着 merged_into 一路走到最终的用户（§10.4）。链上最多走 8 步，防止脏数据打转。
func (s *Service) resolveMergedUser(ctx context.Context, userID string) (User, error) {
	for i := 0; i < 8; i++ {
		var (
			u          User
			created    int64
			mergedInto sql.NullString
		)
		err := s.st.Read().QueryRowContext(ctx,
			`SELECT id, nickname, created_at, merged_into FROM users WHERE id = ?`, userID).
			Scan(&u.ID, &u.Nickname, &created, &mergedInto)
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		if err != nil {
			return User{}, err
		}
		u.CreatedAt = time.Unix(created, 0)
		if !mergedInto.Valid || mergedInto.String == "" {
			return u, nil
		}
		userID = mergedInto.String
	}
	return User{}, fmt.Errorf("身份合并链过长，起点 %s", userID)
}

// touchDevice 隔一段时间才刷新一次 last_seen，避免每个请求都去抢唯一的写连接。
func (s *Service) touchDevice(ctx context.Context, deviceID string, lastSeen time.Time) error {
	if s.clk.Since(lastSeen) < touchInterval {
		return nil
	}
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE devices SET last_seen = ? WHERE id = ?`, s.clk.Now().Unix(), deviceID)
		return err
	})
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isClaimCode 检查是不是 ClaimCodeDigits 位数字。
func isClaimCode(code string) bool {
	if len(code) != ClaimCodeDigits {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
