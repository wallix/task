package lock

import (
	"os"

	"golang.org/x/sys/windows"
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

// lockFileBlocking acquires an exclusive lock, blocking until available.
func lockFileBlocking(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		lockfileExclusiveLock,
		0,
		^uint32(0),
		^uint32(0),
		&ol,
	)
}

// lockFileTry attempts a non-blocking exclusive lock.
// Returns errWouldBlock if held by another process.
func lockFileTry(f *os.File) error {
	var ol windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		lockfileExclusiveLock|lockfileFailImmediately,
		0,
		^uint32(0),
		^uint32(0),
		&ol,
	)
	if err == windows.ERROR_LOCK_VIOLATION {
		return errWouldBlock
	}
	return err
}

func unlockFile(f *os.File) {
	var ol windows.Overlapped
	_ = windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		^uint32(0),
		^uint32(0),
		&ol,
	)
}
