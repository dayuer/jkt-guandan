package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/internal/store"
)

// DBFile 是数据目录里数据库文件的名字（与 store.Open 一致）。
const DBFile = "guandan.db"

// Source 说明从哪里恢复。
type Source struct {
	// Path 是本地快照文件：.db 直接用，.db.age 先解密。
	Path string
	// Object 是异地备份里的对象名；"latest" 表示最新的一份。Path 为空时才看它。
	Object string
	Target Target
	// IdentityFile 是解密用的 age 私钥文件。
	IdentityFile string
}

// RestoreResult 说明这次恢复做了什么。
type RestoreResult struct {
	From       string   // 实际使用的快照
	Migrations []string // 快照里已执行的迁移
	MovedAside []string // 被挪开的原数据库文件
}

// Restore 把快照放回 dataDir/guandan.db（§13.3 guandan restore）。
//
// 调用方必须先持有数据目录锁：拿得到锁，就说明服务已经停了。
// 快照先落到数据目录里的临时文件并通过 VerifySnapshot 校验，原来的数据库文件（含 -wal、-shm）
// 改名成 *.before-restore-<时间>，最后再把临时文件改名到位。任何一步失败，原库都保持不动。
func Restore(ctx context.Context, dataDir string, src Source, now time.Time) (RestoreResult, error) {
	var res RestoreResult
	tmp, err := os.CreateTemp(dataDir, ".restore-*.db")
	if err != nil {
		return res, fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 成功时已经改名走了，这里删不到

	if err := fetch(ctx, dataDir, tmp, src, &res); err != nil {
		tmp.Close()
		return res, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return res, err
	}
	if err := tmp.Close(); err != nil {
		return res, err
	}

	info, err := store.VerifySnapshot(ctx, tmpName)
	if err != nil {
		return res, fmt.Errorf("快照 %s 校验失败，未做任何改动: %w", res.From, err)
	}
	res.Migrations = info.Migrations

	dst := filepath.Join(dataDir, DBFile)
	stamp := now.UTC().Format(timeLayout)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := dst + suffix
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			continue
		}
		aside := dst + ".before-restore-" + stamp + suffix
		if err := os.Rename(p, aside); err != nil {
			return res, fmt.Errorf("挪开原数据库文件 %s: %w", p, err)
		}
		res.MovedAside = append(res.MovedAside, aside)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return res, fmt.Errorf("放置恢复的数据库: %w", err)
	}
	return res, nil
}

func fetch(ctx context.Context, tmpDir string, dst io.Writer, src Source, res *RestoreResult) error {
	if src.Path != "" {
		res.From = src.Path
		f, err := os.Open(src.Path)
		if err != nil {
			return fmt.Errorf("打开快照: %w", err)
		}
		defer f.Close()
		if strings.HasSuffix(src.Path, encSuffix) {
			if src.IdentityFile == "" {
				return errors.New("加密的快照需要用 -i 指定 age 私钥文件")
			}
			return Decrypt(dst, f, src.IdentityFile)
		}
		_, err = io.Copy(dst, f)
		return err
	}

	if src.Target == nil {
		return errors.New("没有配置 GUANDAN_BACKUP_URL，无法从异地恢复；或者给出本地快照文件的路径")
	}
	if src.IdentityFile == "" {
		return errors.New("从异地恢复需要用 -i 指定 age 私钥文件")
	}
	name := src.Object
	if name == "latest" {
		objs, err := src.Target.List(ctx)
		if err != nil {
			return fmt.Errorf("列出异地备份: %w", err)
		}
		name = ""
		for _, o := range objs { // 已按名字排序，名字里的时间是 UTC，字典序即时间序
			if _, ok := ParseSnapshotTime(o.Name); ok {
				name = o.Name
			}
		}
		if name == "" {
			return fmt.Errorf("%s 里没有任何备份", src.Target)
		}
	}
	res.From = strings.TrimSuffix(src.Target.String(), "/") + "/" + name

	// 先把密文完整下载下来再解密：下载中断时不会留下半份明文。
	// 临时文件放在数据目录里，因为容器的根文件系统是只读的。
	enc, err := os.CreateTemp(tmpDir, ".download-*"+encSuffix)
	if err != nil {
		return err
	}
	defer os.Remove(enc.Name())
	defer enc.Close()
	if err := src.Target.Get(ctx, name, enc); err != nil {
		return fmt.Errorf("下载 %s: %w", name, err)
	}
	if _, err := enc.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return Decrypt(dst, enc, src.IdentityFile)
}
