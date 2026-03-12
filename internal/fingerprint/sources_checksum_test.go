package fingerprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wallix/task/v3/taskfile/ast"
)

// helper to create a file with given content inside dir.
func createFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// makeTask builds a minimal ast.Task suitable for checksum tests.
func makeTask(name, dir string, sources, generates []*ast.Glob, cmds []*ast.Cmd) *ast.Task {
	return &ast.Task{
		Task:      name,
		Dirs:      []string{dir},
		Sources:   sources,
		Generates: generates,
		Cmds:      cmds,
	}
}

func mustSourceValue(t *testing.T, checker *ChecksumChecker, task *ast.Task) string {
	t.Helper()
	v, err := checker.SourceValue(task)
	if err != nil {
		t.Fatalf("SourceValue failed: %v", err)
	}
	return v
}

func TestNormalizeFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		In, Out string
	}{
		{"foobarbaz", "foobarbaz"},
		{"foo/bar/baz", "foo-bar-baz"},
		{"foo@bar/baz", "foo-bar-baz"},
		{"foo1bar2baz3", "foo1bar2baz3"},
	}
	for _, test := range tests {
		assert.Equal(t, test.Out, normalizeFilename(test.In))
	}
}

func TestSerializeCmd(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "cmd[0]:echo hello", serializeCmd(0, &ast.Cmd{Cmd: "echo hello"}))
	assert.Equal(t, "cmd[3]:go build", serializeCmd(3, &ast.Cmd{Cmd: "go build"}))
}

func TestFilterChecksumData(t *testing.T) {
	t.Parallel()

	checker := &ChecksumChecker{tempDir: t.TempDir()}

	t.Run("file sources become globs and srcrule data", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			Sources: []*ast.Glob{
				{Glob: "*.go"},
				{Glob: "*.txt", Negate: true},
			},
		}
		globs, data := checker.filterChecksumData(task)
		assert.Len(t, globs, 2)
		assert.Equal(t, "*.go", globs[0].Glob)
		assert.Contains(t, data, "srcrule:*.go")
		assert.Contains(t, data, "srcrule:!*.txt")
	})

	t.Run("value sources go to data only", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			Sources: []*ast.Glob{
				{Glob: "value:myval"},
				{Glob: "src.go"},
			},
		}
		globs, data := checker.filterChecksumData(task)
		assert.Len(t, globs, 1)
		assert.Equal(t, "src.go", globs[0].Glob)
		assert.Contains(t, data, "value:myval")
		assert.Contains(t, data, "srcrule:src.go")
	})

	t.Run("commands are serialized", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			Cmds: []*ast.Cmd{
				{Cmd: "echo hello"},
				{Cmd: "go build"},
			},
		}
		_, data := checker.filterChecksumData(task)
		assert.Contains(t, data, "cmd[0]:echo hello")
		assert.Contains(t, data, "cmd[1]:go build")
	})

	t.Run("generates become genrule data", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			Generates: []*ast.Glob{
				{Glob: "out/*.bin"},
				{Glob: "tmp/*", Negate: true},
			},
		}
		_, data := checker.filterChecksumData(task)
		assert.Contains(t, data, "genrule:out/*.bin")
		assert.Contains(t, data, "genrule:!tmp/*")
	})

	t.Run("data is sorted", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			Sources: []*ast.Glob{
				{Glob: "z.go"},
				{Glob: "a.go"},
			},
			Cmds: []*ast.Cmd{
				{Cmd: "make"},
			},
			Generates: []*ast.Glob{
				{Glob: "out.bin"},
			},
		}
		_, data := checker.filterChecksumData(task)
		for i := 1; i < len(data); i++ {
			assert.LessOrEqual(t, data[i-1], data[i], "data should be sorted")
		}
	})
}

func TestSourceValueReturnsSourcesOnly(t *testing.T) {
	t.Parallel()

	checker := &ChecksumChecker{tempDir: t.TempDir()}

	task := &ast.Task{
		Task: "test-value",
		Sources: []*ast.Glob{
			{Glob: "value:hello"},
		},
		Generates: []*ast.Glob{
			{Glob: "value:world"},
		},
	}

	hash, err := checker.SourceValue(task)
	require.NoError(t, err)
	assert.False(t, strings.Contains(hash, "\n"),
		"SourceValue() should return sources-only hash without newline, got: %q", hash)
	assert.NotEmpty(t, hash)
}

