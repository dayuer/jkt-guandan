package main

import (
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/config"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/metrics"
	"github.com/dayuer/jkt-guandan/server/internal/secheck"
)

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

	g = newContentGuard(config.Config{Dev: true, WxAppID: "wxapp", WxSecret: "s"}, nil, nil, &metrics.Registry{})
	if g == nil || g.AppID != "wxapp" {
		t.Fatalf("配了密钥应开启：%+v", g)
	}
	if _, ok := g.Checker.(secheck.WxChecker); !ok {
		t.Fatalf("配了密钥应调真接口：%T", g.Checker)
	}
}
