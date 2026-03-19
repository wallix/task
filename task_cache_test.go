package task

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestSetCacheCommentRoundTrip(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	taskName := "myapp:build"
	sourcesHash := "abc123"
	generatesHash := "def456"

	if err := setCacheComment(zw, taskName, sourcesHash, generatesHash); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	meta := readCacheComment(zr)
	if meta.task != taskName {
		t.Errorf("task: got %q, want %q", meta.task, taskName)
	}
	if meta.sources != sourcesHash {
		t.Errorf("sources: got %q, want %q", meta.sources, sourcesHash)
	}
	if meta.generates != generatesHash {
		t.Errorf("generates: got %q, want %q", meta.generates, generatesHash)
	}
}

func TestReadCacheCommentEmpty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	meta := readCacheComment(zr)
	if meta.task != "" || meta.sources != "" || meta.generates != "" {
		t.Errorf("expected empty meta, got %+v", meta)
	}
}

func TestReadCacheCommentPartialFields(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// Only set generates, simulating a future format with fewer fields
	if err := zw.SetComment("generates:abc123"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	meta := readCacheComment(zr)
	if meta.task != "" {
		t.Errorf("task: got %q, want empty", meta.task)
	}
	if meta.sources != "" {
		t.Errorf("sources: got %q, want empty", meta.sources)
	}
	if meta.generates != "abc123" {
		t.Errorf("generates: got %q, want %q", meta.generates, "abc123")
	}
}

func TestReadCacheCommentUnknownFields(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := zw.SetComment("task:foo\nfuture_field:bar\ngenerates:abc"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	meta := readCacheComment(zr)
	if meta.task != "foo" {
		t.Errorf("task: got %q, want %q", meta.task, "foo")
	}
	if meta.generates != "abc" {
		t.Errorf("generates: got %q, want %q", meta.generates, "abc")
	}
}

func TestSetCacheCommentWithColonsInTaskName(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Task names commonly contain colons (e.g. "bastionadm:node_modules")
	taskName := "bastionadm:node_modules"
	if err := setCacheComment(zw, taskName, "src1", "gen1"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	meta := readCacheComment(zr)
	if meta.task != taskName {
		t.Errorf("task: got %q, want %q", meta.task, taskName)
	}
}
