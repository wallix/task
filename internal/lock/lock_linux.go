//go:build linux

package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"github.com/wallix/task/v3/errors"
)

// tryAcquire attempts to acquire a lock by binding an abstract unix socket.
// The kernel automatically frees the address when the process dies,
// so dead processes can never hold a stale lock.
// Returns errWouldBlock if the address is already in use.
func (f *Flock) tryAcquire(name string) (Unlocker, error) {
	addr := socketAddr(f.dir, name)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		if isAddrInUse(err) {
			return nil, errWouldBlock
		}
		return nil, err
	}
	// Write holder info for diagnostics (best-effort).
	infoPath := filepath.Join(f.dir, safeName(name)+".lock")
	writeInfoFile(infoPath, name)
	return &socketHandle{ln: ln, infoPath: infoPath}, nil
}

// socketAddr derives an abstract unix socket address from the directory
// and lock name. Abstract addresses start with a null byte and live in
// the kernel (no filesystem entry). The hash ensures uniqueness and
// keeps the address within the 108-byte sun_path limit.
func socketAddr(dir, name string) string {
	h := sha256.Sum256([]byte(dir + "\x00" + name))
	return "\x00task-lock-" + hex.EncodeToString(h[:16])
}

type socketHandle struct {
	ln       *net.UnixListener
	infoPath string
}

func (h *socketHandle) Unlock() error {
	if h.ln == nil {
		return nil
	}
	_ = os.Remove(h.infoPath)
	return h.ln.Close()
}

func writeInfoFile(path, name string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "pid=%d\nlock=%s\n", os.Getpid(), name)
}

func isAddrInUse(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			return errors.Is(sysErr.Err, syscall.EADDRINUSE)
		}
	}
	return false
}
