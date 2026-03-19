//go:build !linux

package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wallix/task/v3/errors"
)

// tryAcquire attempts to acquire a lock using OS file locks (flock / LockFileEx).
// Returns errWouldBlock if the lock is held by another process.
//
// As a safety net, when contention is detected the holder PID is read
// from the lock file. If the holder process is no longer alive the
// lock file is removed so the next retry creates a fresh inode.
// This handles stale locks on NFS or after unclean shutdowns.
func (f *Flock) tryAcquire(name string) (Unlocker, error) {
	path := filepath.Join(f.dir, safeName(name)+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock: failed to open %s: %w", path, err)
	}
	if err := lockFileTry(file); err != nil {
		file.Close()
		if errors.Is(err, errWouldBlock) {
			evictStaleLock(path)
		}
		return nil, err
	}
	writeLockInfo(file, name)
	return &flockHandle{file: file, name: name}, nil
}

// evictStaleLock removes a lock file whose holder PID is no longer alive.
func evictStaleLock(path string) {
	info := readHolderFromFile(path)
	pid := readHolderPID(info)
	if pid > 0 && !processAlive(pid) {
		_ = os.Remove(path)
	}
}

// readHolderInfo reads holder info from the lock file on disk.
func readHolderInfo(dir, name string) string {
	return readHolderFromFile(filepath.Join(dir, safeName(name)+".lock"))
}

// readHolderFromFile reads the holder info from a lock file.
// Returns "unknown" on error.
func readHolderFromFile(path string) string {
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
