//go:build unix

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// DirLock 是数据目录的独占文件锁（docs/server-design.md §12.1 第 2 步）。
// 它保证「只有一个实例」不靠口头约定：两个容器挂同一个卷时，第二个起不来。
type DirLock struct{ file *os.File }

// LockDataDir 对 dir/.lock 加 flock 独占锁。拿不到锁说明已有实例在用这个目录。
func LockDataDir(dir string) (*DirLock, error) {
	path := filepath.Join(dir, ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开锁文件 %s: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("数据目录 %s 已被另一个实例占用", dir)
		}
		return nil, fmt.Errorf("锁定 %s: %w", path, err)
	}
	return &DirLock{file: f}, nil
}

// Release 释放文件锁（§12.4 第 6 步）。可以重复调用。
func (l *DirLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}

// FreeBytes 返回 path 所在文件系统的可用字节数（§12.1 第 3 步、§12.5）。
func FreeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("读取 %s 的磁盘空间: %w", path, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
