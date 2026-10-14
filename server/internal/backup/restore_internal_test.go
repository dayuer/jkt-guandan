package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// #34 复审第 2 条：最后一步（把快照改名到位）失败时，原库必须被挪回原位，
// 否则下次启动会建出一个空库，以空数据对外服务。
func TestRestoreUndoesMoveWhenFinalRenameFails(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

	// 一份合法的快照。
	srcDir := t.TempDir()
	st, err := store.Open(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(t.TempDir(), SnapshotName(now))
	if err := st.Snapshot(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// 数据目录里已有原库和它的 -wal。
	dataDir := t.TempDir()
	db := filepath.Join(dataDir, DBFile)
	for suffix, content := range map[string]string{"": "原来的库", "-wal": "原来的 wal"} {
		if err := os.WriteFile(db+suffix, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 注入故障：只有「临时文件 → guandan.db」这一步失败。
	orig := rename
	t.Cleanup(func() { rename = orig })
	rename = func(from, to string) error {
		if to == db && strings.Contains(filepath.Base(from), ".restore-") {
			return errors.New("注入的故障：磁盘满")
		}
		return orig(from, to)
	}

	_, err = Restore(context.Background(), dataDir, Source{Path: snap}, now)
	if err == nil || !strings.Contains(err.Error(), "原库已挪回原位") {
		t.Fatalf("应报错并说明原库已挪回，实际 %v", err)
	}
	for suffix, want := range map[string]string{"": "原来的库", "-wal": "原来的 wal"} {
		got, err := os.ReadFile(db + suffix)
		if err != nil || string(got) != want {
			t.Fatalf("guandan.db%s 应原样在原位：%q %v", suffix, got, err)
		}
	}
	entries, _ := os.ReadDir(dataDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "before-restore") || strings.HasPrefix(e.Name(), ".restore-") {
			t.Errorf("不该留下 %s", e.Name())
		}
	}
}
