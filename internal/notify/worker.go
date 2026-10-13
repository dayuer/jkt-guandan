package notify

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/store"
)

// 发送的各项参数（§5.3）。
const (
	Interval     = 15 * time.Second
	BatchSize    = 100
	Concurrency  = 8
	MaxAttempts  = 8
	maxStaleness = 24 * time.Hour // 该发的时间过去一天还没发出去的提醒，已经没有意义
	minTTL       = time.Minute
	maxTTL       = 24 * time.Hour
)

// backoff 是第 n 次失败后（n 从 1 开始）到下一次重试的间隔：30 秒、2 分、10 分、30 分，之后每小时。
func backoff(n int) time.Duration {
	steps := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
	if n <= len(steps) {
		return steps[n-1]
	}
	return time.Hour
}

// Worker 是发送 outbox 的后台任务。同一时刻只有它一个调度者，所以不会两处同时处理同一条提醒（§11.5）。
type Worker struct {
	svc    *Service
	sender Sender
	clk    clock.Clock
}

func NewWorker(svc *Service, sender Sender, clk clock.Clock) *Worker {
	if clk == nil {
		clk = clock.System{}
	}
	return &Worker{svc: svc, sender: sender, clk: clk}
}

// Task 交给 supervise.Group：每 15 秒一轮，每轮 beat 一次。单轮出错只记日志。
func (w *Worker) Task(ctx context.Context, beat func()) error {
	ticker := w.clk.NewTicker(Interval)
	defer ticker.Stop()
	for {
		res, err := w.RunOnce(ctx)
		if err != nil {
			slog.Error("推送发送失败", "err", err)
		} else if res != (Result{}) {
			slog.Info("推送", "sent", res.Sent, "dropped", res.Dropped, "retry", res.Retried, "gone_subscriptions", res.GoneSubscriptions)
		}
		beat()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
		}
	}
}

// Result 是一轮做了什么。
type Result struct {
	Sent, Dropped, Retried, GoneSubscriptions int
}

type due struct {
	id        int64
	userID    string
	kind      string
	attempts  int
	dedupeKey string
	sendAt    time.Time
	status    string
	game      gameInfo
}

type outcome int

const (
	outcomeSent outcome = iota
	outcomeDropped
	outcomeRetry
)

type decision struct {
	outcome outcome
	reason  string
}

// RunOnce 处理一批到期的提醒：读出来 → 在事务外并发发送 → 一个短的写事务统一写回（§5.3、§11.1）。
func (w *Worker) RunOnce(ctx context.Context) (Result, error) {
	now := w.clk.Now()
	batch, err := w.loadDue(ctx, now)
	if err != nil || len(batch) == 0 {
		return Result{}, err
	}

	decisions := make(map[int64]decision, len(batch))
	var toSend []due
	for _, d := range batch {
		if reason := pointless(d, now); reason != "" {
			decisions[d.id] = decision{outcomeDropped, reason}
			continue
		}
		toSend = append(toSend, d)
	}

	users := make([]string, 0, len(toSend))
	for _, d := range toSend {
		users = append(users, d.userID)
	}
	subs, err := w.svc.subscriptionsFor(ctx, users)
	if err != nil {
		return Result{}, fmt.Errorf("读取订阅: %w", err)
	}

	// 发送：每个（提醒，订阅）一个请求，最多 Concurrency 个同时进行，每个 10 秒超时（发送器里设置）。
	type attempt struct {
		reminder int64
		endpoint string
		status   int
		err      error
	}
	var (
		mu       sync.Mutex
		attempts []attempt
		wg       sync.WaitGroup
		sem      = make(chan struct{}, Concurrency)
	)
	for _, d := range toSend {
		list := subs[d.userID]
		if len(list) == 0 {
			decisions[d.id] = decision{outcomeDropped, "没有推送订阅"}
			continue
		}
		payload, opts := w.render(d, now)
		for _, sub := range list {
			wg.Add(1)
			sem <- struct{}{}
			go func(id int64, sub Subscription) {
				defer wg.Done()
				defer func() { <-sem }()
				status, err := w.sender.Send(ctx, sub, payload, opts)
				mu.Lock()
				attempts = append(attempts, attempt{id, sub.Endpoint, status, err})
				mu.Unlock()
			}(d.id, sub)
		}
	}
	wg.Wait()

	// 汇总：任何一台设备送达就算发出；404/410 的订阅删掉，这条提醒也视为成功；
	// 429、5xx、网络错误重试；其他 4xx（请求本身有问题）重试也没用，放弃。
	type agg struct {
		ok, gone, retry, fatal int
		lastErr                string
	}
	per := map[int64]*agg{}
	var gone, failed []string
	for _, a := range attempts {
		g := per[a.reminder]
		if g == nil {
			g = &agg{}
			per[a.reminder] = g
		}
		switch {
		case a.err == nil && a.status >= 200 && a.status < 300:
			g.ok++
		case a.err == nil && (a.status == 404 || a.status == 410):
			g.gone++
			gone = append(gone, a.endpoint)
		case a.err != nil || a.status == 429 || a.status >= 500:
			g.retry++
			failed = append(failed, a.endpoint)
			if a.err != nil {
				g.lastErr = a.err.Error()
			} else {
				g.lastErr = fmt.Sprintf("HTTP %d", a.status)
			}
		default:
			g.fatal++
			failed = append(failed, a.endpoint)
			g.lastErr = fmt.Sprintf("HTTP %d", a.status)
		}
	}
	for id, g := range per {
		switch {
		case g.ok > 0:
			decisions[id] = decision{outcomeSent, ""}
		case g.retry > 0:
			decisions[id] = decision{outcomeRetry, g.lastErr}
		case g.fatal > 0:
			decisions[id] = decision{outcomeDropped, g.lastErr}
		default: // 全部 404/410
			decisions[id] = decision{outcomeSent, ""}
		}
	}

	res := Result{GoneSubscriptions: len(gone)}
	attemptsOf := make(map[int64]int, len(batch))
	for _, d := range batch {
		attemptsOf[d.id] = d.attempts
	}
	err = w.svc.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		unix := now.Unix()
		for id, dec := range decisions {
			var err error
			switch dec.outcome {
			case outcomeSent:
				_, err = tx.ExecContext(ctx, `UPDATE outbox SET sent_at = ?, last_error = NULL WHERE id = ?`, unix, id)
			case outcomeDropped:
				_, err = tx.ExecContext(ctx, `UPDATE outbox SET dropped_at = ?, last_error = ? WHERE id = ?`, unix, dec.reason, id)
			case outcomeRetry:
				n := attemptsOf[id] + 1
				if n >= MaxAttempts {
					_, err = tx.ExecContext(ctx,
						`UPDATE outbox SET attempts = ?, dropped_at = ?, last_error = ? WHERE id = ?`,
						n, unix, "重试 8 次仍失败："+dec.reason, id)
					dec.outcome = outcomeDropped
					decisions[id] = dec
				} else {
					_, err = tx.ExecContext(ctx,
						`UPDATE outbox SET attempts = ?, next_try = ?, last_error = ? WHERE id = ?`,
						n, now.Add(backoff(n)).Unix(), dec.reason, id)
				}
			}
			if err != nil {
				return err
			}
		}
		for _, ep := range gone {
			if _, err := tx.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, ep); err != nil {
				return err
			}
		}
		for _, ep := range failed {
			if _, err := tx.ExecContext(ctx,
				`UPDATE push_subscriptions SET fail_count = fail_count + 1 WHERE endpoint = ?`, ep); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("写回发送结果: %w", err)
	}
	for _, dec := range decisions {
		switch dec.outcome {
		case outcomeSent:
			res.Sent++
		case outcomeDropped:
			res.Dropped++
		case outcomeRetry:
			res.Retried++
		}
	}
	return res, nil
}

