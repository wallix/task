package task

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wallix/task/v3/errors"
	"github.com/wallix/task/v3/internal/fingerprint"
	"github.com/wallix/task/v3/internal/logger"
	"github.com/wallix/task/v3/internal/ocicas"
	"github.com/wallix/task/v3/taskfile/ast"
)

// isCacheUnreachable reports whether err is a network-level failure reaching the
// cache registry (dial refused/timeout, DNS failure, stalled response) as
// opposed to a cache miss or a content error.
func isCacheUnreachable(err error) bool {
	// context.DeadlineExceeded satisfies net.Error, but a caller-imposed
	// deadline is not a registry-reachability signal, so exclude it. (Plain
	// cancellation via context.Canceled is not a net.Error and is already
	// excluded by the errors.As check below.)
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// warnCacheUnreachable emits a single visible warning the first time host is
// found unreachable. Cache operations otherwise fail quietly (VerboseErrf) so a
// normal miss stays silent; a connectivity failure that silently disables the
// cache should not.
func (e *Executor) warnCacheUnreachable(host string, err error) {
	if host == "" || !isCacheUnreachable(err) {
		return
	}
	if _, seen := e.unreachableWarned.LoadOrStore(host, struct{}{}); seen {
		return
	}
	e.Logger.Errf(logger.Yellow, "task: WARNING: cache registry %s unreachable, continuing without cache (%v)\n", host, err)
}

// oci:// cache transport: the entry is an ocicas artifact (content-defined
// chunks deduplicated registry-side, see internal/ocicas) instead of a zip
// blob. URL shape:
//
//	oci://[user:password@]host/repo:tag[?ca=<file>][&cas=<dir>][&plainhttp=1]
//
// The tag carries the cache key; OCI tags only allow [A-Za-z0-9._-] (128
// max), so task names must be sanitized in the Taskfile template. `ca` adds
// a trust anchor (self-signed corp registry), `cas` overrides the local
// chunk store (default: <user cache dir>/task/ocicas), `plainhttp` is for
// local development registries. There is no TTL: the registry's retention
// policy prunes old entries.
//
// Credentials and trust can also come from the environment, keeping secrets
// out of the Taskfile and of the resolved URLs: TASK_CACHE_OCI_USER,
// TASK_CACHE_OCI_PASSWORD, TASK_CACHE_OCI_CA and TASK_CACHE_OCI_CAS_DIR are
// the fallbacks for the URL's userinfo, ?ca= and ?cas= parts.

// annotation keys of the cache metadata on the artifact manifest — same
// vocabulary as the zip comment (cacheMeta).
const (
	annTask      = "com.wallix.task.name"
	annSources   = "com.wallix.task.sources"
	annGenerates = "com.wallix.task.generates"
)

// parseOCICacheURL splits an oci:// cache URL into the repository
// reference, the tag, and the store options.
func parseOCICacheURL(u *url.URL) (repo, tag string, opts ocicas.RemoteOptions, err error) {
	last := strings.LastIndex(u.Path, ":")
	if last < 0 {
		return "", "", opts, fmt.Errorf("oci cache url %q: missing :tag", u.Redacted())
	}
	repo = u.Host + u.Path[:last]
	tag = u.Path[last+1:]
	if tag == "" {
		return "", "", opts, fmt.Errorf("oci cache url %q: empty tag", u.Redacted())
	}
	if u.User != nil {
		opts.Username = u.User.Username()
		opts.Password, _ = u.User.Password()
	}
	if opts.Username == "" {
		opts.Username = os.Getenv("TASK_CACHE_OCI_USER")
		opts.Password = os.Getenv("TASK_CACHE_OCI_PASSWORD")
	}
	q := u.Query()
	opts.CAFile = q.Get("ca")
	if opts.CAFile == "" {
		opts.CAFile = os.Getenv("TASK_CACHE_OCI_CA")
	}
	opts.PlainHTTP = q.Get("plainhttp") == "1"
	opts.CacheDir = q.Get("cas")
	if opts.CacheDir == "" {
		opts.CacheDir = os.Getenv("TASK_CACHE_OCI_CAS_DIR")
	}
	if opts.CacheDir == "" {
		if base, err := os.UserCacheDir(); err == nil {
			opts.CacheDir = filepath.Join(base, "task", "ocicas")
		}
	}
	return repo, tag, opts, nil
}

func (e *Executor) openOCICacheStore(u *url.URL) (*ocicas.Store, string, error) {
	repo, tag, opts, err := parseOCICacheURL(u)
	if err != nil {
		return nil, "", err
	}
	store, err := ocicas.NewRemoteStore(repo, opts)
	if err != nil {
		return nil, "", err
	}
	return store, tag, nil
}

// cacheRestoreOCI pulls the entry's chunks (through the local chunk CAS)
// and assembles the generated files in place.
func (e *Executor) cacheRestoreOCI(t *ast.Task, u *url.URL) (bool, cacheMeta) {
	store, tag, err := e.openOCICacheStore(u)
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache restore %q: %v\n", t.Name(), err)
		return false, cacheMeta{}
	}
	ctx := context.Background()
	ann, err := store.ResolveAnnotations(ctx, tag)
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache restore %q: %v\n", t.Name(), err)
		e.warnCacheUnreachable(u.Host, err)
		return false, cacheMeta{}
	}
	if ann == nil {
		return false, cacheMeta{} // cache miss
	}
	if ann[annGenerates] == "" {
		e.Logger.Errf(logger.Yellow, "task: WARNING: cache for %q has no generates checksum, rejecting\n", t.Name())
		return false, cacheMeta{}
	}
	// take meta from the annotations Pull returns, not the pre-check ones:
	// the tag may have been repushed between the two resolves
	_, ann, err = store.Pull(ctx, tag, e.Dir)
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache restore %q: %v\n", t.Name(), err)
		e.warnCacheUnreachable(u.Host, err)
		return false, cacheMeta{}
	}
	e.Logger.Errf(logger.Magenta, "task: %q restored from cache\n", t.Name())
	return true, cacheMeta{task: ann[annTask], sources: ann[annSources], generates: ann[annGenerates]}
}

