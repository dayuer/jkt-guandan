package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultModel 是默认用的 Claude 模型，可以用 GUANDAN_AGENT_MODEL 换。
const DefaultModel = "claude-sonnet-5-5"

// claudeTimeout 是一次调用 Claude 的总时长上限（§10.11：Claude 30 秒）。handler 自己的期限更短，
// 这里只是兜底：http.Client 不能没有超时。
const claudeTimeout = 30 * time.Second

// Claude 通过 Messages API 的流式接口调用 Claude。key 只在服务端（§10.10）。
type Claude struct {
	key     string
	model   string
	baseURL string
	client  *http.Client
}

// NewClaude 建一个 Claude 客户端。model 为空用 DefaultModel。
func NewClaude(key, model string) *Claude {
	if model == "" {
		model = DefaultModel
	}
	return &Claude{
		key: key, model: model, baseURL: "https://api.anthropic.com",
		// 不用 http.DefaultClient：对外的调用都要有自己的超时（§10.11）。
		client: &http.Client{Timeout: claudeTimeout},
	}
}

// Stream 发一次请求，把模型输出的文本片段依次交给 onText。
func (c *Claude) Stream(ctx context.Context, system, user string, onText func(string)) error {
	body, err := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": 600,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": user}},
		"stream":     true,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Api-Key", c.key)
	req.Header.Set("Anthropic-Version", "2023-06-01")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("调用 Claude：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return fmt.Errorf("Claude 返回 %d：%s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return readSSE(resp.Body, onText)
}

// readSSE 读 Messages API 的事件流：只关心 content_block_delta 里的文本、error 和 message_stop。
func readSSE(r io.Reader, onText func(string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue // event: 行、空行、注释
		}
		var ev struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &ev); err != nil {
			return fmt.Errorf("Claude 事件不是 JSON：%w", err)
		}
		switch ev.Type {
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				onText(ev.Delta.Text)
			}
		case "error":
			return fmt.Errorf("Claude 出错：%s %s", ev.Error.Type, ev.Error.Message)
		case "message_stop":
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("读 Claude 的事件流：%w", err)
	}
	return fmt.Errorf("Claude 的事件流没有正常结束")
}
