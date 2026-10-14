package wxapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
)

// access_token：缓存到过期前 5 分钟；快过期时换新的；接口说 token 无效时作废，下次带 force_refresh 重新拿。
func TestStableTokenCacheRefreshAndInvalidate(t *testing.T) {
	var tokenCalls, forced atomic.Int32
	var lastToken atomic.Value
	var code atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["appid"] != "wxapp" || body["secret"] != "sec" || body["grant_type"] != "client_credential" {
				t.Errorf("stable_token 请求 %v", body)
			}
			if body["force_refresh"] == true {
				forced.Add(1)
			}
			fmt.Fprintf(w, `{"access_token":"tok%d","expires_in":7200}`, tokenCalls.Add(1))
		case "/x":
			lastToken.Store(r.URL.Query().Get("access_token"))
			fmt.Fprintf(w, `{"errcode":%d,"errmsg":"m","n":7}`, code.Load())
		}
	}))
	defer srv.Close()

	clk := clock.NewFake(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
	c := &Client{BaseURL: srv.URL, Tokens: &StableToken{AppID: "wxapp", Secret: "sec", BaseURL: srv.URL, Clock: clk}}
	ctx := context.Background()
	var out struct{ N int }
	for i := 0; i < 3; i++ {
		if err := c.PostJSON(ctx, "/x", map[string]string{}, &out); err != nil || out.N != 7 {
			t.Fatalf("%v %+v", err, out)
		}
	}
	if tokenCalls.Load() != 1 || lastToken.Load() != "tok1" {
		t.Fatalf("token 应缓存：取了 %d 次", tokenCalls.Load())
	}

	clk.Advance(2*time.Hour - 4*time.Minute) // 进入过期前 5 分钟
	_ = c.PostJSON(ctx, "/x", nil, nil)
	if tokenCalls.Load() != 2 || lastToken.Load() != "tok2" {
		t.Fatalf("快过期时应刷新：取了 %d 次，用的 %v", tokenCalls.Load(), lastToken.Load())
	}

	code.Store(ErrCodeTokenInvalid)
	err := c.PostJSON(ctx, "/x", nil, nil)
	if got, ok := Code(err); !ok || got != ErrCodeTokenInvalid || !Retryable(err) {
		t.Fatalf("40001 应返回 *Error 且可重试：%v", err)
	}
	code.Store(0)
	_ = c.PostJSON(ctx, "/x", nil, nil)
	if tokenCalls.Load() != 3 || forced.Load() != 1 || lastToken.Load() != "tok3" {
		t.Fatalf("40001 后应强制刷新：取了 %d 次，强制 %d 次", tokenCalls.Load(), forced.Load())
	}
}

// 错误：errcode 非 0 是 *Error（只有限频、繁忙、token 问题可重试）；没拿到回应包着 ErrUnavailable；
// 错误文字里不带 URL（access_token 在查询串里）。
func TestErrors(t *testing.T) {
	var code atomic.Int32
	var status atomic.Int32
	status.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/stable_token" {
			fmt.Fprint(w, `{"access_token":"tok-secret","expires_in":7200}`)
			return
		}
		w.WriteHeader(int(status.Load()))
		fmt.Fprintf(w, `{"errcode":%d}`, code.Load())
	}))
	c := &Client{BaseURL: srv.URL, Tokens: &StableToken{BaseURL: srv.URL}}
	ctx := context.Background()

	for c0, retry := range map[int]bool{ErrCodeBusy: true, ErrCodeRateLimited: true, 47003: false, 43101: false} {
		code.Store(int32(c0))
		err := c.PostJSON(ctx, "/x", nil, nil)
		if got, _ := Code(err); got != c0 || Retryable(err) != retry {
			t.Errorf("errcode %d：%v retry=%v", c0, err, Retryable(err))
		}
	}
	code.Store(0)
	status.Store(502)
	if err := c.PostJSON(ctx, "/x", nil, nil); !errors.Is(err, ErrUnavailable) || !Retryable(err) {
		t.Fatalf("502 应是 ErrUnavailable：%v", err)
	}
	srv.Close()
	err := c.PostJSON(ctx, "/x", nil, nil)
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "tok-secret") || strings.Contains(err.Error(), "http") {
		t.Fatalf("网络错误应是 ErrUnavailable 且不带 URL：%v", err)
	}
}
