package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Locker acquires exclusive locks for tasks. Implementations may use
// local file locks, distributed stores, or any other mechanism.
type Locker interface {
	Lock(name string, onContention func()) (Unlocker, error)
}

// Unlocker releases an acquired lock.
type Unlocker interface {
	Unlock() error
}

// Flock implements Locker using OS file locks.
// Locks are automatically released by the OS if the process dies.
type Flock struct {
	dir string
}

// NewFlock creates a Flock locker that stores lock files in dir.
func NewFlock(dir string) (*Flock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("lock: failed to create directory: %w", err)
	}
	return &Flock{dir: dir}, nil
}

// Lock acquires an exclusive lock for name. It blocks until the lock
// is available, calling onContention once if contention is detected.
func (f *Flock) Lock(name string, onContention func()) (Unlocker, error) {
	path := filepath.Join(f.dir, safeName(name)+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock: failed to open %s: %w", path, err)
	}

	notified := false
	for {
		err = lockFile(file)
		if err == nil {
			_ = file.Truncate(0)
			_, _ = file.Seek(0, 0)
			fmt.Fprintf(file, "%d\n", os.Getpid())
			return &flockHandle{file: file}, nil
		}
		if !notified && onContention != nil {
			onContention()
			notified = true
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type flockHandle struct {
	file *os.File
}

func (h *flockHandle) Unlock() error {
	if h.file == nil {
		return nil
	}
	unlockFile(h.file)
	return h.file.Close()
}

// unsafeChars matches characters outside the safe set [a-zA-Z0-9_.-].
var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// safeName converts a lock name to a filesystem-safe filename.
// Preserves readable characters and appends a hash when the name
// was modified to guarantee uniqueness.
func safeName(name string) string {
	safe := unsafeChars.ReplaceAllString(name, "_")
	if len(safe) > 200 {
		safe = safe[:200]
	}
	if safe != name || strings.Contains(name, "..") {
		h := sha256.Sum256([]byte(name))
		safe = safe + "_" + hex.EncodeToString(h[:])[:8]
	}
	if safe == "" || safe == "_" {
		h := sha256.Sum256([]byte(name))
		safe = hex.EncodeToString(h[:])[:16]
	}
	return safe
}
