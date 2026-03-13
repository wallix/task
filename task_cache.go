package task

import (
	"archive/zip"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wallix/task/v3/internal/fingerprint"
	"github.com/wallix/task/v3/internal/lock"
	"github.com/wallix/task/v3/internal/logger"
	"github.com/wallix/task/v3/internal/redis"
	"github.com/wallix/task/v3/taskfile/ast"
)

// cacheEnabled evaluates whether the cache block is active for a task.
// Returns false if the block is nil, explicitly disabled, or if the
// resolved enabled condition is empty/false.
func (e *Executor) cacheEnabled(t *ast.Task) bool {
	c := t.Cache
	if c == nil {
		return false
	}
	if c.Enabled != nil {
		return *c.Enabled
	}
	if c.If != "" {
		// The If field is template-resolved during compilation.
		// Treat "true" as enabled, anything else as disabled.
		v := strings.TrimSpace(c.If)
		return v != "" && v != "false" && v != "0"
	}
	return true // cache block present, no condition → enabled
}

// evalCacheURL parses the already-resolved cache.url template string as a URL.
// Template variables (.TASK, .CHECKSUM, urlsafe, etc.) are resolved during
// task compilation, so the string is ready to parse directly.
func (e *Executor) evalCacheURL(t *ast.Task) (*url.URL, error) {
	if t.Cache == nil || t.Cache.URL == "" {
		return nil, nil
	}

	raw := strings.TrimSpace(t.Cache.URL)
	if raw == "" {
		return nil, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("cache url %q: %w", raw, err)
	}
	return u, nil
}

// cacheRestore attempts to download and extract a cached archive.
// Returns true if the cache was restored successfully.
func (e *Executor) cacheRestore(t *ast.Task) bool {
	u, err := e.evalCacheURL(t)
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache restore %q: %v\n", t.Name(), err)
		return false
	}
	if u == nil {
		return false
	}

	switch u.Scheme {
	case "file":
		return e.cacheRestoreFile(t, u.Path)
	case "redis":
		return e.cacheRestoreRedis(t, u)
	default:
		e.Logger.VerboseErrf(logger.Yellow, "task: unsupported cache scheme %q\n", u.Scheme)
		return false
	}
}

// cacheRestoreFile extracts a zip archive from a file:// path into the
// task's working directory. Returns true on success.
func (e *Executor) cacheRestoreFile(t *ast.Task, zipPath string) bool {
	f, err := os.Open(zipPath)
	if err != nil {
		return false // miss — file doesn't exist
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return false
	}

	zr, err := zip.NewReader(f, stat.Size())
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache file %q corrupt: %v\n", zipPath, err)
		return false
	}

	baseDir := e.Dir
	for _, entry := range zr.File {
		if err := extractZipEntry(baseDir, entry); err != nil {
			e.Logger.VerboseErrf(logger.Yellow, "task: cache extract %s: %v\n", entry.Name, err)
			return false
		}
	}

	e.Logger.Errf(logger.Magenta, "task: %q restored from cache\n", t.Name())
	return true
}

// cacheSave exports generates to a zip and uploads to the cache URL.
func (e *Executor) cacheSave(t *ast.Task) {
	u, err := e.evalCacheURL(t)
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save %q: %v\n", t.Name(), err)
		return
	}
	if u == nil {
		return
	}

	switch u.Scheme {
	case "file":
		e.cacheSaveFile(t, u.Path)
	case "redis":
		e.cacheSaveRedis(t, u)
	default:
		e.Logger.VerboseErrf(logger.Yellow, "task: unsupported cache scheme %q\n", u.Scheme)
	}
}

// cacheSaveFile collects generated files and writes them to a zip at the
// given path. Skips writing if the archive already matches.
func (e *Executor) cacheSaveFile(t *ast.Task, zipPath string) {
	st, err := fingerprint.NewChecksumChecker(e.TempDir.Fingerprint, t).Status()
	if err != nil || !st.UpToDate {
		return
	}

	files := st.CacheFiles
	if len(files) == 0 {
		return
	}

	// Skip if archive already matches
	if archiveMatches(e.Dir, zipPath, files) {
		return
	}

	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save mkdir: %v\n", err)
		return
	}

	zf, err := os.Create(zipPath)
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save %q: %v\n", zipPath, err)
		return
	}
	defer zf.Close()

	zw := zip.NewWriter(zf)
	for _, f := range files {
		if err := addFileToZip(zw, e.Dir, f); err != nil {
			e.Logger.VerboseErrf(logger.Yellow, "task: cache save add %s: %v\n", f, err)
			zw.Close()
			os.Remove(zipPath)
			return
		}
	}
	if err := zw.Close(); err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save finalize: %v\n", err)
		os.Remove(zipPath)
		return
	}

	e.Logger.VerboseErrf(logger.Magenta, "task: %q saved to cache\n", t.Name())
}

// cacheRestoreRedis downloads a zip from Redis and extracts it.
func (e *Executor) cacheRestoreRedis(t *ast.Task, u *url.URL) bool {
	tmpDir, err := os.MkdirTemp("", "task-cache-*")
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache restore %q: %v\n", t.Name(), err)
		return false
	}
	defer os.RemoveAll(tmpDir)

	localPath, err := redis.CacheGet(u, tmpDir)
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache restore %q: %v\n", t.Name(), err)
		return false
	}
	if localPath == "" {
		return false // cache miss
	}
	return e.cacheRestoreFile(t, localPath)
}

// cacheSaveRedis builds a zip of generates and uploads to Redis.
func (e *Executor) cacheSaveRedis(t *ast.Task, u *url.URL) {
	st, err := fingerprint.NewChecksumChecker(e.TempDir.Fingerprint, t).Status()
	if err != nil || !st.UpToDate {
		return
	}
	files := st.CacheFiles
	if len(files) == 0 {
		return
	}

	tmpFile, err := os.CreateTemp("", "task-cache-*.zip")
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save %q: %v\n", t.Name(), err)
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	zw := zip.NewWriter(tmpFile)
	for _, f := range files {
		if err := addFileToZip(zw, e.Dir, f); err != nil {
			e.Logger.VerboseErrf(logger.Yellow, "task: cache save add %s: %v\n", f, err)
			zw.Close()
			tmpFile.Close()
			return
		}
	}
	if err := zw.Close(); err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save finalize: %v\n", err)
		tmpFile.Close()
		return
	}
	tmpFile.Close()

	if err := redis.CachePut(u, tmpPath); err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save %q: %v\n", t.Name(), err)
		return
	}
	e.Logger.VerboseErrf(logger.Magenta, "task: %q saved to redis cache\n", t.Name())
}

// evalCacheLocker parses the already-resolved cache.lock template string and
// returns a Locker for the given URL scheme. Returns nil if lock is not configured.
// Template variables are resolved during task compilation.
func (e *Executor) evalCacheLocker(t *ast.Task) lock.Locker {
	if t.Cache == nil || t.Cache.Lock == "" {
		return nil
	}

	raw := strings.TrimSpace(t.Cache.Lock)
	if raw == "" {
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache lock url %q: %v\n", raw, err)
		return nil
	}

	switch u.Scheme {
	case "redis":
		return redis.NewLocker(u)
	default:
		e.Logger.VerboseErrf(logger.Yellow, "task: unsupported lock scheme %q\n", u.Scheme)
		return nil
	}
}
