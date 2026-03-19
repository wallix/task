//go:build !windows && !linux

package lock

import (
	"errors"
	"os"
	"syscall"
)

// lockFileBlocking acquires an exclusive lock, blocking until available.
// Returns nil on success. May return EINTR if interrupted by a signal.
func lockFileBlocking(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == nil {
			return nil
		}
		// Retry on EINTR (signal interrupted the syscall).
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err
	}
}

// lockFileTry attempts a non-blocking exclusive lock.
// Returns nil on success, errWouldBlock if held by another process,
// or another error for real failures.
func lockFileTry(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errWouldBlock
	}
	return err
}

func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
