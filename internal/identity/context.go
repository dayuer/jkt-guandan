package identity

import (
	"context"
	"net/http"
)

// 请求的身份由 httpapi 的身份中间件统一解析（#41），放进 ctx；各业务包从这里取，不再各自查一遍。

type ctxKey struct{}

type requestIdentity struct {
	ident  *Identity
	banned bool
}

// NewContext 把解析好的身份（nil 表示访客）和封禁状态放进 ctx。只由身份中间件调用。
func NewContext(ctx context.Context, ident *Identity, banned bool) context.Context {
	return context.WithValue(ctx, ctxKey{}, requestIdentity{ident: ident, banned: banned})
}

// FromContext 取出当前请求的身份；访客、或者路由不识别身份（route.Anonymous）时返回 nil。
func FromContext(ctx context.Context) *Identity {
	v, _ := ctx.Value(ctxKey{}).(requestIdentity)
	return v.ident
}

// BannedFromContext 说明当前身份是否被封禁。只有 route.DeferBan 的路由（GET /ws）需要看它，
// 其余路由里被封禁的身份已经被中间件 403 挡掉了。
func BannedFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKey{}).(requestIdentity)
	return v.banned
}

// UserID 返回当前用户 id；访客返回空串。
func UserID(ctx context.Context) string {
	if ident := FromContext(ctx); ident != nil {
		return ident.User.ID
	}
	return ""
}

// Resolve 是身份中间件用的：从 cookie 认出身份并查封禁（§13.6）。没有身份返回 nil, false, nil。
func (s *Service) Resolve(r *http.Request) (ident *Identity, banned bool, err error) {
	ident = s.FromRequest(r)
	if ident == nil {
		return nil, false, nil
	}
	banned, err = s.Banned(r.Context(), ident)
	return ident, banned, err
}

// ForRealtime 是 WebSocket 握手时的身份（§5.5）：从中间件放进 ctx 的身份里取。没有身份返回空字符串；
// 被封禁返回 ErrBanned，握手随之 403。和心跳时的 StillValid 是同一套规则。
func ForRealtime(r *http.Request) (userID, deviceID string, err error) {
	if BannedFromContext(r.Context()) {
		return "", "", ErrBanned
	}
	ident := FromContext(r.Context())
	if ident == nil {
		return "", "", nil
	}
	return ident.User.ID, ident.Device.ID, nil
}
