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
		RawCmds:   cmds,
	}
}

func mustSourceValue(t *testing.T, checker *ChecksumChecker) string {
	t.Helper()
	v := checker.SourceValue()
	require.NotEmpty(t, v)
	return v
}

func TestRelGlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dir      string
		glob     string
		expected string
	}{
		{"relative stays relative", "/work", "src/*.go", "src/*.go"},
		{"absolute under dir is relativized", "/work", "/work/src/*.go", "src/*.go"},
		{"absolute outside dir uses dotdot", "/work/tasks", "/work/src/*.go", "../src/*.go"},
		{"nested absolute", "/builds/abc/git/wab", "/builds/abc/git/wab/src/**/*.po", "src/**/*.po"},
		{"sibling directory", "/builds/abc/git/wab/tasks", "/builds/abc/git/wab/src/**/*.po", "../src/**/*.po"},
		{"dir itself", "/work", "/work", "."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, relGlob(tt.dir, tt.glob))
		})
	}
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

	tempDir := t.TempDir()

	t.Run("file sources become globs and srcrule data", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			Sources: []*ast.Glob{
				{Glob: "*.go"},
				{Glob: "*.txt", Negate: true},
			},
		}
		checker := NewChecksumChecker(tempDir, task)
		assert.Len(t, checker.sourcesGlobs, 2)
		assert.Equal(t, "*.go", checker.sourcesGlobs[0].Glob)
		assert.Contains(t, checker.srcData, "srcrule:*.go")
		assert.Contains(t, checker.srcData, "srcrule:!*.txt")
	})

	t.Run("value sources go to data only", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			Sources: []*ast.Glob{
				{Glob: "value:myval"},
				{Glob: "src.go"},
			},
		}
		checker := NewChecksumChecker(tempDir, task)
		assert.Len(t, checker.sourcesGlobs, 1)
		assert.Equal(t, "src.go", checker.sourcesGlobs[0].Glob)
		assert.Contains(t, checker.srcData, "value:myval")
		assert.Contains(t, checker.srcData, "srcrule:src.go")
	})

	t.Run("commands are serialized", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			RawCmds: []*ast.Cmd{
				{Cmd: "echo hello"},
				{Cmd: "go build"},
			},
		}
		checker := NewChecksumChecker(tempDir, task)
		assert.Contains(t, checker.srcData, "cmd[0]:echo hello")
		assert.Contains(t, checker.srcData, "cmd[1]:go build")
	})

	t.Run("generates become genrule data", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			Generates: []*ast.Glob{
				{Glob: "out/*.bin"},
				{Glob: "tmp/*", Negate: true},
			},
		}
		checker := NewChecksumChecker(tempDir, task)
		assert.Contains(t, checker.srcData, "genrule:out/*.bin")
		assert.Contains(t, checker.srcData, "genrule:!tmp/*")
	})

	t.Run("absolute source paths are relativized", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		task := &ast.Task{
			Dirs: []string{dir},
			Sources: []*ast.Glob{
				{Glob: filepath.Join(dir, "src/**/*.go")},
			},
			Generates: []*ast.Glob{
				{Glob: filepath.Join(dir, "build/**/*")},
			},
		}
		checker := NewChecksumChecker(tempDir, task)
		assert.Contains(t, checker.srcData, "srcrule:src/**/*.go",
			"absolute source path should be relativized to task dir")
		assert.Contains(t, checker.srcData, "genrule:build/**/*",
			"absolute generates path should be relativized to task dir")
		assert.NotContains(t, checker.srcData, "srcrule:"+filepath.Join(dir, "src/**/*.go"),
			"absolute path should not appear in data")
	})

	t.Run("data is sorted", func(t *testing.T) {
		t.Parallel()
		task := &ast.Task{
			Sources: []*ast.Glob{
				{Glob: "z.go"},
				{Glob: "a.go"},
			},
			RawCmds: []*ast.Cmd{
				{Cmd: "make"},
			},
			Generates: []*ast.Glob{
				{Glob: "out.bin"},
			},
		}
		checker := NewChecksumChecker(tempDir, task)
		for i := 1; i < len(checker.srcData); i++ {
			assert.LessOrEqual(t, checker.srcData[i-1], checker.srcData[i], "data should be sorted")
		}
	})
}

