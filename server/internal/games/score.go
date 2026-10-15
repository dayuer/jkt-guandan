package games

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/scoring"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// 牌桌计分（#165，design/guandan-minigame/README.md §7，docs/server-design.md §5.6）。
//
// 库里只有事实：score_matches 一行开局约定，score_rounds 每局一行（头游座号、搭档名次、
// 谁记的、谁改的）。级数、打谁的级、A 没过几次、第几场、是否结束，每次读的时候由
// scoring.Compute 从第 1 局重算，所以改中间一局不用改后面任何一行。

// 实时事件（§5.5、§5.6）。推到 game:{code}，和座位事件同一个主题。
const (
	EventScoreStarted = "score.started"
	EventScoreRound   = "score.round"
	EventScoreEdited  = "score.edited"
)

// ScoreOpensBefore：开局前多久可以开始记分（S1「离开局 ≤ 30 分钟」出现入口条）。
const ScoreOpensBefore = 30 * time.Minute

// 计分的业务错误。
var (
	ErrNotSeated        = errors.New("只有座位上的四个人能记分")
	ErrNotFull          = errors.New("人还没齐，坐满 4 人才能记分")
	ErrScoreTooEarly    = errors.New("开局前 30 分钟起才能开始记分")
	ErrScoreNotStarted  = errors.New("这一局还没开始记分")
	ErrRoundNotFound    = errors.New("没有这一局的记录")
	ErrRoundConflict    = errors.New("这一局已经有人记了")
	ErrRoundChanged     = errors.New("这一局已经被别人改过了")
	ErrIdemKeyReused    = errors.New("同一个 Idempotency-Key 用在了别的请求上")
	errScoreUnreachable = errors.New("计分数据不一致") // 库里的事实过不了规则校验：约束挡住了，不该出现
)

// UserRef 是记录里的人：只有 id 和称呼（§10.4 的白名单）。
type UserRef struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
}

// ScoreRound 是一局：事实、规则推出来的结果、谁记的谁改的。
type ScoreRound struct {
	scoring.RoundResult
	RecordedBy UserRef
	RecordedAt time.Time
	EditedBy   *UserRef // 没改过为 nil
	EditedAt   time.Time
}

// Score 是一局牌的计分。Started 为假时其余字段都是零值。
type Score struct {
	Started   bool
	Settings  scoring.Settings
	StartedBy UserRef
	StartedAt time.Time
	// Revision：开始、记一局、改一局各 +1。实时事件带着它，客户端丢弃不比本地新的（§5.6）。
	Revision int64
	Rounds   []ScoreRound
	State    scoring.State
	// CanRecord：当前身份在座位上、牌局满座，可以开始记分 / 记一局 / 改一局（S14 旁观时为假）。
	CanRecord bool
	// CanStart：CanRecord 之外还没开始记分，而且已经到了开局前 30 分钟——这时点「开始记分」
	// 才会成功（不会 409 too_early）。S1 的入口条按它显示，客户端不用自己算时间。
	CanStart bool
}

// round 返回第 no 局；没有这一局返回 nil。
func (s *Score) round(no int) *ScoreRound {
	if no < 1 || no > len(s.Rounds) {
		return nil
	}
	return &s.Rounds[no-1]
}

// RoundConflict 是「两人同时记同一局」（S12）：带上已经记下的那一局和最新的计分，
// 前端结果相同就静默合并，不同就让用户选「用他的」还是「改成我的」。
type RoundConflict struct {
	Existing ScoreRound
	Latest   Score
}

func (e *RoundConflict) Error() string { return ErrRoundConflict.Error() }
func (e *RoundConflict) Unwrap() error { return ErrRoundConflict }

// RoundChanged 是改一局时带的「原来的结果」和库里的不一样：别人先改了。带上最新的计分。
type RoundChanged struct{ Latest Score }

func (e *RoundChanged) Error() string { return ErrRoundChanged.Error() }
func (e *RoundChanged) Unwrap() error { return ErrRoundChanged }

// ---------------------------------------------------------------------------
// 读
// ---------------------------------------------------------------------------

