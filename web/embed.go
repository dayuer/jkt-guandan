// Package web 把前端构建产物 web/dist 打进二进制，让页面和 API 同源
// （docs/server-design.md §2、§7）。
//
// dist 目录里平时只有 .gitkeep；构建时先跑 web 的 `npm run build` 再编译 Go。
// 用 all: 前缀是为了让 .gitkeep 也被匹配到，否则空目录下 embed 会编译失败。
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
