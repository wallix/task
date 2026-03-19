package lock

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wallix/task/v3/errors"
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

// errWouldBlock is returned by tryAcquire when the lock is held.
var errWouldBlock = errors.New("lock: would block")

// Flock implements Locker using OS-level locks.
//
// On Linux the lock is an abstract unix socket — the kernel
// automatically releases it when the holding process dies.
// On other platforms it uses file locks (flock / LockFileEx).
type Flock struct {
	dir     string
	timeout time.Duration // 0 means use default (1h)

	// OnWaiting is called periodically (every 10 minutes) while blocked
	// on a lock. The arguments are the lock name and the current holder
	// info read from the lock holder. May be nil.
	OnWaiting func(name, holder string)
}

// NewFlock creates a Flock locker that stores lock state in dir.
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
	timeout := f.timeout
	if timeout == 0 {
		timeout = flockDefaultTimeout
	}

	deadline := time.Now().Add(timeout)
	notified := false
	lastLog := time.Time{}

	for {
		u, err := f.tryAcquire(name)
		if err == nil {
			return u, nil
		}
		if !errors.Is(err, errWouldBlock) {
			return nil, fmt.Errorf("lock: failed to acquire %q: %w", name, err)
		}
		if !notified && onContention != nil {
			onContention()
			notified = true
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock: timeout after %v acquiring %q", timeout, name)
		}
		if f.OnWaiting != nil && time.Since(lastLog) >= flockWaitLogInterval {
			f.OnWaiting(name, readHolderInfo(f.dir, name))
			lastLog = time.Now()
		}
		time.Sleep(flockRetryInterval)
	}
}

// ReadHolderFile returns the current holder info for the named lock.
// On Linux this connects to the holder's socket; on other platforms
// it reads the lock file.
func ReadHolderFile(dir, name string) (string, error) {
	s := readHolderInfo(dir, name)
	if s == "unknown" || s == "" {
		return "", fmt.Errorf("lock: no holder info for %q", name)
	}
	return s, nil
}

// readHolderPID extracts the PID from holder info text (format: "pid=12345\n...").
// Returns 0 if the text does not contain a valid PID line.
func readHolderPID(info string) int {
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "pid=") {
			pid, err := strconv.Atoi(strings.TrimPrefix(line, "pid="))
			if err == nil {
				return pid
			}
		}
	}
	return 0
}
