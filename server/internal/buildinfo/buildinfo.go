// Package buildinfo 提供构建号（docs/server-design.md §13.4）。
//
// 发布构建用 -ldflags "-X github.com/dayuer/jkt-guandan/server/internal/buildinfo.Build=<提交 SHA>" 写入；
// 没写入时从 Go 的构建信息里取 vcs.revision，再没有就是 "dev"。
package buildinfo

import "runtime/debug"

// Build 由链接参数写入。
var Build string

// Get 返回构建号。
func Get() string {
	if Build != "" {
		return Build
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				return s.Value
			}
		}
	}
	return "dev"
}
