package task_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wallix/task/v3"
	"github.com/wallix/task/v3/internal/lock"
)

// lockProbe is a Locker that, at the moment the lock is released, records
// whether the cache entry already exists on disk. The cache upload runs
// inside the task lock, so the entry must be present before Unlock fires.
type lockProbe struct {
	cacheFile         string
	locks             atomic.Int64
	unlocks           atomic.Int64
	savedBeforeUnlock atomic.Bool
}

func (p *lockProbe) Lock(string, func()) (lock.Unlocker, error) {
	p.locks.Add(1)
	return p, nil
}

func (p *lockProbe) Unlock() error {
	p.unlocks.Add(1)
	if _, err := os.Stat(p.cacheFile); err == nil {
		p.savedBeforeUnlock.Store(true)
	}
	return nil
}

// TestCacheSavedWhileLockHeld guards the invariant that the cache upload
// happens before the task lock is released: a waiter on the same key would
// otherwise wake up to a missing entry and rebuild it. See the cacheSave
// call inside RunTask's locked closure.
func TestCacheSavedWhileLockHeld(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "cache", "build.zip")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "source.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Taskfile.yml"), fmt.Appendf(nil, `version: '3'
tasks:
  build:
    sources:
      - source.txt
    generates:
      - output.txt
    cache:
      url: 'file://%s'
    cmds:
      - cp source.txt output.txt
`, cacheFile), 0o644))

	probe := &lockProbe{cacheFile: cacheFile}
	e := task.NewExecutor(
		task.WithDir(dir),
		task.WithStdout(io.Discard),
		task.WithStderr(io.Discard),
		task.WithTempDir(task.TempDir{Fingerprint: filepath.Join(dir, ".task")}),
	)
	e.Locker = probe
	require.NoError(t, e.Setup())
	require.NoError(t, e.Run(t.Context(), &task.Call{Task: "build"}))

	require.Positive(t, probe.locks.Load(), "the task lock was never acquired — test would not prove anything")
	require.Positive(t, probe.unlocks.Load(), "the task lock was never released")
	require.FileExists(t, cacheFile, "the cache entry was not saved at all")
	assert.True(t, probe.savedBeforeUnlock.Load(),
		"the cache entry did not exist when the lock was released: the upload escaped the lock")
}
