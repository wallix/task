package fingerprint

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/go-task/task/v3/taskfile/ast"
)

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