func TestSourceValueReturnsSourcesOnly(t *testing.T) {
	t.Parallel()

	task := &ast.Task{
		Task: "test-value",
		Sources: []*ast.Glob{
			{Glob: "value:hello"},
		},
		Generates: []*ast.Glob{
			{Glob: "value:world"},
		},
	}

	checker := NewChecksumChecker(t.TempDir(), task)
	hash := checker.SourceValue()
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

	tempDir := t.TempDir()

	taskA := makeTask("test", dirA,
		[]*ast.Glob{{Glob: "hello.txt"}},
		nil, nil)
	taskB := makeTask("test", dirB,
		[]*ast.Glob{{Glob: "hello.txt"}},
		nil, nil)

	hashA := mustSourceValue(t, NewChecksumChecker(tempDir, taskA))
	hashB := mustSourceValue(t, NewChecksumChecker(tempDir, taskB))

	if hashA != hashB {
		t.Errorf("expected same checksum for identical relative layout, got %s vs %s", hashA, hashB)
	}
}

// TestAbsolutePathStabilityAcrossWorkspaces simulates two CI runners with
// different workspace paths but identical file contents. Sources and generates
// use absolute paths (as happens when {{ .ROOTDIR }} is resolved). The
// checksums must be identical despite the different base directories.
func TestAbsolutePathStabilityAcrossWorkspaces(t *testing.T) {
	t.Parallel()

	// Simulate two CI runners with different workspace paths
	runnerA := t.TempDir() // e.g. /builds/abc123/0/git/wab
	runnerB := t.TempDir() // e.g. /builds/def456/0/git/wab

	// Create identical file trees
	for _, dir := range []string{runnerA, runnerB} {
		createFile(t, dir, "src/main.go", "package main")
		createFile(t, dir, "src/util.go", "package main")
		createFile(t, dir, "build/app", "binary")
	}

	tempDir := t.TempDir()

	// Tasks use absolute paths (as resolved from {{ .ROOTDIR }}/src/**/*.go)
	taskA := makeTask("compile", runnerA,
		[]*ast.Glob{{Glob: filepath.Join(runnerA, "src/**/*.go")}},
		[]*ast.Glob{{Glob: filepath.Join(runnerA, "build/**/*")}},
		[]*ast.Cmd{{Cmd: "go build -o build/app ."}},
	)
	taskB := makeTask("compile", runnerB,
		[]*ast.Glob{{Glob: filepath.Join(runnerB, "src/**/*.go")}},
		[]*ast.Glob{{Glob: filepath.Join(runnerB, "build/**/*")}},
		[]*ast.Cmd{{Cmd: "go build -o build/app ."}},
	)

	hashA := mustSourceValue(t, NewChecksumChecker(tempDir, taskA))
	hashB := mustSourceValue(t, NewChecksumChecker(tempDir, taskB))

	assert.Equal(t, hashA, hashB,
		"checksums must be identical across workspaces with same relative layout")
}

// TestAbsolutePathStabilityWithSubdir tests that a task whose ComputeDir
// is a subdirectory (e.g. tasks/) but whose sources use ROOTDIR-prefixed
// absolute paths (../src/...) produces stable checksums across workspaces.
func TestAbsolutePathStabilityWithSubdir(t *testing.T) {
	t.Parallel()

	runnerA := t.TempDir()
	runnerB := t.TempDir()

	for _, root := range []string{runnerA, runnerB} {
		createFile(t, root, "src/notifier/locale/en/notifier.po", "msgid \"hello\"")
		// Create the tasks/ subdir as the task's working directory
		require.NoError(t, os.MkdirAll(filepath.Join(root, "tasks"), 0o755))
	}

	tempDir := t.TempDir()

	// Task dir is root/tasks/ but sources reference root/src/ via absolute path
	taskA := makeTask("compile:notifier", filepath.Join(runnerA, "tasks"),
		[]*ast.Glob{{Glob: filepath.Join(runnerA, "src/notifier/locale/*/notifier.po")}},
		[]*ast.Glob{{Glob: filepath.Join(runnerA, "src/notifier/locale/*/notifier.mo")}},
		[]*ast.Cmd{{Cmd: "msgfmt -vv {{.ITEM}}"}},
	)
	taskB := makeTask("compile:notifier", filepath.Join(runnerB, "tasks"),
		[]*ast.Glob{{Glob: filepath.Join(runnerB, "src/notifier/locale/*/notifier.po")}},
		[]*ast.Glob{{Glob: filepath.Join(runnerB, "src/notifier/locale/*/notifier.mo")}},
		[]*ast.Cmd{{Cmd: "msgfmt -vv {{.ITEM}}"}},
	)

	hashA := mustSourceValue(t, NewChecksumChecker(tempDir, taskA))
	hashB := mustSourceValue(t, NewChecksumChecker(tempDir, taskB))

	assert.Equal(t, hashA, hashB,
		"checksums must be identical even when task dir is a subdirectory and sources use parent paths")
}

