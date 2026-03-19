//go:build linux

package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/wallix/task/v3/errors"
)

// tryAcquire attempts to acquire a lock by binding an abstract unix socket.
// The kernel automatically frees the address when the process dies,
// so dead processes can never hold a stale lock.
//
// Once the lock is acquired a background goroutine accepts connections
// and writes the holder identity (pid, lock name, acquisition time) to
// each client. This lets waiters query the holder without any files.
func (f *Flock) tryAcquire(name string) (Unlocker, error) {
	addr := socketAddr(f.dir, name)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		if isAddrInUse(err) {
			return nil, errWouldBlock
		}
		return nil, err
	}
	h := &socketHandle{
		ln:   ln,
		info: fmt.Sprintf("pid=%d\nlock=%s\nacquired=%s", os.Getpid(), name, time.Now().Format(time.RFC3339)),
	}
	go h.serve()
	return h, nil
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
	ln   *net.UnixListener
	info string // holder identity sent to each connecting client
}

// serve accepts connections and writes the holder info to each client.
// Each connection is handled in its own goroutine with a write deadline
// so that a misbehaving client (that connects but never reads) cannot
// block the holder from serving other clients.
// It returns when the listener is closed (i.e. on Unlock).
func (h *socketHandle) serve() {
	for {
		conn, err := h.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_, _ = io.WriteString(conn, h.info)
		}()
	}
}

func (h *socketHandle) Unlock() error {
	if h.ln == nil {
		return nil
	}
	return h.ln.Close()
}

// readHolderInfo connects to the lock holder's socket and reads its
// identity. Returns "unknown" if the socket is not reachable.
func readHolderInfo(dir, name string) string {
	addr := socketAddr(dir, name)
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		return "unknown"
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	data, err := io.ReadAll(conn)
	if err != nil {
		return "unknown"
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "unknown"
	}
	return s
}

// processAlive reports whether a process with the given PID exists.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
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
