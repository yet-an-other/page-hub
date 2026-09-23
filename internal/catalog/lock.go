package catalog

import (
	"fmt"
	"os"
	"syscall"
)

// Lock is an exclusive advisory lock on a sidecar file next to the catalog.
// The running manager holds it for its lifetime, so administrative commands
// (migrate, commit) refuse to run concurrently. flock is used instead of
// SQLite's own locking so it composes with the driver's internal locks.
type Lock struct {
	file *os.File
}

func acquireFlock(lockPath string) (*Lock, error) {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open catalog lock %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("catalog is held by another Page Hub process; stop the runtime before running this command")
		}
		return nil, fmt.Errorf("lock catalog %s: %w", lockPath, err)
	}
	return &Lock{file: file}, nil
}

// Release removes the lock file while still holding the lock, then drops it.
// Unlinking first prevents a waiter from acquiring the now-orphaned inode and
// briefly sharing the catalog with a fresh lock file created afterwards.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	path := l.file.Name()
	_ = os.Remove(path)
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

func mkdirAll(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create catalog directory %s: %w", dir, err)
	}
	return nil
}
