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