// cacheSaveOCI chunks the generated files and pushes the missing blobs plus
// the index/manifest. A tag already carrying the same generates checksum is
// left untouched (the equivalent of archiveMatches for zips).
func (e *Executor) cacheSaveOCI(t *ast.Task, u *url.URL) {
	checker := fingerprint.NewChecksumChecker(e.TempDir.Fingerprint, t)
	st, err := checker.Status()
	if err != nil || !st.UpToDate || len(st.CacheFiles) == 0 {
		return
	}
	store, tag, err := e.openOCICacheStore(u)
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save %q: %v\n", t.Name(), err)
		return
	}
	ctx := context.Background()
	if ann, err := store.ResolveAnnotations(ctx, tag); err == nil && ann != nil &&
		ann[annGenerates] == st.GeneratesHash {
		return // same content already pushed
	}

	rels := make([]string, 0, len(st.CacheFiles))
	for _, f := range st.CacheFiles {
		rel := f
		if filepath.IsAbs(f) {
			if rel, err = filepath.Rel(e.Dir, f); err != nil {
				e.Logger.VerboseErrf(logger.Yellow, "task: cache save %q: %v\n", t.Name(), err)
				return
			}
		}
		rels = append(rels, filepath.ToSlash(rel))
	}

	sess := store.NewPushSession(ctx)
	idx, err := ocicas.Build(e.Dir, rels, sess.Sink())
	werr := sess.Wait() // always drain the uploads, even when Build failed
	if err == nil {
		err = werr
	}
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save %q: %v\n", t.Name(), err)
		e.warnCacheUnreachable(u.Host, err)
		return
	}
	err = store.PushIndex(ctx, tag, idx, map[string]string{
		annTask:      t.Name(),
		annSources:   checker.SourceValue(),
		annGenerates: st.GeneratesHash,
	})
	if err != nil {
		e.Logger.VerboseErrf(logger.Yellow, "task: cache save %q: %v\n", t.Name(), err)
		e.warnCacheUnreachable(u.Host, err)
		return
	}
	pushed, skipped, sent := sess.Stats()
	e.Logger.Errf(logger.Magenta, "task: %q saved to cache (pushed %d/%d chunks, %.1f MB)\n",
		t.Name(), pushed, pushed+skipped, float64(sent)/1e6)
}
