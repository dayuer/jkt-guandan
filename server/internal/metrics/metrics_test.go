package metrics_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/metrics"
)

func TestRegistryOutput(t *testing.T) {
	var r metrics.Registry
	r.Info("guandan_build_info", "构建号。", "build", "abc123")
	h := metrics.NewHTTP(&r)
	h.Observe(200, 30*time.Millisecond)
	h.Observe(204, 2*time.Second)
	h.Observe(503, time.Millisecond)
	r.Gauge("guandan_outbox_pending", "积压。", func(context.Context) (float64, error) { return 7, nil })
	r.Gauge("guandan_broken", "坏掉的一项。", func(context.Context) (float64, error) { return 0, errors.New("x") })

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	out := string(body)

	for _, want := range []string{
		"# TYPE guandan_build_info gauge\nguandan_build_info{build=\"abc123\"} 1\n",
		"guandan_http_requests_total{code=\"2xx\"} 2\n",
		"guandan_http_requests_total{code=\"5xx\"} 1\n",
		"guandan_http_request_duration_seconds_bucket{le=\"0.05\"} 2\n",
		"guandan_http_request_duration_seconds_bucket{le=\"+Inf\"} 3\n",
		"guandan_http_request_duration_seconds_count 3\n",
		"guandan_outbox_pending 7\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里缺少 %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "guandan_broken") {
		t.Error("读取失败的指标不该输出")
	}
	if resp.Header.Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type %s", resp.Header.Get("Content-Type"))
	}
}

func TestNilHTTPObserveIsNoop(t *testing.T) {
	var h *metrics.HTTP
	h.Observe(200, time.Second) // 不 panic
}
