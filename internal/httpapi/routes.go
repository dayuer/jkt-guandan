package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/dayuer/jkt-guandan/internal/identity"
	"github.com/dayuer/jkt-guandan/internal/route"
)

// Table 是整个服务的路由表（#41）：基础设施的几条、各业务包的路由（opts.Routes），
// 最后是兜底的前端页面。New 按它注册，路由表测试和授权矩阵测试也遍历它。
func Table(opts Options) []route.Route {
	return newServer(opts).table(opts.Routes)
}

func (s *Server) table(app []route.Route) []route.Route {
	t := []route.Route{
		{Method: http.MethodGet, Pattern: "/healthz", Access: route.Anonymous, Handler: http.HandlerFunc(s.healthz)},
		{Method: http.MethodGet, Pattern: "/readyz", Access: route.Anonymous, Handler: http.HandlerFunc(s.readyz)},
		{Method: http.MethodGet, Pattern: "/api/config", Access: route.Anonymous, Handler: http.HandlerFunc(s.publicConfig)},
	}
	t = append(t, app...)
	// 兜底放在最后：它匹配所有剩下的路径，交给内嵌的前端。
	return append(t, route.Route{Pattern: "/", Access: route.Anonymous, Handler: s.assets()})
}

// mount 把路由表挂到 mux 上，每条按它的 Access 套上身份中间件。
// 同一个「方法 路径」出现两次、或者需要识别身份却没有 identity.Service，属于写代码时的错误，启动就 panic。
func mount(mux *http.ServeMux, ident *identity.Service, routes []route.Route) {
	seen := map[string]bool{}
	for _, rt := range routes {
		key := rt.Key()
		if rt.Method == "" {
			key = rt.Pattern
		}
		if seen[key] {
			panic("httpapi: 路由重复注册: " + key)
		}
		seen[key] = true
		if rt.Handler == nil {
			panic("httpapi: 路由没有 handler: " + key)
		}
		if rt.Access != route.Anonymous && ident == nil {
			panic(fmt.Sprintf("httpapi: %s 要识别身份（%s），但没有传 identity.Service", key, rt.Access))
		}
		mux.Handle(key, withIdentity(ident, rt))
	}
}

// withIdentity 是唯一的身份入口（#41）：从 cookie 认出身份、查一次封禁（§13.6），按路由的 Access 放行，
// 把身份放进 ctx。各业务包用 identity.FromContext 取，不再各自查。
//
//   - Anonymous：不读 cookie，原样交给 handler；
//   - DeferIdentity：不马上识别，ctx 里放一个入口，由 handler 在需要时调用（同一个 Resolve）；
//   - 被封禁的身份：403 banned；
//   - User 路由没有身份：401 unauthorized。
func withIdentity(ident *identity.Service, rt route.Route) http.Handler {
	if rt.Access == route.Anonymous {
		return rt.Handler
	}
	if rt.DeferIdentity {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := identity.NewLazyContext(r.Context(), func() (*identity.Identity, bool, error) { return ident.Resolve(r) })
			rt.Handler.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, banned, err := ident.Resolve(r)
		if err != nil {
			slog.Error("查封禁状态失败", "err", err)
			writeError(w, http.StatusServiceUnavailable, "unavailable", "暂时无法处理，请稍后再试")
			return
		}
		if banned {
			writeError(w, http.StatusForbidden, "banned", "这个账号已被停用")
			return
		}
		if who == nil && !rt.Access.AllowsGuest() {
			writeError(w, http.StatusUnauthorized, "unauthorized", "还没有身份")
			return
		}
		rt.Handler.ServeHTTP(w, r.WithContext(identity.NewContext(r.Context(), who, banned)))
	})
}
