package games_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// ---------------------------------------------------------------------------
// 开局的幂等键比对路由和请求指纹（#175）
// ---------------------------------------------------------------------------

func (f *fixture) createInput() games.CreateInput {
	return games.CreateInput{
		StartsAt: f.clk.Now().Add(2 * time.Hour),
		Area:     "SCBD",
		Venue:    "Pacific Place",
		Kind:     "classic",
	}
}

// 同一个键、同一个请求体：重放，还是第一局。JSON 之外的差别（首尾空白、省略默认 kind）不算不同。
func TestCreateIdemKeyReplay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	host := f.newUser("局头")

	in := f.createInput()
	first, err := f.games.Create(ctx, host, in, "c-1")
	if err != nil {
		t.Fatal(err)
	}
	again := in
	again.Venue = "  Pacific Place "
	again.Kind = ""
	second, err := f.games.Create(ctx, host, again, "c-1")
	if err != nil {
		t.Fatalf("原样重发应是重放：%v", err)
	}
	if second.Game.ShortCode != first.Game.ShortCode {
		t.Fatalf("重放应返回第一局 %s，实际 %s", first.Game.ShortCode, second.Game.ShortCode)
	}
	if n := f.count(`SELECT count(*) FROM games WHERE host_id = ?`, host); n != 1 {
		t.Fatalf("只应开一局，实际 %d", n)
	}
}

// 同一个键、换了开局参数：422，不静默返回第一局，也不开新局。
func TestCreateIdemKeyDifferentBody(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	host := f.newUser("局头")

	in := f.createInput()
	if _, err := f.games.Create(ctx, host, in, "c-1"); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*games.CreateInput){
		"starts_at": func(c *games.CreateInput) { c.StartsAt = c.StartsAt.Add(time.Hour) },
		"area":      func(c *games.CreateInput) { c.Area = "Kuningan" },
		"venue":     func(c *games.CreateInput) { c.Venue = "Plaza Senayan" },
		"kind":      func(c *games.CreateInput) { c.Kind = "no-level" },
		"note":      func(c *games.CreateInput) { c.Note = "新手友好" },
	} {
		changed := in
		change(&changed)
		if _, err := f.games.Create(ctx, host, changed, "c-1"); !errors.Is(err, games.ErrIdemKeyReused) {
			t.Fatalf("换了 %s 应 ErrIdemKeyReused，实际 %v", name, err)
		}
	}
	if n := f.count(`SELECT count(*) FROM games WHERE host_id = ?`, host); n != 1 {
		t.Fatalf("只应开一局，实际 %d", n)
	}
	// 别人用同一个字符串不受影响。
	if _, err := f.games.Create(ctx, f.newUser("别人"), in, "c-1"); err != nil {
		t.Fatalf("别人用同一个键：%v", err)
	}
}

// 记分接口用过的键拿来开局：422，不能回放出一个空的 short_code。
func TestCreateIdemKeyFromOtherRoute(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	code, seats := f.fullGame()
	if _, err := f.games.StartScore(ctx, code, seats[0], 3, "shared"); err != nil {
		t.Fatal(err)
	}
	before := f.count(`SELECT count(*) FROM games WHERE host_id = ?`, seats[0])
	if _, err := f.games.Create(ctx, seats[0], f.createInput(), "shared"); !errors.Is(err, games.ErrIdemKeyReused) {
		t.Fatalf("记分用过的键拿来开局应 ErrIdemKeyReused，实际 %v", err)
	}
	if n := f.count(`SELECT count(*) FROM games WHERE host_id = ?`, seats[0]); n != before {
		t.Fatalf("不应开新局：%d -> %d", before, n)
	}
}

// 旧版本写的行（fingerprint 为空）只比路由：开局路由的照旧重放，别的路由的 422。
func TestCreateIdemKeyLegacyRow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	host := f.newUser("局头")
	code := f.newGame(host)

	insert := func(key, route, response string) {
		t.Helper()
		if err := f.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO idempotency_keys (key, user_id, route, status, response, created_at)
				VALUES (?, ?, ?, 200, ?, ?)`, key, host, route, response, f.clk.Now().Unix())
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert("old-create", "POST /api/games", `{"short_code":"`+code+`"}`)
	insert("old-score", "POST /api/games/{code}/score "+code, `{}`)

	changed := f.createInput()
	changed.Venue = "Plaza Senayan"
	detail, err := f.games.Create(ctx, host, changed, "old-create")
	if err != nil {
		t.Fatalf("旧的开局键应照旧重放：%v", err)
	}
	if detail.Game.ShortCode != code {
		t.Fatalf("重放应返回 %s，实际 %s", code, detail.Game.ShortCode)
	}
	if _, err := f.games.Create(ctx, host, f.createInput(), "old-score"); !errors.Is(err, games.ErrIdemKeyReused) {
		t.Fatalf("旧的记分键拿来开局应 ErrIdemKeyReused，实际 %v", err)
	}
	if n := f.count(`SELECT count(*) FROM games WHERE host_id = ?`, host); n != 1 {
		t.Fatalf("只应有一局，实际 %d", n)
	}
}