// TestAbsolutePathStabilityWithMixedSources tests that a task with both
// relative and absolute source paths produces stable checksums across
// different workspace roots.
func TestAbsolutePathStabilityWithMixedSources(t *testing.T) {
	t.Parallel()

	runnerA := t.TempDir()
	runnerB := t.TempDir()

	for _, dir := range []string{runnerA, runnerB} {
		createFile(t, dir, "package.json", `{"name":"test"}`)
		createFile(t, dir, "src/notifier/locale/en/notifier.po", "msgid \"hello\"")
	}

	tempDir := t.TempDir()

	// Mix of relative and absolute paths (as happens in real Taskfiles)
	taskA := makeTask("compile:notifier", runnerA,
		[]*ast.Glob{{Glob: filepath.Join(runnerA, "src/notifier/locale/*/notifier.po")}},
		[]*ast.Glob{{Glob: filepath.Join(runnerA, "src/notifier/locale/*/notifier.mo")}},
		[]*ast.Cmd{{Cmd: "msgfmt -vv {{.ITEM}}"}},
	)
	taskB := makeTask("compile:notifier", runnerB,
		[]*ast.Glob{{Glob: filepath.Join(runnerB, "src/notifier/locale/*/notifier.po")}},
		[]*ast.Glob{{Glob: filepath.Join(runnerB, "src/notifier/locale/*/notifier.mo")}},
		[]*ast.Cmd{{Cmd: "msgfmt -vv {{.ITEM}}"}},
	)

	hashA := mustSourceValue(t, NewChecksumChecker(tempDir, taskA))
	hashB := mustSourceValue(t, NewChecksumChecker(tempDir, taskB))

	assert.Equal(t, hashA, hashB,
		"checksums must be identical for same relative layout with absolute source paths")
}

func TestCommandStringInclusion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "src.txt", "content")

	tempDir := t.TempDir()
	sources := []*ast.Glob{{Glob: filepath.Join(dir, "src.txt")}}

	taskNoCmds := makeTask("test", dir, sources, nil, nil)
	taskWithCmds := makeTask("test", dir, sources, nil,
		[]*ast.Cmd{{Cmd: "echo hello"}})

	hashNoCmds := mustSourceValue(t, NewChecksumChecker(tempDir, taskNoCmds))
	hashWithCmds := mustSourceValue(t, NewChecksumChecker(tempDir, taskWithCmds))

	if hashNoCmds == hashWithCmds {
		t.Error("expected checksum to change when commands are added")
	}

	taskDiffCmd := makeTask("test", dir, sources, nil,
		[]*ast.Cmd{{Cmd: "echo goodbye"}})
	hashDiffCmd := mustSourceValue(t, NewChecksumChecker(tempDir, taskDiffCmd))

	if hashWithCmds == hashDiffCmd {
		t.Error("expected checksum to change when command string changes")
	}
}

func TestChecksumStability(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "a.txt", "aaa")
	createFile(t, dir, "b.txt", "bbb")

	tempDir := t.TempDir()
	sources := []*ast.Glob{
		{Glob: filepath.Join(dir, "a.txt")},
		{Glob: filepath.Join(dir, "b.txt")},
	}
	task := makeTask("stable", dir, sources, nil, nil)

	h1 := mustSourceValue(t, NewChecksumChecker(tempDir, task))
	h2 := mustSourceValue(t, NewChecksumChecker(tempDir, task))

	if h1 != h2 {
		t.Errorf("checksum not stable: %s vs %s", h1, h2)
	}
}

func TestSourceRuleChangesInvalidate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "data-a.txt", "same content")
	createFile(t, dir, "data-b.txt", "same content")

	tempDir := t.TempDir()

	taskA := makeTask("test", dir,
		[]*ast.Glob{{Glob: "data-a.txt"}},
		nil, nil)
	taskB := makeTask("test", dir,
		[]*ast.Glob{{Glob: "data-b.txt"}},
		nil, nil)

	hashA := mustSourceValue(t, NewChecksumChecker(tempDir, taskA))
	hashB := mustSourceValue(t, NewChecksumChecker(tempDir, taskB))

	if hashA == hashB {
		t.Error("expected different checksums when source glob patterns differ")
	}
}

func TestGenerateRuleChangesInvalidate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "src.txt", "src")

	tempDir := t.TempDir()
	sources := []*ast.Glob{{Glob: filepath.Join(dir, "src.txt")}}

	taskA := makeTask("test", dir, sources,
		[]*ast.Glob{{Glob: "output-a.txt"}}, nil)
	taskB := makeTask("test", dir, sources,
		[]*ast.Glob{{Glob: "output-b.txt"}}, nil)

	hashA := mustSourceValue(t, NewChecksumChecker(tempDir, taskA))
	hashB := mustSourceValue(t, NewChecksumChecker(tempDir, taskB))

	if hashA == hashB {
		t.Error("expected different checksums when generate patterns differ")
	}
}

