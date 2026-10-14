package share

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/singleflight"
)

// imageCache 缓存渲染好的 PNG（§5.4、§11.5）：
//   - 键是「图的种类 + 短码 + 版本号 + 座位数」，局有任何变化就换键；
//   - 同一个键用 singleflight 合并：链接发进 200 人的群时，抓取器和用户同时来要这张图，只渲染一次；
//   - 最多 2 个并发渲染（出图吃 CPU）；
//   - 内存有上限：超过 maxEntries 就淘汰最早放进去的（§11.5：内存中的结构都有容量上限）。
type imageCache struct {
	mu         sync.Mutex
	entries    map[string][]byte
	order      []string
	maxEntries int

	group   singleflight.Group
	slots   chan struct{}
	renders int // 测试用：实际渲染了几次
}

func newImageCache(maxEntries, concurrency int) *imageCache {
	return &imageCache{entries: make(map[string][]byte), maxEntries: maxEntries, slots: make(chan struct{}, concurrency)}
}

// get 返回 key 对应的 PNG；没有就调用 render 渲染并放进缓存。render 出错时不缓存，返回错误。
func (c *imageCache) get(ctx context.Context, key string, render func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if b, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return b, nil
	}
	c.mu.Unlock()

	v, err, _ := c.group.Do(key, func() (any, error) {
		select {
		case c.slots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-c.slots }()
		b, err := safeRender(render)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.renders++
		if _, ok := c.entries[key]; !ok {
			c.entries[key] = b
			c.order = append(c.order, key)
			for len(c.order) > c.maxEntries {
				delete(c.entries, c.order[0])
				c.order = c.order[1:]
			}
		}
		c.mu.Unlock()
		return b, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// safeRender 把渲染里的 panic 变成错误：一张图出问题不能带走请求（§12.2）。
func safeRender(render func() ([]byte, error)) (b []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("渲染图片 panic", "panic", r)
			err = fmt.Errorf("渲染 panic: %v", r)
		}
	}()
	return render()
}

func (c *imageCache) renderCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renders
}
