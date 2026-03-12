package lock

import (
	"os"

	"golang.org/x/sys/windows"
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

func lockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		lockfileExclusiveLock|lockfileFailImmediately,
		0,
		^uint32(0),
		^uint32(0),
		&ol,
	)
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