func TestRelativePathIndependence(t *testing.T) {
	t.Parallel()

	dirA := t.TempDir()
	dirB := t.TempDir()

	createFile(t, dirA, "hello.txt", "hello world")
	createFile(t, dirB, "hello.txt", "hello world")

	checker := NewChecksumChecker(t.TempDir())

	taskA := makeTask("test", dirA,
		[]*ast.Glob{{Glob: "hello.txt"}},
		nil, nil)
	taskB := makeTask("test", dirB,
		[]*ast.Glob{{Glob: "hello.txt"}},
		nil, nil)

	hashA := mustSourceValue(t, checker, taskA)
	hashB := mustSourceValue(t, checker, taskB)

	if hashA != hashB {
		t.Errorf("expected same checksum for identical relative layout, got %s vs %s", hashA, hashB)
	}
}

func TestCommandStringInclusion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "src.txt", "content")

	checker := NewChecksumChecker(t.TempDir())
	sources := []*ast.Glob{{Glob: filepath.Join(dir, "src.txt")}}

	taskNoCmds := makeTask("test", dir, sources, nil, nil)
	taskWithCmds := makeTask("test", dir, sources, nil,
		[]*ast.Cmd{{Cmd: "echo hello"}})

	hashNoCmds := mustSourceValue(t, checker, taskNoCmds)
	hashWithCmds := mustSourceValue(t, checker, taskWithCmds)

	if hashNoCmds == hashWithCmds {
		t.Error("expected checksum to change when commands are added")
	}

	taskDiffCmd := makeTask("test", dir, sources, nil,
		[]*ast.Cmd{{Cmd: "echo goodbye"}})
	hashDiffCmd := mustSourceValue(t, checker, taskDiffCmd)

	if hashWithCmds == hashDiffCmd {
		t.Error("expected checksum to change when command string changes")
	}
}

func TestChecksumStability(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "a.txt", "aaa")
	createFile(t, dir, "b.txt", "bbb")

	checker := NewChecksumChecker(t.TempDir())
	sources := []*ast.Glob{
		{Glob: filepath.Join(dir, "a.txt")},
		{Glob: filepath.Join(dir, "b.txt")},
	}
	task := makeTask("stable", dir, sources, nil, nil)

	h1 := mustSourceValue(t, checker, task)
	h2 := mustSourceValue(t, checker, task)

	if h1 != h2 {
		t.Errorf("checksum not stable: %s vs %s", h1, h2)
	}
}

func TestSourceRuleChangesInvalidate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "data-a.txt", "same content")
	createFile(t, dir, "data-b.txt", "same content")

	checker := NewChecksumChecker(t.TempDir())

	taskA := makeTask("test", dir,
		[]*ast.Glob{{Glob: "data-a.txt"}},
		nil, nil)
	taskB := makeTask("test", dir,
		[]*ast.Glob{{Glob: "data-b.txt"}},
		nil, nil)

	hashA := mustSourceValue(t, checker, taskA)
	hashB := mustSourceValue(t, checker, taskB)

	if hashA == hashB {
		t.Error("expected different checksums when source glob patterns differ")
	}
}

func TestGenerateRuleChangesInvalidate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "src.txt", "src")

	checker := NewChecksumChecker(t.TempDir())
	sources := []*ast.Glob{{Glob: filepath.Join(dir, "src.txt")}}

	taskA := makeTask("test", dir, sources,
		[]*ast.Glob{{Glob: "output-a.txt"}}, nil)
	taskB := makeTask("test", dir, sources,
		[]*ast.Glob{{Glob: "output-b.txt"}}, nil)

	hashA := mustSourceValue(t, checker, taskA)
	hashB := mustSourceValue(t, checker, taskB)

	if hashA == hashB {
		t.Error("expected different checksums when generate patterns differ")
	}
}
