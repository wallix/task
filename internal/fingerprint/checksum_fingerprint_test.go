package fingerprint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wallix/task/v3/internal/fingerprint"
	"github.com/wallix/task/v3/taskfile/ast"
)

// newTask creates a minimal Task with the given dir, sources and generates.
func newTask(dir string, sources, generates []*ast.Glob) *ast.Task {
	t := &ast.Task{}
	t.Task = "test-task"
	t.Dirs = []string{dir}
	t.Sources = sources
	t.Generates = generates
	return t
}

func TestChecksumChecker_Status_WithFingerprint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmpDir := t.TempDir() // for checksum storage

	// Create source file
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0o644))

	// Create node_modules with marker + actual deps
	nmDir := filepath.Join(dir, "node_modules")
	require.NoError(t, os.MkdirAll(filepath.Join(nmDir, "vite", "bin"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(nmDir, "react"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nmDir, ".yarn-state.yml"), []byte("state"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nmDir, "vite", "bin", "vite.js"), []byte("vite"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nmDir, "react", "index.js"), []byte("react"), 0o644))

	task := newTask(dir,
		[]*ast.Glob{{Glob: "package.json"}},
		[]*ast.Glob{{Glob: "node_modules/**/*", Fingerprint: "node_modules/.yarn-state.yml"}},
	)

	checker := fingerprint.NewChecksumChecker(tmpDir, task)
	st, err := checker.Status()
	require.NoError(t, err)

	// GenerateFiles should only contain the fingerprint file
	assert.Equal(t, []string{
		filepath.Join(dir, "node_modules/.yarn-state.yml"),
	}, st.GenerateFiles)

	// CacheFiles should contain all files matched by the glob + the fingerprint file
	assert.Equal(t, []string{
		filepath.Join(dir, "node_modules/.yarn-state.yml"),
		filepath.Join(dir, "node_modules/react/index.js"),
		filepath.Join(dir, "node_modules/vite/bin/vite.js"),
	}, st.CacheFiles)
}

func TestChecksumChecker_Status_WithoutFingerprint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmpDir := t.TempDir()

	// Create source and build output
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "build"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build", "app"), []byte("binary"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build", "app.map"), []byte("map"), 0o644))

	task := newTask(dir,
		[]*ast.Glob{{Glob: "main.go"}},
		[]*ast.Glob{{Glob: "build/**/*"}},
	)

	checker := fingerprint.NewChecksumChecker(tmpDir, task)
	st, err := checker.Status()
	require.NoError(t, err)

	// Without fingerprint, GenerateFiles and CacheFiles should be identical
	expected := []string{
		filepath.Join(dir, "build/app"),
		filepath.Join(dir, "build/app.map"),
	}
	assert.Equal(t, expected, st.GenerateFiles)
	assert.Equal(t, expected, st.CacheFiles)
}

func TestChecksumChecker_Fingerprint_OnlyHashesMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmpDir := t.TempDir()

	// Create source
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0o644))

	// Create node_modules
	nmDir := filepath.Join(dir, "node_modules")
	require.NoError(t, os.MkdirAll(filepath.Join(nmDir, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nmDir, ".yarn-state.yml"), []byte("state-v1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nmDir, "pkg", "index.js"), []byte("v1"), 0o644))

	task := newTask(dir,
		[]*ast.Glob{{Glob: "package.json"}},
		[]*ast.Glob{{Glob: "node_modules/**/*", Fingerprint: "node_modules/.yarn-state.yml"}},
	)

	// Mark as up to date
	checker := fingerprint.NewChecksumChecker(tmpDir, task)
	require.NoError(t, checker.SetUpToDate())

	// Verify it's up to date
	checker2 := fingerprint.NewChecksumChecker(tmpDir, task)
	upToDate, err := checker2.IsUpToDate()
	require.NoError(t, err)
	assert.True(t, upToDate)

	// Modify a non-marker file — should NOT affect up-to-date status
	require.NoError(t, os.WriteFile(filepath.Join(nmDir, "pkg", "index.js"), []byte("v2-changed"), 0o644))

	checker3 := fingerprint.NewChecksumChecker(tmpDir, task)
	upToDate, err = checker3.IsUpToDate()
	require.NoError(t, err)
	assert.True(t, upToDate, "changing a non-marker file should not invalidate fingerprint")

	// Modify the marker file — SHOULD affect up-to-date status
	require.NoError(t, os.WriteFile(filepath.Join(nmDir, ".yarn-state.yml"), []byte("state-v2"), 0o644))

	checker4 := fingerprint.NewChecksumChecker(tmpDir, task)
	upToDate, err = checker4.IsUpToDate()
	require.NoError(t, err)
	assert.False(t, upToDate, "changing the marker file should invalidate fingerprint")
}
