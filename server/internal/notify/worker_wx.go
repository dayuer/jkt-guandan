package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// 订阅消息这一路的发送（#137，§5.3「订阅消息通道」）。
//
// 和 Web Push 不同，这一路是**至多送达一次**：订阅消息没有 Topic / tag 这种设备端去重，崩溃后重发，
// 用户就会看到两条，还白白多用一次一次性订阅的次数。所以发送前先开一个短的写事务「预占」：
// 次数 - 1，并把这条提醒标成已发送（sent_at）；发送失败时在写回事务里退回次数、清掉 sent_at。
// 进程在预占和写回之间崩溃，这条提醒就不再发——宁可漏一条，也不发两条。

type wxQuotaKey struct{ userID, templateID string }

type wxResult struct {
	decisions map[int64]decision
	refund    []wxQuotaKey // 预占了但没发出去：退回次数
	exhausted []wxQuotaKey // 微信说用户没订阅（43101）：次数清零
	sent      int
}

type wxJob struct {
	d      due
	key    wxQuotaKey
	msg    WxMessage
	code   int
	err    error
	reason string // 预占阶段就决定放弃时的原因
}

// sendWx 发一批没有 Web Push 订阅、但有订阅消息模板的提醒。
func (w *Worker) sendWx(ctx context.Context, now time.Time, batch []due) (wxResult, error) {
	res := wxResult{decisions: map[int64]decision{}}
	ch := w.svc.wx
	if ch == nil || len(batch) == 0 {
		return res, nil
	}
	provider := "wx_openid:" + ch.AppID
	jobs := make([]*wxJob, 0, len(batch))

	// 预占：找 openid、占次数、标记已发送，一个事务里做完。
	err := w.svc.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		jobs = jobs[:0] // 事务可能整体重试
		for _, d := range batch {
			t, _ := ch.template(d.kind)
			j := &wxJob{d: d, key: wxQuotaKey{d.userID, t.ID}}
			jobs = append(jobs, j)
			var openid string
			err := tx.QueryRowContext(ctx,
				`SELECT subject FROM user_links WHERE provider = ? AND user_id = ?`, provider, d.userID).Scan(&openid)
			if errors.Is(err, sql.ErrNoRows) {
				j.reason = "没有推送订阅"
				continue
			}
			if err != nil {
				return err
			}
			r, err := tx.ExecContext(ctx, `
				UPDATE wx_subscribe_quota SET remaining = remaining - 1, updated_at = ?
				 WHERE user_id = ? AND template_id = ? AND remaining > 0`, now.Unix(), d.userID, t.ID)
			if err != nil {
				return err
			}
			if n, _ := r.RowsAffected(); n == 0 {
				j.reason = "没有推送订阅（订阅消息次数为 0）"
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE outbox SET sent_at = ?, last_error = '订阅消息发送中' WHERE id = ?`, now.Unix(), d.id); err != nil {
				return err
			}
			j.msg = wxPayload(t, d.kind, d.game)
			j.msg.OpenID = openid
		}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("预占订阅消息次数: %w", err)
	}

	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, Concurrency)
	)
	for _, j := range jobs {
		if j.reason != "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(j *wxJob) {
			defer wg.Done()
			defer func() { <-sem }()
			j.code, j.err = ch.Sender.SendSubscribe(ctx, j.msg)
		}(j)
	}
	wg.Wait()

	for _, j := range jobs {
		id := j.d.id
		switch {
		case j.reason != "":
			res.decisions[id] = decision{outcomeDropped, j.reason}
		case j.err == nil && j.code == 0:
			res.decisions[id] = decision{outcomeSent, ""}
			res.sent++
		case j.err == nil && j.code == wxErrNotSubscribed:
			// 微信那边没有可用的订阅了（用户在设置里关掉、或者我们记的次数多了）：以微信为准。
			res.exhausted = append(res.exhausted, j.key)
			res.decisions[id] = decision{outcomeDropped, "订阅消息：用户没有订阅（43101）"}
		default:
			// 没发出去，次数在微信那边也没用掉，退回来。
			res.refund = append(res.refund, j.key)
			logWxError(j.d.kind, j.code, j.err)
			if j.err != nil || wxRetryable(j.code) {
				reason := fmt.Sprintf("订阅消息：errcode %d", j.code)
				if j.err != nil {
					reason = "订阅消息：" + j.err.Error()
				}
				res.decisions[id] = decision{outcomeRetry, reason}
			} else {
				res.decisions[id] = decision{outcomeDropped, fmt.Sprintf("订阅消息：errcode %d", j.code)}
			}
		}
	}
	return res, nil
}

// wxRetryable：系统繁忙、限频、access_token 失效（已作废缓存，下一轮换新的）都值得重试；
// 其他错误（模板参数不对、openid 无效……）重试也一样，直接放弃。
func wxRetryable(code int) bool {
	switch code {
	case wxErrBusy, wxErrRateLimited, wxErrTokenInvalid, wxErrTokenExpired, wxErrTokenBad:
		return true
	}
	return false
}
