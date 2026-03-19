package lock_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wallix/task/v3/internal/lock"
)

func TestFlockMutualExclusion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locker, err := lock.NewFlock(dir)
	if err != nil {
		t.Fatal(err)
	}

	var running atomic.Int32
	var maxConcurrent atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := locker.Lock("test-task", nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { require.NoError(t, u.Unlock()) }()

			cur := running.Add(1)
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			running.Add(-1)
		}()
	}
	wg.Wait()

	if maxConcurrent.Load() != 1 {
		t.Fatalf("expected max concurrency of 1, got %d", maxConcurrent.Load())
	}
}

func TestFlockOnContention(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locker, err := lock.NewFlock(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Hold lock
	u1, err := locker.Lock("contention-test", nil)
	if err != nil {
		t.Fatal(err)
	}

	var contended atomic.Bool
	done := make(chan struct{})
	go func() {
		u2, err := locker.Lock("contention-test", func() {
			contended.Store(true)
		})
		if err != nil {
			t.Error(err)
			return
		}
		require.NoError(t, u2.Unlock())
		close(done)
	}()

	// Give the goroutine time to hit contention
	time.Sleep(250 * time.Millisecond)
	if !contended.Load() {
		t.Fatal("expected onContention to be called")
	}

	// Release first lock
	require.NoError(t, u1.Unlock())

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for second lock")
	}
}

func TestFlockDifferentNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locker, err := lock.NewFlock(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Two different names should not block each other
	u1, err := locker.Lock("task-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { require.NoError(t, u1.Unlock()) }()

	done := make(chan struct{})
	go func() {
		u2, err := locker.Lock("task-b", nil)
		if err != nil {
			t.Error(err)
			return
		}
		require.NoError(t, u2.Unlock())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("different lock names should not block each other")
	}
}

func TestFlockTimeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locker, err := lock.NewFlockWithTimeout(dir, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	// Hold lock indefinitely
	u1, err := locker.Lock("timeout-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { require.NoError(t, u1.Unlock()) }()

	// Second lock should time out
	start := time.Now()
	_, err = locker.Lock("timeout-test", nil)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
	require.Contains(t, err.Error(), "timeout-test")
	// Should have waited ~500ms, not much longer
	require.Less(t, elapsed, 2*time.Second)
}

func TestFlockNoContentionCallbackOnUncontended(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locker, err := lock.NewFlock(dir)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	u, err := locker.Lock("uncontended", func() {
		called = true
	})
	require.NoError(t, err)
	require.NoError(t, u.Unlock())

	if called {
		t.Fatal("onContention should not be called when lock is uncontended")
	}
}

func TestFlockBlockingAcquireAfterRelease(t *testing.T) {
	// Verifies that a waiter actually acquires the lock (not just
	// returns an error) once the holder releases it.
	t.Parallel()
	dir := t.TempDir()
	locker, err := lock.NewFlockWithTimeout(dir, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	u1, err := locker.Lock("release-test", nil)
	require.NoError(t, err)

	acquired := make(chan struct{})
	go func() {
		u2, err := locker.Lock("release-test", nil)
		if err != nil {
			t.Error(err)
			return
		}
		close(acquired)
		require.NoError(t, u2.Unlock())
	}()

	// Let the goroutine enter the retry loop.
	time.Sleep(300 * time.Millisecond)

	// Release — the waiter should acquire promptly.
	require.NoError(t, u1.Unlock())

	select {
	case <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("waiter did not acquire lock after release")
	}
}

func TestFlockConcurrentHighContention(t *testing.T) {
	// Stress test: many goroutines competing for the same lock.
	t.Parallel()
	dir := t.TempDir()
	locker, err := lock.NewFlockWithTimeout(dir, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	const n = 20
	var running atomic.Int32
	var maxConcurrent atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := locker.Lock("stress", nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { require.NoError(t, u.Unlock()) }()

			cur := running.Add(1)
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			// Hold the lock briefly to create contention.
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
		}()
	}
	wg.Wait()

	if maxConcurrent.Load() != 1 {
		t.Fatalf("expected max concurrency of 1, got %d", maxConcurrent.Load())
	}
}

func TestFlockHolderInfo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locker, err := lock.NewFlock(dir)
	if err != nil {
		t.Fatal(err)
	}

	u, err := locker.Lock("holder-test", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Read the lock file to verify holder info format
	data, err := lock.ReadHolderFile(dir, "holder-test")
	require.NoError(t, err)
	require.Contains(t, data, "pid=")
	require.Contains(t, data, "lock=holder-test")

	require.NoError(t, u.Unlock())
}
