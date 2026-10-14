// Package route 是路由表的条目（#41）。各业务包用它声明自己的路由和授权要求，
// httpapi 按同一张表注册、统一做身份识别和授权，授权矩阵测试也遍历这张表（§10.4、§10.12）。
//
// 单独成包是为了避免循环引用：httpapi 要引用 identity，identity 又要声明自己的路由。
package route

import "net/http"

// Access 是一条路由对身份的要求。
type Access int

const (
	// Anonymous 不识别身份：健康检查、静态页面、分享图片、VAPID 公钥。
	// 这些请求不读身份 cookie，也就不查库、不受封禁影响。
	Anonymous Access = iota
	// Public 识别身份（有就放进 ctx），访客也能用；被封禁的身份返回 403（§13.6）。
	Public
	// User 必须有身份，访客返回 401；被封禁的身份返回 403。
	User
)

func (a Access) String() string {
	switch a {
	case Anonymous:
		return "anonymous"
	case Public:
		return "public"
	case User:
		return "user"
	default:
		return "unknown"
	}
}

// AllowsGuest 说明没有身份的访客能不能用这条路由。
func (a Access) AllowsGuest() bool { return a != User }

// Route 是路由表里的一条。
type Route struct {
	Method  string
	Pattern string // ServeMux 的路径模式，比如 /api/games/{code}
	Access  Access
	// DeferIdentity 为真时，中间件不马上识别身份，只在 ctx 里放一个「需要时再识别」的入口，
	// 由 handler 决定什么时候识别、什么时候拒绝被封禁的身份。只有 GET /ws 用：它要先查 Origin、
	// 是否在关闭、每个 IP 的连接数，过了这几关才查库（识别身份、查封禁），连接上限也就护住了数据库。
	DeferIdentity bool
	Handler       http.Handler
}

// Key 是 ServeMux 用的「方法 路径」。
func (r Route) Key() string { return r.Method + " " + r.Pattern }
