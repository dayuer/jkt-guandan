package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/backup"
	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/store"
)

var t0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Seed(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	return st
}

func keypair(t *testing.T) (identityFile, recipient string) {
	t.Helper()
	id, rec, err := backup.Keygen()
	if err != nil {
		t.Fatal(err)
	}
	identityFile = filepath.Join(t.TempDir(), "key.txt")
	if err := os.WriteFile(identityFile, []byte(id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return identityFile, rec
}

func fileTarget(t *testing.T) (backup.Target, string) {
	t.Helper()
	dir := t.TempDir()
	tg, err := backup.NewTarget(backup.TargetConfig{URL: "file://" + dir})
	if err != nil {
		t.Fatal(err)
	}
	return tg, dir
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func TestSnapshotNameRoundTrip(t *testing.T) {
	name := backup.SnapshotName(t0)
	if name != "guandan-20261013T120000Z.db" {
		t.Fatalf("文件名 %s", name)
	}
	for _, n := range []string{name, name + ".age"} {
		if got, ok := backup.ParseSnapshotTime(n); !ok || !got.Equal(t0) {
			t.Fatalf("解析 %s 得到 %v %v", n, got, ok)
		}
	}
	if _, ok := backup.ParseSnapshotTime("guandan.db"); ok {
		t.Fatal("数据库本身不是快照")
	}
}

// §13.3：保留最近 48 个，再加 7 个每日快照。
func TestKeepRetention(t *testing.T) {
	var snaps []time.Time
	for i := 0; i < 24*12; i++ { // 12 天，每小时一个
		snaps = append(snaps, t0.Add(-time.Duration(i)*time.Hour))
	}
	keep := backup.Keep(snaps)
	if len(keep) != 48+7 {
		t.Fatalf("应保留 55 个，实际 %d", len(keep))
	}
	for i := 0; i < 48; i++ {
		if !keep[snaps[i]] {
			t.Fatalf("最近的第 %d 个小时快照被删了", i)
		}
	}
	days := map[string]bool{}
	for ts := range keep {
		if t0.Sub(ts) >= 48*time.Hour {
			days[ts.Format("2006-01-02")] = true
		}
	}
	if len(days) != 7 {
		t.Fatalf("每日快照应覆盖 7 天，实际 %v", days)
	}
	// 每日快照保留的是那一天最新的一份。
	if !keep[time.Date(2026, 10, 10, 23, 0, 0, 0, time.UTC)] {
		t.Fatal("10-10 应保留当天最后一份（23:00）")
	}
}

func TestRunOnceSnapshotsEncryptsAndUploads(t *testing.T) {
	st := openStore(t)
	identity, recipient := keypair(t)
	target, remoteDir := fileTarget(t)
	localDir := t.TempDir()
	clk := clock.NewFake(t0)

	r, err := backup.New(backup.Options{Store: st, Dir: localDir, Target: target, Recipient: recipient, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Check(context.Background()); err == nil {
		t.Fatal("还没有快照时 Check 应失败")
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("备份: %v", err)
	}
	if err := r.Check(context.Background()); err != nil {
		t.Fatalf("刚备份完 Check 应通过: %v", err)
	}
	if got := names(t, localDir); len(got) != 1 || got[0] != "guandan-20261013T120000Z.db" {
		t.Fatalf("本地快照 %v", got)
	}
	remote := names(t, remoteDir)
	if len(remote) != 1 || remote[0] != "guandan-20261013T120000Z.db.age" {
		t.Fatalf("异地对象 %v", remote)
	}

	// 上传的是密文：不含明文里的称呼，能用私钥解开并通过校验。
	enc, err := os.ReadFile(filepath.Join(remoteDir, remote[0]))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("Pacific Place")) || bytes.Contains(enc, []byte("SQLite format")) {
		t.Fatal("异地对象应当是加密的")
	}
	plain := filepath.Join(t.TempDir(), "plain.db")
	f, _ := os.Create(plain)
	if err := backup.Decrypt(f, bytes.NewReader(enc), identity); err != nil {
		t.Fatalf("解密: %v", err)
	}
	f.Close()
	if _, err := store.VerifySnapshot(context.Background(), plain); err != nil {
		t.Fatalf("解密后的快照校验失败: %v", err)
	}

	// 两个半小时之后，没有新备份 → Check 报过期。
	clk.Advance(150 * time.Minute)
	if err := r.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("备份过期时 Check 应失败，实际 %v", err)
	}
}

func TestRunOncePrunesRemoteAfter30Days(t *testing.T) {
	st := openStore(t)
	_, recipient := keypair(t)
	target, remoteDir := fileTarget(t)
	for _, ts := range []time.Time{t0.Add(-31 * 24 * time.Hour), t0.Add(-29 * 24 * time.Hour)} {
		if err := os.WriteFile(filepath.Join(remoteDir, backup.SnapshotName(ts)+".age"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(remoteDir, "README"), []byte("不是快照，不动"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := backup.New(backup.Options{Store: st, Dir: t.TempDir(), Target: target, Recipient: recipient, Clock: clock.NewFake(t0)})
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := names(t, remoteDir)
	want := []string{"README", "guandan-20260914T120000Z.db.age", "guandan-20261013T120000Z.db.age"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("远端剩下 %v，期望 %v", got, want)
	}
}

type flakyTarget struct {
	backup.Target
	mu   sync.Mutex
	fail bool
}

func (f *flakyTarget) Put(ctx context.Context, name, local string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("网络不通")
	}
	return f.Target.Put(ctx, name, local)
}

// §12.5 降级表：上传失败时本地快照照常保留；连续 3 次失败 /readyz 才告警，成功一次就恢复。
func TestUploadFailuresDegradeReadinessAfterThree(t *testing.T) {
	st := openStore(t)
	_, recipient := keypair(t)
	inner, _ := fileTarget(t)
	target := &flakyTarget{Target: inner, fail: true}
	clk := clock.NewFake(t0)
	localDir := t.TempDir()
	r, _ := backup.New(backup.Options{Store: st, Dir: localDir, Target: target, Recipient: recipient, Clock: clk})

	for i := 1; i <= 3; i++ {
		if err := r.RunOnce(context.Background()); err != nil {
			t.Fatalf("上传失败不应让 RunOnce 失败: %v", err)
		}
		err := r.Check(context.Background())
		if i < 3 && err != nil {
			t.Fatalf("第 %d 次失败还不该告警: %v", i, err)
		}
		if i == 3 && (err == nil || !strings.Contains(err.Error(), "连续失败 3 次")) {
			t.Fatalf("连续 3 次失败应告警，实际 %v", err)
		}
		clk.Advance(time.Hour)
	}
	if n := len(names(t, localDir)); n != 3 {
		t.Fatalf("本地快照应照常保留 3 份，实际 %d", n)
	}
	target.mu.Lock()
	target.fail = false
	target.mu.Unlock()
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(context.Background()); err != nil {
		t.Fatalf("上传恢复后 Check 应通过: %v", err)
	}
	if s := r.Status(); s.UploadFailures != 0 || !s.LastUpload.Equal(clk.Now()) {
		t.Fatalf("状态 %+v", s)
	}
}

func TestRunOnceWithoutRemoteOnlySnapshots(t *testing.T) {
	st := openStore(t)
	dir := t.TempDir()
	r, err := backup.New(backup.Options{Store: st, Dir: dir, Clock: clock.NewFake(t0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(names(t, dir)); n != 1 {
		t.Fatalf("应有 1 份本地快照，实际 %d", n)
	}
}

// Task 启动时如果最近一份快照还不到一小时，就等满一小时；之后每小时一次。
func TestTaskSchedulesHourly(t *testing.T) {
	st := openStore(t)
	dir := t.TempDir()
	clk := clock.NewFake(t0)
	r, _ := backup.New(backup.Options{Store: st, Dir: dir, Clock: clk})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// 20 分钟前刚做过一份（比如重启前）。
	if err := st.Snapshot(context.Background(), filepath.Join(dir, backup.SnapshotName(t0.Add(-20*time.Minute)))); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	beats := make(chan struct{}, 10)
	done := make(chan error, 1)
	go func() { done <- r.Task(ctx, func() { beats <- struct{}{} }) }()

	waitBeat := func(what string) {
		t.Helper()
		select {
		case <-beats:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s：没有等到备份", what)
		}
	}
	// 让 Task 先挂上 After，再推进时间。
	time.Sleep(50 * time.Millisecond)
	clk.Advance(30 * time.Minute)
	select {
	case <-beats:
		t.Fatal("距上次快照不到一小时，不该备份")
	case <-time.After(100 * time.Millisecond):
	}
	clk.Advance(10 * time.Minute) // 距上次 60 分钟
	waitBeat("满一小时")
	time.Sleep(50 * time.Millisecond)
	clk.Advance(time.Hour)
	waitBeat("又过一小时")

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := len(names(t, dir)); n != 3 {
		t.Fatalf("应有 3 份快照，实际 %d：%v", n, names(t, dir))
	}
}

// ---- 恢复 ----

func makeSnapshot(t *testing.T) (plain, encrypted, identity string) {
	t.Helper()
	st := openStore(t)
	dir := t.TempDir()
	plain = filepath.Join(dir, backup.SnapshotName(t0))
	if err := st.Snapshot(context.Background(), plain); err != nil {
		t.Fatal(err)
	}
	identity, recipient := keypair(t)
	target, remoteDir := fileTarget(t)
	r, _ := backup.New(backup.Options{Store: st, Dir: t.TempDir(), Target: target, Recipient: recipient, Clock: clock.NewFake(t0)})
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	return plain, filepath.Join(remoteDir, backup.SnapshotName(t0)+".age"), identity
}

func gameCount(t *testing.T, dataDir string) int {
	t.Helper()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.Read().QueryRow(`SELECT count(*) FROM games`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRestoreFromLocalPlainAndEncrypted(t *testing.T) {
	plain, encrypted, identity := makeSnapshot(t)
	for _, src := range []backup.Source{
		{Path: plain},
		{Path: encrypted, IdentityFile: identity},
	} {
		dataDir := t.TempDir() // 全新机器：数据目录是空的
		res, err := backup.Restore(context.Background(), dataDir, src, t0)
		if err != nil {
			t.Fatalf("从 %s 恢复: %v", src.Path, err)
		}
		if len(res.MovedAside) != 0 || len(res.Migrations) != 2 {
			t.Fatalf("结果 %+v", res)
		}
		if n := gameCount(t, dataDir); n != 5 {
			t.Fatalf("恢复后应有 5 局，实际 %d", n)
		}
	}
	if _, err := backup.Restore(context.Background(), t.TempDir(), backup.Source{Path: encrypted}, t0); err == nil {
		t.Fatal("加密快照没给私钥应报错")
	}
}

func TestRestoreFromRemoteLatestMovesOldDBAside(t *testing.T) {
	st := openStore(t)
	identity, recipient := keypair(t)
	target, _ := fileTarget(t)
	clk := clock.NewFake(t0)
	r, _ := backup.New(backup.Options{Store: st, Dir: t.TempDir(), Target: target, Recipient: recipient, Clock: clk})
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 第二份备份前多开一局，用来区分「最新」那一份。
	if err := st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, created_at)
			VALUES ('g-late', 'LATE01', 'seed-laowang', 0, 'SCBD', 'v', 'classic', 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, backup.DBFile), []byte("坏掉的旧库"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := backup.Restore(context.Background(), dataDir,
		backup.Source{Object: "latest", Target: target, IdentityFile: identity}, t0.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("从异地恢复: %v", err)
	}
	if !strings.HasSuffix(res.From, "guandan-20261013T130000Z.db.age") {
		t.Fatalf("应取最新的一份，实际 %s", res.From)
	}
	if len(res.MovedAside) != 1 || !strings.Contains(res.MovedAside[0], ".before-restore-") {
		t.Fatalf("旧库应被挪开：%v", res.MovedAside)
	}
	if old, _ := os.ReadFile(res.MovedAside[0]); string(old) != "坏掉的旧库" {
		t.Fatal("挪开的旧库内容不对")
	}
	if n := gameCount(t, dataDir); n != 6 {
		t.Fatalf("恢复后应有 6 局，实际 %d", n)
	}
	for _, n := range names(t, dataDir) {
		if strings.HasPrefix(n, ".") {
			t.Errorf("临时文件没清理：%s", n)
		}
	}
}

// 校验不通过时一个字节都不动。
func TestRestoreRejectsBadSnapshotWithoutTouchingDB(t *testing.T) {
	dataDir := t.TempDir()
	orig := filepath.Join(dataDir, backup.DBFile)
	if err := os.WriteFile(orig, []byte("原来的库"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.db")
	if err := os.WriteFile(bad, []byte("这不是 SQLite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Restore(context.Background(), dataDir, backup.Source{Path: bad}, t0); err == nil {
		t.Fatal("坏快照应恢复失败")
	}
	if got, _ := os.ReadFile(orig); string(got) != "原来的库" {
		t.Fatal("恢复失败时原库被改了")
	}
	if got := names(t, dataDir); len(got) != 1 {
		t.Fatalf("数据目录里多出了文件：%v", got)
	}
}

// ---- S3 ----

// fakeS3 是一个最小的 S3：按 path-style 存对象，支持 ListObjectsV2；
// 检查每个请求都带了签名，且 x-amz-content-sha256 与请求体一致。
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	t       *testing.T
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKID/") || !strings.Contains(auth, "/ap-southeast-3/s3/aws4_request") {
		f.t.Errorf("%s %s 签名头不对：%s", r.Method, r.URL, auth)
	}
	body, _ := io.ReadAll(r.Body)
	sum := sha256.Sum256(body)
	if got := r.Header.Get("X-Amz-Content-Sha256"); got != hex.EncodeToString(sum[:]) {
		f.t.Errorf("x-amz-content-sha256 %s 与请求体不符", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
		prefix := r.URL.Query().Get("prefix")
		var keys []string
		for k := range f.objects {
			if strings.HasPrefix(k, strings.TrimPrefix(r.URL.Path, "/")+"/"+prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><IsTruncated>false</IsTruncated>`)
		for _, k := range keys {
			fmt.Fprintf(&b, `<Contents><Key>%s</Key><Size>%d</Size><LastModified>2026-10-13T12:00:00.000Z</LastModified></Contents>`,
				strings.SplitN(k, "/", 2)[1], len(f.objects[k]))
		}
		b.WriteString(`</ListBucketResult>`)
		w.Write([]byte(b.String()))
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/")
	switch r.Method {
	case http.MethodPut:
		f.objects[key] = body
	case http.MethodGet:
		obj, ok := f.objects[key]
		if !ok {
			http.Error(w, "<Error><Code>NoSuchKey</Code></Error>", http.StatusNotFound)
			return
		}
		w.Write(obj)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestS3TargetRoundTrip(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{}, t: t}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	target, err := backup.NewTarget(backup.TargetConfig{
		URL: srv.URL + "/guandan-backup/prod", Region: "ap-southeast-3", AccessKey: "AKID", SecretKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	local := filepath.Join(t.TempDir(), "x.age")
	os.WriteFile(local, []byte("密文"), 0o600)
	if err := target.Put(ctx, "guandan-20261013T120000Z.db.age", local); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := fake.objects["guandan-backup/prod/guandan-20261013T120000Z.db.age"]; !ok {
		t.Fatalf("对象没按 path-style 落到 bucket/前缀 下：%v", fake.objects)
	}
	objs, err := target.List(ctx)
	if err != nil || len(objs) != 1 || objs[0].Name != "guandan-20261013T120000Z.db.age" || objs[0].Size != int64(len("密文")) {
		t.Fatalf("List: %+v %v", objs, err)
	}
	var buf bytes.Buffer
	if err := target.Get(ctx, objs[0].Name, &buf); err != nil || buf.String() != "密文" {
		t.Fatalf("Get: %q %v", buf.String(), err)
	}
	if err := target.Get(ctx, "missing", &buf); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("不存在的对象应报 404，实际 %v", err)
	}
	if err := target.Delete(ctx, objs[0].Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if objs, _ := target.List(ctx); len(objs) != 0 {
		t.Fatalf("删除后还有 %v", objs)
	}
}

// 同一秒里连做两次（启动时自动一份，紧接着手动 guandan backup）也不能失败。
func TestRunOnceTwiceInSameSecond(t *testing.T) {
	st := openStore(t)
	dir := t.TempDir()
	r, _ := backup.New(backup.Options{Store: st, Dir: dir, Clock: clock.NewFake(t0)})
	for i := 0; i < 2; i++ {
		if err := r.RunOnce(context.Background()); err != nil {
			t.Fatalf("第 %d 次: %v", i+1, err)
		}
	}
	if got := names(t, dir); strings.Join(got, ",") != "guandan-20261013T120000Z.db,guandan-20261013T120001Z.db" {
		t.Fatalf("快照 %v", got)
	}
}
