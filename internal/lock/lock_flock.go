//go:build !linux

package lock

import (
	"fmt"
	"os"
	"path/filepath"
)

// tryAcquire attempts to acquire a lock using OS file locks (flock / LockFileEx).
// Returns errWouldBlock if the lock is held by another process.
func (f *Flock) tryAcquire(name string) (Unlocker, error) {
	path := filepath.Join(f.dir, safeName(name)+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock: failed to open %s: %w", path, err)
	}
	if err := lockFileTry(file); err != nil {
		file.Close()
		return nil, err
	}
	writeLockInfo(file, name)
	return &flockHandle{file: file, name: name}, nil
}

func writeLockInfo(file *os.File, name string) {
	_ = file.Truncate(0)
	_, _ = file.Seek(0, 0)
	fmt.Fprintf(file, "pid=%d\nlock=%s\n", os.Getpid(), name)
}

type flockHandle struct {
	file *os.File
	name string
}

func (h *flockHandle) Unlock() error {
	if h.file == nil {
		return nil
	}
	unlockFile(h.file)
	return h.file.Close()
}
