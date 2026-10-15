package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// #190 复审意见 c：**「全仓只建一个 access_token 缓存」这件事没有测试守着**。
//
// 内容安全（#146）和订阅消息（#137）各建一个 StableToken 的话，任何一方收到 40001 强刷，
// 都会让另一方缓存里的 token 立刻失效，两边互相顶（#159 修的就是这个）。现在的做法是
// main.go 建一次、注入给两边。但以后有人在 main 里再写一个 `&wxapi.StableToken{...}`，
// 不会有任何测试变红 —— 这条就是那个测试。
//
// 用 AST 而不是正则扫源码：注释和字符串里出现 `StableToken` 不该算数。
func TestSingleStableTokenWiredToBothConsumers(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		// 只看包里的**非测试**文件：测试自己建 StableToken 是为了验证，不算接线。
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("解析包失败：%v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("本目录该只有一个包，实际 %d 个", len(pkgs))
	}

	// 1) 整个包里有几处建 StableToken？必须是 1 处。
	var built []string
	// 2) 那一处建出来赋给了谁？名字要能对上后面两个调用点。
	builtNames := map[string]bool{}
	for _, pkg := range pkgs {
		ast.Inspect(pkg, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isStableTokenType(lit.Type) {
				return true
			}
			built = append(built, fset.Position(lit.Pos()).String())
			return true
		})
		// 赋值语句：`wxTokens = &wxapi.StableToken{...}`，以及 `x := wxTokens` 这种别名。
		// 别名要跟着走，否则 `t := wxTokens` 再接给使用方会被误判成「又建了一个」。
		for pass := 0; pass < 4; pass++ {
			for _, f := range pkg.Files {
				ast.Inspect(f, func(n ast.Node) bool {
					as, ok := n.(*ast.AssignStmt)
					if !ok || len(as.Lhs) != len(as.Rhs) {
						return true
					}
					for i, rhs := range as.Rhs {
						id, ok := unparen(as.Lhs[i]).(*ast.Ident)
						if !ok {
							continue
						}
						if _, ok := asStableTokenLit(rhs); ok {
							builtNames[id.Name] = true
							continue
						}
						if src, ok := unparen(rhs).(*ast.Ident); ok && builtNames[src.Name] {
							builtNames[id.Name] = true
						}
					}
					return true
				})
			}
		}
	}

	if len(built) != 1 {
		t.Fatalf("全包该只建一个 wxapi.StableToken（内容安全和订阅消息共用一个缓存，#159），实际 %d 个：%v",
			len(built), built)
	}
	if len(builtNames) == 0 {
		t.Fatal("建出来的 StableToken 没有接给任何变量，接不到使用方")
	}

	// 3) 两个使用方都得拿到**同一个**变量。
	var gotGuard, gotSubscribe []string
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := calleeName(call.Fun)
				if name != "newContentGuard" && name != "setupWxSubscribe" {
					return true
				}
				// 只看真正的调用点，不看函数定义。
				if filepath.Base(fset.Position(call.Pos()).Filename) != "main.go" {
					return true
				}
				for _, a := range call.Args {
					if id, ok := unparen(a).(*ast.Ident); ok && builtNames[id.Name] {
						if name == "newContentGuard" {
							gotGuard = append(gotGuard, id.Name)
						} else {
							gotSubscribe = append(gotSubscribe, id.Name)
						}
					}
				}
				return true
			})
		}
	}
	// 上面已经确认全包只有一个 StableToken 字面量，所以任何一个（跟着别名找过去的）名字
	// 指的都是同一个实例。每个使用方只要**接到了一个**就对了：接 nil 或者接别的都不行。
	for _, c := range []struct {
		who string
		got []string
	}{
		{"newContentGuard（内容安全）", gotGuard},
		{"setupWxSubscribe（订阅消息）", gotSubscribe},
	} {
		if len(c.got) != 1 {
			t.Errorf("%s 该接到 main 里建的那个 token 源，实际 %v —— 接 nil 会 panic，"+
				"两边各建一个的话任何一方 40001 强刷都会把另一方顶掉（#159）", c.who, c.got)
		}
	}
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// asStableTokenLit 剥掉括号和取地址，判断是不是 `&wxapi.StableToken{…}`，是就返回那个字面量。
func asStableTokenLit(e ast.Expr) (*ast.CompositeLit, bool) {
	e = unparen(e)
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = unparen(u.X)
	}
	lit, ok := e.(*ast.CompositeLit)
	if !ok || !isStableTokenType(lit.Type) {
		return nil, false
	}
	return lit, true
}

// isStableTokenType 判断 `wxapi.StableToken` 这种类型。
func isStableTokenType(e ast.Expr) bool {
	sel, ok := unparen(e).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "StableToken" {
		return false
	}
	pkg, ok := unparen(sel.X).(*ast.Ident)
	return ok && pkg.Name == "wxapi"
}

func calleeName(e ast.Expr) string {
	switch f := unparen(e).(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}
