// Package migrations 把 SQL 迁移文件打进二进制；启动时按文件名顺序执行
// （docs/server-design.md §2、§5）。部署时不需要额外的迁移工具。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