// Score 读一局牌的计分。能看牌局详情的人都能看（§7.6），访客 viewerID 为空。
func (s *Service) Score(ctx context.Context, code, viewerID string) (*Score, error) {
	// 牌局、开局约定、各局在同一个读事务里读，看到的是同一个快照。
	tx, err := s.st.Read().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	game, err := scanGame(tx.QueryRowContext(ctx, gameColumns+` WHERE g.short_code = ?`, code))
	if err != nil {
		return nil, err
	}
	return loadScore(ctx, tx, game, viewerID, s.clk.Now())
}

// loadScore 读出事实，交给 scoring 重算。now 只用来算 CanStart。
func loadScore(ctx context.Context, q queryer, game Game, viewerID string, now time.Time) (*Score, error) {
	sc := &Score{}
	if viewerID != "" && game.Status == StatusFull {
		seated, err := isSeated(ctx, q, game.ID, viewerID)
		if err != nil {
			return nil, err
		}
		sc.CanRecord = seated
	}

	var startedAt int64
	err := q.QueryRowContext(ctx, `
		SELECT m.kind, m.a_fail_limit, m.started_by, COALESCE(u.nickname, ''), m.started_at, m.revision
		  FROM score_matches m LEFT JOIN users u ON u.id = m.started_by
		 WHERE m.game_id = ?`, game.ID).
		Scan(&sc.Settings.Kind, &sc.Settings.AFailLimit, &sc.StartedBy.ID, &sc.StartedBy.Nickname,
			&startedAt, &sc.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		// 和 StartScore 的判断一致：full 已经排除了 done / cancelled。
		sc.CanStart = sc.CanRecord && !now.Before(scoreOpensAt(game))
		return sc, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读开局约定: %w", err)
	}
	sc.Started = true
	sc.StartedAt = time.Unix(startedAt, 0)

	rows, err := q.QueryContext(ctx, `
		SELECT r.round_no, r.head_seat, r.partner_place,
		       r.recorded_by, COALESCE(ru.nickname, ''), r.created_at,
		       COALESCE(r.edited_by, ''), COALESCE(eu.nickname, ''), COALESCE(r.edited_at, 0)
		  FROM score_rounds r
		  LEFT JOIN users ru ON ru.id = r.recorded_by
		  LEFT JOIN users eu ON eu.id = r.edited_by
		 WHERE r.game_id = ?
		 ORDER BY r.round_no`, game.ID)
	if err != nil {
		return nil, fmt.Errorf("读各局: %w", err)
	}
	defer rows.Close()

	var facts []scoring.Round
	for rows.Next() {
		var (
			no                   int
			fact                 scoring.Round
			sr                   ScoreRound
			recordedAt           int64
			editedBy, editedNick string
			editedAt             int64
		)
		if err := rows.Scan(&no, &fact.HeadSeat, &fact.PartnerPlace,
			&sr.RecordedBy.ID, &sr.RecordedBy.Nickname, &recordedAt,
			&editedBy, &editedNick, &editedAt); err != nil {
			return nil, err
		}
		// 不变量 6 保证局号从 1 连续；这里再确认一次，免得重算对错了局。
		if no != len(facts)+1 {
			return nil, fmt.Errorf("%w：第 %d 局之前缺局", errScoreUnreachable, no)
		}
		sr.RecordedAt = time.Unix(recordedAt, 0)
		if editedBy != "" {
			sr.EditedBy = &UserRef{ID: editedBy, Nickname: editedNick}
			sr.EditedAt = time.Unix(editedAt, 0)
		}
		facts = append(facts, fact)
		sc.Rounds = append(sc.Rounds, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	res, err := scoring.Compute(sc.Settings, facts)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", errScoreUnreachable, err)
	}
	for i := range res.Rounds {
		sc.Rounds[i].RoundResult = res.Rounds[i]
	}
	sc.State = res.State
	return sc, nil
}

// ---------------------------------------------------------------------------
// 写
// ---------------------------------------------------------------------------

// scoreOpensAt 是这一局最早可以开始记分的时刻。
func scoreOpensAt(game Game) time.Time { return game.StartsAt.Add(-ScoreOpensBefore) }

// scoreWriter 是三个写操作共同的前置检查：牌局在、没结束、我在座位上、满座。
func (s *Service) scoreWriter(ctx context.Context, tx *store.Tx, code, userID string) (Game, error) {
	game, err := loadGameByCode(ctx, tx, code)
	if err != nil {
		return Game{}, err
	}
	if !game.Open() {
		return Game{}, ErrClosed
	}
	seated, err := isSeated(ctx, tx, game.ID, userID)
	if err != nil {
		return Game{}, err
	}
	if !seated {
		return Game{}, ErrNotSeated
	}
	// 有人中途退出、局回到 open：等补齐了再记，免得记到空座位上。
	if game.Status != StatusFull {
		return Game{}, ErrNotFull
	}
	return game, nil
}

// StartScore 开始记分（S2）。座位上任何一人都能点；已经开始了就原样返回（两人同时点「开始」
// 也只开一次，后到的那个人的约定不生效）。aFailLimit 只能是 3 或 0（不回 2）。
// 玩法按牌局的 kind 定：no-level 只记胜局，其余（classic、teaching）记级数。
func (s *Service) StartScore(ctx context.Context, code, userID string, aFailLimit int, idemKey string) (*Score, error) {
	if !s.scoreLimiter.Allow(userID) {
		return nil, ErrRateLimited
	}
	now := s.clk.Now()
	const route = "POST /api/games/{code}/score"
	fp := idemFingerprint("a_fail_limit", aFailLimit)

	var sc *Score
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := s.scoreWriter(ctx, tx, code, userID)
		if err != nil {
			return err
		}
		if replay, err := idemReplay(ctx, tx, userID, idemKey, route+" "+code, fp); err != nil || replay {
			if err == nil {
				sc, err = loadScore(ctx, tx, game, userID, now)
			}
			return err
		}

		started, err := exists(ctx, tx, `SELECT count(*) FROM score_matches WHERE game_id = ?`, game.ID)
		if err != nil {
			return err
		}
		if !started {
			if now.Before(scoreOpensAt(game)) {
				return ErrScoreTooEarly
			}
			settings := scoring.Settings{Kind: scoring.KindClassic, AFailLimit: aFailLimit}
			if game.Kind == scoring.KindNoLevel {
				settings.Kind = scoring.KindNoLevel
			}
			if err := settings.Validate(); err != nil {
				return fmt.Errorf("%w：%v", ErrInvalidInput, err)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO score_matches (game_id, kind, a_fail_limit, started_by, started_at, revision)
				VALUES (?, ?, ?, ?, ?, 1)`,
				game.ID, settings.Kind, settings.AFailLimit, userID, now.Unix()); err != nil {
				return err
			}
			tx.Touch(game.ID)
			s.publishScoreAfterCommit(tx, game, EventScoreStarted, 0)
		}
		if err := idemSave(ctx, tx, userID, idemKey, route+" "+code, fp, map[string]any{}, now); err != nil {
			return err
		}
		sc, err = loadScore(ctx, tx, game, userID, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return sc, nil
}

// RecordRound 记一局（S4–S7）。expectedNo 是客户端以为的下一局局号：
//   - 正好是下一局：写入；
//   - 这一局已经有人记了（两人同时记，S12）：返回 *RoundConflict，带上已经记下的那一局；
//   - 比下一局还大（客户端跳号）：输入不合法。
//
// 本场已经过 A 结束时，记的这一局就是下一场的第 1 局（S8「再来一场」不需要单独的接口）。
func (s *Service) RecordRound(ctx context.Context, code, userID string, expectedNo int, rd scoring.Round, idemKey string) (*Score, error) {
	if !s.scoreLimiter.Allow(userID) {
		return nil, ErrRateLimited
	}
	if err := rd.Validate(); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrInvalidInput, err)
	}
	if expectedNo < 1 {
		return nil, fmt.Errorf("%w：需要 expected_round_no", ErrInvalidInput)
	}
	now := s.clk.Now()
	const route = "POST /api/games/{code}/score/rounds"
	fp := idemFingerprint("expected_round_no", expectedNo, "head_seat", rd.HeadSeat, "partner_place", rd.PartnerPlace)

	var sc *Score
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := s.scoreWriter(ctx, tx, code, userID)
		if err != nil {
			return err
		}
		// 幂等键：超时后重试同一次提交，返回现在的计分，不报「这一局已经有人记了」。
		if replay, err := idemReplay(ctx, tx, userID, idemKey, route+" "+code, fp); err != nil || replay {
			if err == nil {
				sc, err = loadScore(ctx, tx, game, userID, now)
			}
			return err
		}

		current, err := loadScore(ctx, tx, game, userID, now)
		if err != nil {
			return err
		}
		if !current.Started {
			return ErrScoreNotStarted
		}
		next := len(current.Rounds) + 1
		switch {
		case expectedNo < next:
			return &RoundConflict{Existing: *current.round(expectedNo), Latest: *current}
		case expectedNo > next:
			return fmt.Errorf("%w：下一局是第 %d 局", ErrInvalidInput, next)
		}

		// 主键 (game_id, round_no) 是最后一道防线：写事务串行，正常走不到冲突。
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			game.ID, next, rd.HeadSeat, rd.PartnerPlace, userID, now.Unix()); err != nil {
			return err
		}
		if err := bumpScoreRevision(ctx, tx, game.ID); err != nil {
			return err
		}
		tx.Touch(game.ID)
		s.publishScoreAfterCommit(tx, game, EventScoreRound, next)

		if err := idemSave(ctx, tx, userID, idemKey, route+" "+code, fp, map[string]int{"round_no": next}, now); err != nil {
			return err
		}
		sc, err = loadScore(ctx, tx, game, userID, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return sc, nil
}

// EditRound 改一局（S10）：替换这一局的事实，后面各局随之重算。
//
// was 是客户端打开「改一局」时看到的原结果，可以不带。带了而且和库里的不一样，说明别人刚改过，
// 返回 *RoundChanged 和最新的计分，不悄悄覆盖（和 PATCH 的 If-Match 同一个意思，但只比这一局：
// 别人在此期间记了新的一局不算冲突）。改成和现在一样的结果什么也不写、不发事件。
func (s *Service) EditRound(ctx context.Context, code, userID string, no int, rd scoring.Round, was *scoring.Round) (*Score, error) {
	if !s.scoreLimiter.Allow(userID) {
		return nil, ErrRateLimited
	}
	if err := rd.Validate(); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrInvalidInput, err)
	}
	now := s.clk.Now()

	var sc *Score
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := s.scoreWriter(ctx, tx, code, userID)
		if err != nil {
			return err
		}
		current, err := loadScore(ctx, tx, game, userID, now)
		if err != nil {
			return err
		}
		if !current.Started {
			return ErrScoreNotStarted
		}
		old := current.round(no)
		if old == nil {
			return ErrRoundNotFound
		}
		if was != nil && *was != old.Round {
			return &RoundChanged{Latest: *current}
		}
		if old.Round == rd {
			sc = current
			return nil
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE score_rounds SET head_seat = ?, partner_place = ?, edited_by = ?, edited_at = ?
			 WHERE game_id = ? AND round_no = ?`,
			rd.HeadSeat, rd.PartnerPlace, userID, now.Unix(), game.ID, no); err != nil {
			return err
		}
		if err := bumpScoreRevision(ctx, tx, game.ID); err != nil {
			return err
		}
		tx.Touch(game.ID)
		s.publishScoreAfterCommit(tx, game, EventScoreEdited, no)

		sc, err = loadScore(ctx, tx, game, userID, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return sc, nil
}

func bumpScoreRevision(ctx context.Context, tx *store.Tx, gameID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE score_matches SET revision = revision + 1 WHERE game_id = ?`, gameID)
	return err
}

// ---------------------------------------------------------------------------
// 幂等键（§11.4）
// ---------------------------------------------------------------------------

// idemReplay 查幂等键：这个键已经用在同一个请求上，返回 true，调用方直接返回现在的结果。
// 用在了别的请求上（别的路由、别的局，或者同一个接口换了请求体）返回 ErrIdemKeyReused：
// 客户端误把键复用到下一局时，不能当成重放静默返回 200、实际却没记。键为空什么也不做。
// 库里的指纹为空（旧版本写的）只比路由。
func idemReplay(ctx context.Context, tx *store.Tx, userID, key, route, fingerprint string) (bool, error) {
	if key == "" {
		return false, nil
	}
	var saved string
	var savedFP sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT route, fingerprint FROM idempotency_keys WHERE user_id = ? AND key = ?`, userID, key).
		Scan(&saved, &savedFP)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	case saved != route, savedFP.Valid && savedFP.String != fingerprint:
		return false, ErrIdemKeyReused
	}
	return true, nil
}

// idemFingerprint 是请求的指纹：业务参数按给定的顺序拼成 name=value，取 sha256。
// 用解析后的参数而不是原始请求体，JSON 的字段顺序、空白、省略默认值都不影响。
func idemFingerprint(kv ...any) string {
	h := sha256.New()
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(h, "%v=%v\n", kv[i], kv[i+1])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// idemSave 记下这个键和请求指纹。计分的响应是「现在的计分」，重放时现读，所以 response 只存一个摘要。
func idemSave(ctx context.Context, tx *store.Tx, userID, key, route, fingerprint string, summary any, now time.Time) error {
	if key == "" {
		return nil
	}
	body, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO idempotency_keys (key, user_id, route, fingerprint, status, response, created_at)
		VALUES (?, ?, ?, ?, 200, ?, ?)`, key, userID, route, fingerprint, body, now.Unix())
	return err
}

// ---------------------------------------------------------------------------
// 实时事件（§5.5、§5.6）
// ---------------------------------------------------------------------------

// publishScoreAfterCommit 提交后把计分的变化推到 game:{code}。
//
// 事件不带顶层 v：game:* 主题上的 v 是牌局版本号，计分不动牌局版本（不然局头改时间会被记分
// 挤成 409），所以和 presence 一样不带 v，改在 data.revision 上去重。round 是这次记或改的
// 那一局（score.started 没有），state 是记完后的状态；改一局时后面各局都变了，客户端要
// 完整列表就重新拉 GET /score。
func (s *Service) publishScoreAfterCommit(tx *store.Tx, game Game, eventType string, roundNo int) {
	tx.AfterCommit(func(ctx context.Context) {
		sc, err := s.Score(ctx, game.ShortCode, "")
		if err != nil {
			slog.Error("推计分事件前读计分失败", "code", game.ShortCode, "err", err)
			return
		}
		data := map[string]any{
			"revision": sc.Revision,
			"settings": sc.Settings,
			"rounds":   len(sc.Rounds),
			"state":    sc.State,
		}
		if r := sc.round(roundNo); r != nil {
			data["round"] = scoreRoundJSON(*r)
			by := r.RecordedBy
			if eventType == EventScoreEdited && r.EditedBy != nil {
				by = *r.EditedBy
			}
			data["by"] = by
		} else {
			data["by"] = sc.StartedBy
		}
		s.pub.Publish(Event{Topic: GameTopic(game.ShortCode), Type: eventType, Data: data})
	})
}

// ---------------------------------------------------------------------------
// JSON
// ---------------------------------------------------------------------------

func scoreRoundJSON(r ScoreRound) map[string]any {
	// RoundResult 的字段带 json 标签，先编一遍再加上记录信息，免得两处各写一份字段表。
	raw, _ := json.Marshal(r.RoundResult)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["recorded_by"] = r.RecordedBy
	out["recorded_at"] = r.RecordedAt.Unix()
	if r.EditedBy != nil {
		out["edited_by"] = *r.EditedBy
		out["edited_at"] = r.EditedAt.Unix()
	}
	return out
}

func toScoreJSON(sc *Score) map[string]any {
	if !sc.Started {
		return map[string]any{"started": false, "can_record": sc.CanRecord, "can_start": sc.CanStart}
	}
	rounds := make([]map[string]any, 0, len(sc.Rounds))
	for _, r := range sc.Rounds {
		rounds = append(rounds, scoreRoundJSON(r))
	}
	return map[string]any{
		"started":    true,
		"can_record": sc.CanRecord,
		"can_start":  false,
		"settings":   sc.Settings,
		"started_by": sc.StartedBy,
		"started_at": sc.StartedAt.Unix(),
		"revision":   sc.Revision,
		"rounds":     rounds,
		"state":      sc.State,
	}
}

// parseRoundNo 解析路径里的局号。
func parseRoundNo(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w：局号不对", ErrInvalidInput)
	}
	return n, nil
}
