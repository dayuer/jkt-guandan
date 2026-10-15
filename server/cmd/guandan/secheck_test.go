package main

import (
	"context"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/config"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/metrics"
	"github.com/dayuer/jkt-guandan/server/internal/secheck"
	"github.com/dayuer/jkt-guandan/server/internal/wxapi"
)

// fixedTokenSource 是个够用的假 token 源（内容安全这边的测试只关心接线，不关心 token）。
type fixedTokenSource struct{}

func (fixedTokenSource) Token(context.Context) (string, error) { return "tok", nil }
func (fixedTokenSource) Invalidate(string, string, int)        {}

// #146：内容安全检测的开关跟着 GUANDAN_WX_APPID / GUANDAN_WX_SECRET 走；开发模式用假检测器。
func TestNewContentGuard(t *testing.T) {
	if g := newContentGuard(config.Config{}, nil, nil, &metrics.Registry{}); g != nil {
		t.Fatalf("生产没配密钥应关闭：%+v", g)
	}

	g := newContentGuard(config.Config{Dev: true}, nil, nil, &metrics.Registry{})
	if g == nil || g.AppID != identity.DevWxAppID {
		t.Fatalf("开发模式应查 wx_openid:dev：%+v", g)
	}
	if _, ok := g.Checker.(secheck.DevChecker); !ok {
		t.Fatalf("开发模式应用假检测器：%T", g.Checker)
	}

	// 配了密钥、也给了 token 源：开启，调真接口。
	g = newContentGuard(config.Config{Dev: true, WxAppID: "wxapp", WxSecret: "s"}, nil, fixedTokenSource{}, &metrics.Registry{})
	if g == nil || g.AppID != "wxapp" {
		t.Fatalf("配了密钥应开启：%+v", g)
	}
	if _, ok := g.Checker.(secheck.WxChecker); !ok {
		t.Fatalf("配了密钥应调真接口：%T", g.Checker)
	}

	// 配了密钥却没给 token 源（#190 复审意见 a）：这是接线接错了，**关掉**。
	// 照旧建一个 wxapi.Client{Tokens: nil} 的话，第一次检测就会踩 nil 接口 panic；
	// 自己再建一个 StableToken 又违反 #159（两份缓存互相顶）。两个都不能要。
	if g := newContentGuard(config.Config{Dev: true, WxAppID: "wxapp", WxSecret: "s"}, nil, nil, &metrics.Registry{}); g != nil {
		t.Fatalf("配了密钥却没给 token 源，该关闭：%+v", g)
	}
}

// 上面那条为什么不能照着老样子建：tokens 为 nil 时第一次调用就会踩空。
// 这条把「会 panic」本身钉住 —— newContentGuard 里的防守不是多余的。
func TestWxCheckerWithNilTokensPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Tokens 为 nil 时该 panic；说明 newContentGuard 直接建 Client 的写法是真会炸的")
		}
	}()
	_, _ = secheck.WxChecker{API: &wxapi.Client{Tokens: nil}}.
		Check(context.Background(), "openid", 1, "随便一句")
}
