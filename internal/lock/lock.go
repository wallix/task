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

const (
	flockRetryInterval   = 100 * time.Millisecond
	flockDefaultTimeout  = 1 * time.Hour
	flockWaitLogInterval = 10 * time.Minute
)

// Flock implements Locker using OS file locks.
// Locks are automatically released by the OS if the process dies.
type Flock struct {
	dir     string
	timeout time.Duration // 0 means use default (1h)

	// OnWaiting is called periodically (every 10 minutes) while blocked
	// on a lock. The arguments are the lock name and the current holder
	// info read from the lock file. May be nil.
	OnWaiting func(name, holder string)
}

// NewFlock creates a Flock locker that stores lock files in dir.
func NewFlock(dir string) (*Flock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("lock: failed to create directory: %w", err)
	}
	return &Flock{dir: dir}, nil
}

// NewFlockWithTimeout creates a Flock locker with a custom timeout.
// If timeout is 0, the default (1h) is used.
func NewFlockWithTimeout(dir string, timeout time.Duration) (*Flock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("lock: failed to create directory: %w", err)
	}
	return &Flock{dir: dir, timeout: timeout}, nil
}

// Lock acquires an exclusive lock for name. It blocks until the lock
// is available, the timeout expires, or an error occurs.
// onContention is called once when contention is first detected.
func (f *Flock) Lock(name string, onContention func()) (Unlocker, error) {
	path := filepath.Join(f.dir, safeName(name)+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock: failed to open %s: %w", path, err)
	}

	timeout := f.timeout
	if timeout == 0 {
		timeout = flockDefaultTimeout
	}
	deadline := time.Now().Add(timeout)
	notified := false
	lastLog := time.Time{}

	for {
		err = lockFile(file)
		if err == nil {
			_ = file.Truncate(0)
			_, _ = file.Seek(0, 0)
			fmt.Fprintf(file, "pid=%d\nlock=%s\n", os.Getpid(), name)
			return &flockHandle{file: file, name: name}, nil
		}
		if !notified && onContention != nil {
			onContention()
			notified = true
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("lock: timeout after %v acquiring %q", timeout, name)
		}
		if f.OnWaiting != nil && time.Since(lastLog) >= flockWaitLogInterval {
			f.OnWaiting(name, readHolder(path))
			lastLog = time.Now()
		}
		time.Sleep(flockRetryInterval)
	}
}

// ReadHolderFile reads the holder info from a lock file identified by
// directory and lock name. Returns the content or an error.
func ReadHolderFile(dir, name string) (string, error) {
	path := filepath.Join(dir, safeName(name)+".lock")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// readHolder reads the holder info from a lock file.
// Returns a descriptive string or "unknown" on error.
func readHolder(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "unknown"
	}
	return s
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
