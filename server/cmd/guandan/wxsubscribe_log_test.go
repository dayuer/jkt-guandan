package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/config"
)

// #190 复审意见 b：关掉订阅消息时打的日志，别把原因说错。
//
// 原来只有两种说法：「配了模板但没配 GUANDAN_WX_APPID / GUANDAN_WX_SECRET」和「没配模板」。
// 但「配了 AppID / Secret 却没给 token 源」（接线接错）也会落进第一种，于是日志会告诉运维
// 去配一个其实已经配好的东西 —— 人被带偏不说，真正的原因（接线）反而看不见。
func TestSetupWxSubscribeLogsTheRightReason(t *testing.T) {
	tmpl := config.WxTemplate{ID: "t1"}

	for _, c := range []struct {
		name     string
		cfg      config.Config
		want     []string
		notWant  []string
		nilToken bool
	}{
		{
			name:     "配了模板、没配 AppID / Secret：明说没配密钥",
			cfg:      config.Config{WxTmplFull: tmpl},
			want:     []string{"没配 GUANDAN_WX_APPID / GUANDAN_WX_SECRET"},
			nilToken: true,
		},
		{
			name:     "AppID / Secret 配了、token 源没给：**不要说没配密钥**",
			cfg:      config.Config{WxTmplFull: tmpl, WxAppID: "wxapp", WxSecret: "s"},
			want:     []string{"没有传 token 源"},
			notWant:  []string{"没配 GUANDAN_WX_APPID / GUANDAN_WX_SECRET"},
			nilToken: true,
		},
		{
			name:     "模板都没配：说没配模板",
			cfg:      config.Config{},
			want:     []string{"没有配置订阅消息模板"},
			nilToken: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			defer slog.SetDefault(prev)

			// 三种情况都走不到「通道建成了」那一支，所以 svc 用不上，给 nil 就行。
			setupWxSubscribe(nil, c.cfg, nil)

			got := logs.String()
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("日志该提到 %q，实际：%s", w, got)
				}
			}
			for _, nw := range c.notWant {
				if strings.Contains(got, nw) {
					t.Errorf("日志**不该**说 %q（其实配了，说了会把人带偏），实际：%s", nw, got)
				}
			}
		})
	}
}
