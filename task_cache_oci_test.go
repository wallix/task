package task

import (
	"net/url"
	"testing"
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
