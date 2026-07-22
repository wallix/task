package task

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"

	"oras.land/oras-go/v2/errdef"

	"github.com/wallix/task/v3/internal/logger"
)

func TestParseOCICacheURL(t *testing.T) {
	t.Parallel()

	u, _ := url.Parse("oci://ci:secret@10.10.140.49/task-cache:build-thing-abc123?ca=/etc/ca.crt&cas=/var/cache/x")
	repo, tag, opts, err := parseOCICacheURL(u)
	if err != nil {
		t.Fatal(err)
	}
	if repo != "10.10.140.49/task-cache" {
		t.Errorf("repo %q", repo)
	}
	if tag != "build-thing-abc123" {
		t.Errorf("tag %q", tag)
	}
	if opts.Username != "ci" || opts.Password != "secret" {
		t.Errorf("credentials %q/%q", opts.Username, opts.Password)
	}
	if opts.CAFile != "/etc/ca.crt" || opts.CacheDir != "/var/cache/x" || opts.PlainHTTP {
		t.Errorf("options %+v", opts)
	}

	// default chunk CAS dir when ?cas= is absent
	u, _ = url.Parse("oci://host/repo:tag")
	_, _, opts, err = parseOCICacheURL(u)
	if err != nil {
		t.Fatal(err)
	}
	if opts.CacheDir == "" {
		t.Error("expected a default chunk CAS dir")
	}

	u, _ = url.Parse("oci://host/repo:tag?plainhttp=1")
	_, _, opts, err = parseOCICacheURL(u)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.PlainHTTP {
		t.Error("plainhttp=1 not applied")
	}

	for _, bad := range []string{"oci://host/repo", "oci://host/repo:"} {
		u, _ := url.Parse(bad)
		if _, _, _, err := parseOCICacheURL(u); err == nil {
			t.Errorf("%s: expected an error", bad)
		}
	}
}

func TestParseOCICacheURLEnvFallbacks(t *testing.T) {
	t.Setenv("TASK_CACHE_OCI_USER", "envuser")
	t.Setenv("TASK_CACHE_OCI_PASSWORD", "envpass")
	t.Setenv("TASK_CACHE_OCI_CA", "/env/ca.crt")
	t.Setenv("TASK_CACHE_OCI_CAS_DIR", "/env/cas")

	u, _ := url.Parse("oci://host/repo:tag")
	_, _, opts, err := parseOCICacheURL(u)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Username != "envuser" || opts.Password != "envpass" ||
		opts.CAFile != "/env/ca.crt" || opts.CacheDir != "/env/cas" {
		t.Errorf("env fallbacks not applied: %+v", opts)
	}

	// the URL wins over the environment
	u, _ = url.Parse("oci://urluser:urlpass@host/repo:tag?ca=/url/ca&cas=/url/cas")
	_, _, opts, err = parseOCICacheURL(u)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Username != "urluser" || opts.CAFile != "/url/ca" || opts.CacheDir != "/url/cas" {
		t.Errorf("URL should take precedence: %+v", opts)
	}
}

func TestIsCacheUnreachable(t *testing.T) {
	t.Parallel()

	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"dial failure", dialErr, true},
		{"wrapped dial failure", fmt.Errorf("resolve annotations: %w", dialErr), true},
		{"cache miss", errdef.ErrNotFound, false},
		{"wrapped cache miss", fmt.Errorf("pull: %w", errdef.ErrNotFound), false},
		{"content error", errors.New("bad manifest"), false},
		{"context canceled", context.Canceled, false},
		// context.DeadlineExceeded satisfies net.Error but a caller-imposed
		// deadline is not a registry-reachability signal.
		{"context deadline", context.DeadlineExceeded, false},
		{"wrapped context deadline", fmt.Errorf("pull: %w", context.DeadlineExceeded), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := isCacheUnreachable(tc.err); got != tc.want {
			t.Errorf("%s: isCacheUnreachable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWarnCacheUnreachable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	e := &Executor{Logger: &logger.Logger{Stderr: &buf}}
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	// First unreachable failure for a host warns once; repeats stay silent.
	e.warnCacheUnreachable("host-a", dialErr)
	e.warnCacheUnreachable("host-a", dialErr)
	if n := strings.Count(buf.String(), "host-a"); n != 1 {
		t.Errorf("host-a warned %d times, want 1", n)
	}

	// A different host warns independently.
	e.warnCacheUnreachable("host-b", dialErr)
	if n := strings.Count(buf.String(), "host-b"); n != 1 {
		t.Errorf("host-b warned %d times, want 1", n)
	}

	// A non-network error (cache miss / content error) never warns.
	buf.Reset()
	e.warnCacheUnreachable("host-c", errdef.ErrNotFound)
	if buf.Len() != 0 {
		t.Errorf("cache miss should not warn, got %q", buf.String())
	}

	// An empty host never warns.
	e.warnCacheUnreachable("", dialErr)
	if buf.Len() != 0 {
		t.Errorf("empty host should not warn, got %q", buf.String())
	}
}