// TestSourceValueUsesPrecomputedHash verifies that SourceValue returns
// the precomputed t.SourceHash and that a checker with different commands
// (raw vs resolved) uses whichever cmds are on the task at construction.
func TestSourceValueUsesPrecomputedHash(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "src.txt", "content")

	sources := []*ast.Glob{{Glob: filepath.Join(dir, "src.txt")}}

	// Build a task with raw cmds and compute its hash
	rawTask := makeTask("test", dir, sources, nil, []*ast.Cmd{{Cmd: "echo {{.FOO}}"}})
	rawChecker := NewChecksumChecker(t.TempDir(), rawTask)
	rawHash := rawChecker.SourceValue()
	assert.NotEmpty(t, rawHash)

	// Build a task with resolved cmds — different hash
	resolvedTask := makeTask("test", dir, sources, nil, []*ast.Cmd{{Cmd: "echo bar"}})
	resolvedChecker := NewChecksumChecker(t.TempDir(), resolvedTask)
	resolvedHash := resolvedChecker.SourceValue()

	assert.NotEqual(t, rawHash, resolvedHash,
		"raw and resolved commands should produce different hashes")

	// Precomputed hash on task should be returned without recomputation
	resolvedTask.SourceHash = rawHash
	precomputedChecker := NewChecksumChecker(t.TempDir(), resolvedTask)
	assert.Equal(t, rawHash, precomputedChecker.SourceValue(),
		"SourceValue should return precomputed SourceHash")
}

// TestConstructorDoesNotAccessDisk verifies that NewChecksumChecker does not
// read from disk when t.SourceHash is already set. It uses a non-existent
// directory — if the constructor tried to glob or hash files, it would fail
// or produce a different hash.
func TestConstructorDoesNotAccessDisk(t *testing.T) {
	t.Parallel()

	precomputed := "abc123"
	task := &ast.Task{
		Task: "no-disk",
		Dirs: []string{"/non/existent/directory"},
		Sources: []*ast.Glob{
			{Glob: "*.go"},
		},
		RawCmds: []*ast.Cmd{
			{Cmd: "echo build"},
		},
		SourceHash: precomputed,
	}

	checker := NewChecksumChecker(t.TempDir(), task)

	// SourceValue must return the precomputed hash without touching disk.
	assert.Equal(t, precomputed, checker.SourceValue(),
		"SourceValue should return t.SourceHash without disk access")
}

// TestSourcesChangedDetectsDrift verifies that SourcesChanged detects
// files modified between IsUpToDate and a later check.
func TestSourcesChangedDetectsDrift(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tempDir := t.TempDir()
	srcPath := filepath.Join(dir, "src.txt")
	createFile(t, dir, "src.txt", "original")

	task := makeTask("drift", dir,
		[]*ast.Glob{{Glob: srcPath}},
		nil, nil,
	)

	checker := NewChecksumChecker(tempDir, task)

	// IsUpToDate snapshots disk state
	_, err := checker.IsUpToDate()
	require.NoError(t, err)

	// No changes yet
	changed, err := checker.SourcesChanged()
	require.NoError(t, err)
	assert.False(t, changed, "sources should not have changed yet")

	// Modify the source file
	require.NoError(t, os.WriteFile(srcPath, []byte("modified"), 0o644))

	// Now drift should be detected
	changed, err = checker.SourcesChanged()
	require.NoError(t, err)
	assert.True(t, changed, "sources should be detected as changed after modification")
}

// TestSourceValueLazyComputesWhenNoPrecomputed verifies that SourceValue
// computes from disk when t.SourceHash is empty (the compilation-time path).
func TestSourceValueLazyComputesWhenNoPrecomputed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFile(t, dir, "src.go", "package main")

	task := makeTask("lazy", dir,
		[]*ast.Glob{{Glob: filepath.Join(dir, "src.go")}},
		nil,
		[]*ast.Cmd{{Cmd: "go build"}},
	)
	// SourceHash deliberately left empty (zero value)

	checker := NewChecksumChecker(t.TempDir(), task)
	hash := checker.SourceValue()

	assert.NotEmpty(t, hash, "SourceValue should lazily compute when SourceHash is empty")

	// Second call should return the cached value
	assert.Equal(t, hash, checker.SourceValue(), "SourceValue should be stable across calls")
}
