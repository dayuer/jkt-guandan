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
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
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

// Service 是身份系统的业务入口。
type Service struct {
	st      *store.Store
	clk     clock.Clock
	cookies CookiePolicy

	createLimiter *Limiter // POST /api/me：每 IP 每小时 20 次（§12.3）
	codeLimiter   *Limiter // 认领码兑换：每 IP 每分钟 10 次（§12.3）
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
		createLimiter: NewLimiter(20.0/60, 20, clk),
		codeLimiter:   NewLimiter(10, 10, clk),
	}
}

// Cookies 让接口层用同一份 cookie 策略读写（§10.3）。
func (s *Service) Cookies() CookiePolicy { return s.cookies }

// AllowCreate 检查这个 IP 现在能不能创建身份。
func (s *Service) AllowCreate(ip string) bool { return s.createLimiter.Allow(ip) }

// AllowCodeRedeem 检查这个 IP 现在能不能试认领码。
func (s *Service) AllowCodeRedeem(ip string) bool { return s.codeLimiter.Allow(ip) }

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
// 返回的 deviceToken 只在这次真的新建了设备时非空，前端拿到后镜像一份到 localStorage。
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

// IssueClaim 签发一次性交接令牌和 4 位认领码（§5.2）。
func (s *Service) IssueClaim(ctx context.Context, userID string) (token, code string, expiresAt time.Time, err error) {
	token = newToken(ClaimTokenBytes)
	code = newClaimCode()
	expiresAt = s.clk.Now().Add(ClaimTTL)

	err = s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO claim_tokens (token_hash, code, user_id, expires_at, used_at, attempts)
			 VALUES (?, ?, ?, ?, NULL, 0)`,
			hashToken(token), code, userID, expiresAt.Unix())
		return err
	})
	if err != nil {
		return "", "", time.Time{}, err
	}
	return token, code, expiresAt, nil
}

// RedeemByToken 用 claim token 兑换身份：换出同一个 user_id，并给这台设备发新的 device_token。
//
// 只有前端的 POST 会走到这里。抓取链接预览的机器人只发 GET，所以不会把一次性 token 用掉（§10.3）。
// currentUserID 是这台设备当前（可能是误建的）身份，会和 token 指向的身份合并。
func (s *Service) RedeemByToken(ctx context.Context, token, userAgent, currentUserID string) (Identity, string, error) {
	if token == "" {
		return Identity{}, "", ErrClaimInvalid
	}

	deviceToken := newToken(DeviceTokenBytes)
	now := s.clk.Now()

	var result Identity
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		// 这一条语句同时保证「只能用一次」和「过期作废」（§5.2）。
		var userID string
		err := tx.QueryRowContext(ctx, `
			UPDATE claim_tokens SET used_at = ?
			 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?
			RETURNING user_id`,
			now.Unix(), hashToken(token), now.Unix()).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrClaimInvalid
		}
		if err != nil {
			return err
		}

		primary := userID
		if currentUserID != "" && currentUserID != userID {
			primary, err = mergeUsers(ctx, tx, currentUserID, userID)
			if err != nil {
				return err
			}
		}

		device, err := insertDevice(ctx, tx, now, primary, deviceToken, userAgent)
		if err != nil {
			return err
		}
		user, err := loadUser(ctx, tx, primary)
		if err != nil {
			return err
		}
		result = Identity{User: user, Device: device}
		return nil
	})
	if err != nil {
		return Identity{}, "", err
	}
	return result, deviceToken, nil
}

// RedeemByCode 用 4 位认领码兑换身份。只允许还没有身份的设备使用（§5.2）。
func (s *Service) RedeemByCode(ctx context.Context, code, userAgent string) (Identity, string, error) {
	if !isClaimCode(code) {
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
			expiresAt int64
			usedAt    sql.NullInt64
		)
		err := tx.QueryRowContext(ctx, `
			SELECT rowid, user_id, attempts, expires_at, used_at
			  FROM claim_tokens
			 WHERE code = ?
			 ORDER BY rowid DESC LIMIT 1`, code).
			Scan(&rowid, &userID, &attempts, &expiresAt, &usedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrClaimInvalid
		}
		if err != nil {
			return err
		}

		// 已经试满 5 次的码直接作废，连尝试都不再记（§5.2）。
		if attempts >= ClaimCodeMaxAttempts {
			return ErrClaimInvalid
		}
		// 不管这次成不成，都记一次尝试。
		if _, err := tx.ExecContext(ctx,
			`UPDATE claim_tokens SET attempts = attempts + 1 WHERE rowid = ?`, rowid); err != nil {
			return err
		}

		// 已经用过、或者过期了，都返回同一个错误（§10.3）。
		if usedAt.Valid || expiresAt <= now.Unix() {
			return ErrClaimInvalid
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
		return nil
	})
	if err != nil {
		return Identity{}, "", err
	}
	return result, deviceToken, nil
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
		res, err := tx.ExecContext(ctx,
			`DELETE FROM devices WHERE id = ? AND user_id = ?`, deviceID, userID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrDeviceNotFound
		}
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

// isClaimCode 检查是不是 4 位数字。
func isClaimCode(code string) bool {
	if len(code) != 4 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
