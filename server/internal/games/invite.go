package games

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/friends"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// ReminderInvited 是「有人邀请你打掼蛋」的推送（notify/message.go 里已有文案）。
const ReminderInvited = "invited"

// InviteResult 是一次邀请的结果：Invited 是这次新邀请的人（会收到推送），Skipped 是没必要邀请的人：
// 已经在这一局里（座位或候补）、之前已经邀请过、或者被局头移出过这一局。
type InviteResult struct {
	Invited []string
	Skipped []string
}

// Invite 局头邀请牌友来这一局（#115，08「邀请谁」）。
//
//   - 只有局头能邀请，局要还开着；
//   - 只能邀请自己的牌友（friends.RequireFriend，和 GET /api/friends 同一套规则）。只要有一个不是牌友，
//     整次邀请就失败（friends.ErrNotFriend → 404），不会部分成功，也就不能拿来探测用户 id；
//   - 去重；一次最多 MaxInvitesPerCall 人，一个人 24 小时内最多发出 MaxInvitesPerDay 条邀请；
//   - 新邀请的人记进 invites 表、各发一条 invited 推送；同一个人对同一局只推一次。
func (s *Service) Invite(ctx context.Context, code, hostID string, userIDs []string) (InviteResult, error) {
	var targets []string
	for _, id := range userIDs {
		if id != "" && !slices.Contains(targets, id) {
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 || len(targets) > MaxInvitesPerCall {
		return InviteResult{}, fmt.Errorf("%w：一次邀请 1–%d 个人", ErrInvalidInput, MaxInvitesPerCall)
	}

	now := s.clk.Now()
	var res InviteResult
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		res = InviteResult{}
		game, err := loadGameByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		if game.HostID != hostID {
			return ErrNotHost
		}
		if !game.Open() {
			return ErrClosed
		}
		for _, target := range targets {
			if err := friends.RequireFriend(ctx, tx, hostID, target); err != nil {
				return err
			}
		}

		var fresh []string
		for _, target := range targets {
			var skip bool
			if err := tx.QueryRowContext(ctx, `
				SELECT EXISTS (SELECT 1 FROM seats     WHERE game_id = ?1 AND user_id = ?2)
				    OR EXISTS (SELECT 1 FROM waitlist  WHERE game_id = ?1 AND user_id = ?2)
				    OR EXISTS (SELECT 1 FROM game_bans WHERE game_id = ?1 AND user_id = ?2)
				    OR EXISTS (SELECT 1 FROM invites   WHERE game_id = ?1 AND user_id = ?2)`,
				game.ID, target).Scan(&skip); err != nil {
				return err
			}
			if skip {
				res.Skipped = append(res.Skipped, target)
			} else {
				fresh = append(fresh, target)
			}
		}

		var sent int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM invites WHERE invited_by = ? AND created_at > ?`,
			hostID, now.Add(-24*time.Hour).Unix()).Scan(&sent); err != nil {
			return err
		}
		if sent+len(fresh) > MaxInvitesPerDay {
			return fmt.Errorf("%w：24 小时内最多邀请 %d 次", ErrQuota, MaxInvitesPerDay)
		}

		version, err := currentVersion(ctx, tx, game.ID)
		if err != nil {
			return err
		}
		for _, target := range fresh {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO invites (game_id, user_id, invited_by, created_at) VALUES (?, ?, ?, ?)`,
				game.ID, target, hostID, now.Unix()); err != nil {
				return err
			}
			if err := addReminder(ctx, tx, game.ID, target, ReminderInvited, now, version); err != nil {
				return err
			}
			res.Invited = append(res.Invited, target)
		}
		return nil
	})
	return res, err
}
