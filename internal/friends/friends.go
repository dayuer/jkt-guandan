// Package friends 是 09 牌友（#108，设计稿 F1）：和我一起坐过同一局的人，以及星标。
//
//   - 只列一起坐过局的人，不提供按称呼搜索用户的接口（§10.9 防遍历与隐私）；
//   - 「一起打过 N 局」从 seats 实时算，不单独存；取消了的局不算；
//   - 已注销、被封禁、被合并掉的身份不出现；
//   - 星标（follows 表，0001 就有）只能打给一起坐过局的人，否则 404，免得拿接口探测用户 id。
package friends

import (
	"context"
	"errors"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/store"
)

// MaxFriends 是牌友列表最多返回的人数（§11.5：返回的东西都有上限）。
const MaxFriends = 200

// ErrNotFriend 表示目标不是一起坐过局的人（或者不存在、已注销、被封禁、就是自己）。
var ErrNotFriend = errors.New("friends: 没有一起打过局")

// Friend 是牌友列表里的一个人。
type Friend struct {
	UserID        string
	Nickname      string
	TogetherCount int
	LastTogether  time.Time // 最近一次一起坐的那一局的开局时间
	Starred       bool
}

// Service 读牌友列表、改星标。
type Service struct {
	st  *store.Store
	clk clock.Clock
}

func NewService(st *store.Store, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	return &Service{st: st, clk: clk}
}

// together 是「和 me 一起坐过、没取消的局」按人汇总；排除自己和已注销、被封禁、被合并的人。
const together = `
	SELECT o.user_id, u.nickname, COUNT(*) AS n, MAX(g.starts_at) AS last
	  FROM seats me
	  JOIN seats o  ON o.game_id = me.game_id AND o.user_id <> me.user_id
	  JOIN games g  ON g.id = me.game_id AND g.status <> 'cancelled'
	  JOIN users u  ON u.id = o.user_id
	 WHERE me.user_id = ?
	   AND u.deleted_at IS NULL AND u.banned_at IS NULL AND u.merged_into IS NULL`

// List 返回 me 的牌友：星标的在前，然后按一起打过的局数、最近一起打的时间排序，最多 MaxFriends 个。
func (s *Service) List(ctx context.Context, me string) ([]Friend, error) {
	rows, err := s.st.Read().QueryContext(ctx, `
		SELECT t.user_id, t.nickname, t.n, t.last,
		       EXISTS (SELECT 1 FROM follows f WHERE f.user_id = ? AND f.target_id = t.user_id) AS starred
		  FROM (`+together+` GROUP BY o.user_id, u.nickname) t
		 ORDER BY starred DESC, t.n DESC, t.last DESC, t.user_id
		 LIMIT ?`, me, me, MaxFriends)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Friend{}
	for rows.Next() {
		var f Friend
		var last int64
		if err := rows.Scan(&f.UserID, &f.Nickname, &f.TogetherCount, &last, &f.Starred); err != nil {
			return nil, err
		}
		f.LastTogether = time.Unix(last, 0)
		out = append(out, f)
	}
	return out, rows.Err()
}

// Star 把 target 加星标（幂等）。不是牌友返回 ErrNotFriend。
func (s *Service) Star(ctx context.Context, me, target string) error {
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		if err := RequireFriend(ctx, tx, me, target); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO follows (user_id, target_id, created_at) VALUES (?, ?, ?)`,
			me, target, s.clk.Now().Unix())
		return err
	})
}

// Unstar 取消星标，幂等，不查是不是牌友：对方注销、被封禁之后，旧的星标也要能清掉（#108 复审）。
// 不管目标存不存在都是同样的结果（删 0 行或 1 行），所以也探测不出用户 id。
func (s *Service) Unstar(ctx context.Context, me, target string) error {
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM follows WHERE user_id = ? AND target_id = ?`, me, target)
		return err
	})
}

// RequireFriend 判断 target 是不是 me 的牌友（和 List 同一套规则），不是返回 ErrNotFriend。
// 在写事务里调用：games 的邀请（#115）也用它。
func RequireFriend(ctx context.Context, tx *store.Tx, me, target string) error {
	if target == "" || target == me {
		return ErrNotFriend
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT n FROM (`+together+` AND o.user_id = ?)`, me, target).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFriend
	}
	return nil
}
