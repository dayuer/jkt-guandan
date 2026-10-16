package games_test

import (
	"context"
	"testing"
)

// #210：改称呼之后，牌局详情里的座位要显示新称呼。
//
// 座位、候补、牌友列表、每局记录的「谁记的」都只存 user id，读的时候 JOIN users ——
// 这条用例就是钉住这一点：如果有谁把称呼快照进了自己的表，这里会红。
func TestRenameUpdatesNicknameEverywhere(t *testing.T) {
	f := newFixture(t)
	hostID := f.newUser("局头")
	playerID := f.newUser("牌友")

	code := f.newGame(hostID)
	if _, err := f.games.Join(context.Background(), code, playerID); err != nil {
		t.Fatalf("报名: %v", err)
	}

	// 改称呼。
	if _, err := f.ident.ChangeNickname(context.Background(), playerID, "新牌友"); err != nil {
		t.Fatalf("改称呼: %v", err)
	}

	detail, err := f.games.Get(context.Background(), code, hostID)
	if err != nil {
		t.Fatalf("读牌局: %v", err)
	}
	var got string
	for _, s := range detail.Seats {
		if s.UserID == playerID {
			got = s.Nickname
		}
	}
	if got != "新牌友" {
		t.Fatalf("座位上的称呼应当是「新牌友」，实际 %q", got)
	}

	// 列表页（「我的」页里的牌局列表）也走同一条路。
	list, err := f.games.List(context.Background(), hostID, "", "", 20, 0)
	if err != nil {
		t.Fatalf("列表: %v", err)
	}
	for _, it := range list {
		for _, s := range it.Seats {
			if s.UserID == playerID && s.Nickname != "新牌友" {
				t.Fatalf("列表里的称呼应当是「新牌友」，实际 %q", s.Nickname)
			}
		}
	}

	// 局头的名字也一起变（他不是改自己的那个人，走的是同一张表）。
	if _, err := f.ident.ChangeNickname(context.Background(), hostID, "新局头"); err != nil {
		t.Fatalf("改局头称呼: %v", err)
	}
	detail, err = f.games.Get(context.Background(), code, hostID)
	if err != nil {
		t.Fatalf("读牌局: %v", err)
	}
	if detail.HostName != "新局头" {
		t.Fatalf("局头名字应当是「新局头」，实际 %q", detail.HostName)
	}
}
