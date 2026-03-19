//go:build linux

package lock_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wallix/task/v3/internal/lock"
)

func TestHolderInfoRobustUnderSlowClients(t *testing.T) {
	// A misbehaving client that connects to the holder socket but never
	// reads must not prevent other clients from querying the holder or
	// block the lock holder itself.
	t.Parallel()
	dir := t.TempDir()
	locker, err := lock.NewFlock(dir)
	require.NoError(t, err)

	u, err := locker.Lock("robust-test", nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, u.Unlock()) }()

	// Open several connections that never read — these should not
	// block the serve goroutine thanks to per-connection deadlines.
	var slowConns []net.Conn
	for i := 0; i < 10; i++ {
		addr := lock.SocketAddrForTest(dir, "robust-test")
		conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", addr)
		if err != nil {
			t.Logf("slow conn %d dial failed (acceptable): %v", i, err)
			continue
		}
		slowConns = append(slowConns, conn)
	}
	defer func() {
		for _, c := range slowConns {
			c.Close()
		}
	}()

	// A well-behaved client should still be able to read holder info
	// promptly despite the stalled connections above.
	done := make(chan string, 1)
	go func() {
		done <- lock.ReadHolderInfo(dir, "robust-test")
	}()

	select {
	case info := <-done:
		require.Contains(t, info, "pid=")
		require.Contains(t, info, "lock=robust-test")
	case <-time.After(3 * time.Second):
		t.Fatal("readHolderInfo blocked — slow clients are starving the holder")
	}
}
