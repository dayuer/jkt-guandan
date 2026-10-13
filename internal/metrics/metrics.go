// Package metrics 输出 Prometheus 文本格式的 /metrics（docs/server-design.md §13.7）。
//
// 只实现用得到的三种：带一个标签的计数器、直方图、读取时现算的仪表。
// 不引入 client_golang，避免一大串依赖；格式按 Prometheus text exposition 0.0.4。
package metrics

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CounterVec 是只有一个标签的计数器。
type CounterVec struct {
	mu   sync.Mutex
	vals map[string]uint64
}

func (c *CounterVec) Inc(label string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.vals == nil {
		c.vals = make(map[string]uint64)
	}
	c.vals[label]++
}

func (c *CounterVec) snapshot() map[string]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]uint64, len(c.vals))
	for k, v := range c.vals {
		out[k] = v
	}
	return out
}

// Histogram 是累积直方图，单位秒。
type Histogram struct {
	mu      sync.Mutex
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

// NewHistogram 用给定的上界（升序）建直方图，+Inf 自动补上。
func NewHistogram(buckets ...float64) *Histogram {
	return &Histogram{buckets: buckets, counts: make([]uint64, len(buckets))}
}

func (h *Histogram) Observe(d time.Duration) {
	v := d.Seconds()
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i]++
		}
	}
	h.sum += v
	h.count++
}

// Gauge 在每次抓取时现算。返回 error 时这一项不输出，并记日志。
type Gauge func(ctx context.Context) (float64, error)

type entry struct {
	name, help, kind string
	write            func(ctx context.Context, w io.Writer, name string) error
}

// Registry 按登记顺序输出各项指标。
type Registry struct {
	mu      sync.Mutex
	entries []entry
}

func (r *Registry) add(e entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

func (r *Registry) CounterVec(name, help, label string, c *CounterVec) {
	r.add(entry{name: name, help: help, kind: "counter", write: func(_ context.Context, w io.Writer, name string) error {
		vals := c.snapshot()
		keys := make([]string, 0, len(vals))
		for k := range vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "%s{%s=%q} %d\n", name, label, k, vals[k])
		}
		return nil
	}})
}

func (r *Registry) Histogram(name, help string, h *Histogram) {
	r.add(entry{name: name, help: help, kind: "histogram", write: func(_ context.Context, w io.Writer, name string) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, b := range h.buckets {
			fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", name, formatFloat(b), h.counts[i])
		}
		fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, h.count)
		fmt.Fprintf(w, "%s_sum %s\n", name, formatFloat(h.sum))
		fmt.Fprintf(w, "%s_count %d\n", name, h.count)
		return nil
	}})
}

func (r *Registry) Gauge(name, help string, g Gauge) {
	r.add(entry{name: name, help: help, kind: "gauge", write: func(ctx context.Context, w io.Writer, name string) error {
		v, err := g(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s %s\n", name, formatFloat(v))
		return nil
	}})
}

// Info 输出一个值恒为 1、带标签的仪表，如 guandan_build_info{build="abc"} 1。
func (r *Registry) Info(name, help, label, value string) {
	r.add(entry{name: name, help: help, kind: "gauge", write: func(_ context.Context, w io.Writer, name string) error {
		fmt.Fprintf(w, "%s{%s=%q} 1\n", name, label, value)
		return nil
	}})
}

// WriteTo 输出全部指标。单项出错时跳过它，其他照常输出。
func (r *Registry) WriteTo(ctx context.Context, w io.Writer) {
	r.mu.Lock()
	entries := append([]entry(nil), r.entries...)
	r.mu.Unlock()
	for _, e := range entries {
		var b strings.Builder
		if err := e.write(ctx, &b, e.name); err != nil {
			slog.Warn("指标读取失败", "metric", e.name, "err", err)
			continue
		}
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s", e.name, e.help, e.name, e.kind, b.String())
	}
}

// Handler 是 /metrics。它挂在单独的监听地址上（默认 127.0.0.1:9090），不和对外服务共用端口。
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/metrics" {
			http.NotFound(w, req)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.WriteTo(ctx, w)
	})
}

func formatFloat(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// HTTP 是 httpapi 中间件记录的请求指标。
type HTTP struct {
	Requests CounterVec // 按状态码分类：2xx、3xx、4xx、5xx
	Duration *Histogram
}

// NewHTTP 建请求指标并登记到 r。
func NewHTTP(r *Registry) *HTTP {
	h := &HTTP{Duration: NewHistogram(.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10)}
	r.CounterVec("guandan_http_requests_total", "HTTP 请求数，按状态码分类。", "code", &h.Requests)
	r.Histogram("guandan_http_request_duration_seconds", "HTTP 请求耗时。", h.Duration)
	return h
}

// Observe 记一次请求。h 为 nil 时什么都不做，方便测试里不接指标。
func (h *HTTP) Observe(status int, d time.Duration) {
	if h == nil {
		return
	}
	h.Requests.Inc(strconv.Itoa(status/100) + "xx")
	h.Duration.Observe(d)
}