func (w *Worker) loadDue(ctx context.Context, now time.Time) ([]due, error) {
	rows, err := w.svc.st.Read().QueryContext(ctx, `
		SELECT o.id, o.user_id, o.kind, o.attempts, o.dedupe_key, o.send_at,
		       g.short_code, g.venue, g.starts_at, g.status,
		       4 - (SELECT count(*) FROM seats s WHERE s.game_id = g.id)
		  FROM outbox o
		  JOIN games g ON g.id = o.game_id
		 WHERE o.sent_at IS NULL AND o.dropped_at IS NULL AND o.next_try <= ?
		 ORDER BY o.next_try, o.id
		 LIMIT ?`, now.Unix(), BatchSize)
	if err != nil {
		return nil, fmt.Errorf("读取到期提醒: %w", err)
	}
	defer rows.Close()
	var out []due
	for rows.Next() {
		var d due
		var sendAt, startsAt int64
		if err := rows.Scan(&d.id, &d.userID, &d.kind, &d.attempts, &d.dedupeKey, &sendAt,
			&d.game.Code, &d.game.Venue, &startsAt, &d.status, &d.game.Missing); err != nil {
			return nil, err
		}
		d.sendAt = time.Unix(sendAt, 0)
		d.game.StartsAt = time.Unix(startsAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// pointless 判断一条提醒是否已经没有意义（§5.3：发送前先检查），返回放弃的原因；空字符串表示照常发送。
func pointless(d due, now time.Time) string {
	switch {
	case d.status == "done":
		return "牌局已结束"
	case d.status == "cancelled" && d.kind != "cancelled":
		return "牌局已取消"
	case !now.Before(d.game.StartsAt):
		return "已过开局时间"
	case now.Sub(d.sendAt) > maxStaleness:
		return "提醒已过期"
	}
	return ""
}

// render 生成推送内容和参数。
func (w *Worker) render(d due, now time.Time) ([]byte, SendOptions) {
	title, body := message(d.kind, d.game)
	payload, _ := json.Marshal(Payload{
		Title: title, Body: body, Tag: d.dedupeKey,
		URL: "/g/" + d.game.Code, Kind: d.kind, Game: d.game.Code,
	})
	ttl := d.game.StartsAt.Sub(now)
	if ttl > maxTTL {
		ttl = maxTTL
	}
	if ttl < minTTL {
		ttl = minTTL
	}
	return payload, SendOptions{Topic: topic(d.dedupeKey), TTL: ttl}
}

// topic 是 Web Push 的 Topic 头：RFC 8030 要求最多 32 个 base64url 字符，
// 所以用 dedupe_key 的哈希。同一条提醒的多次投递会被推送服务合并（§5.3）。
func topic(dedupeKey string) string {
	sum := sha256.Sum256([]byte(dedupeKey))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:32]
}
