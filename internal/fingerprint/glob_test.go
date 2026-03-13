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

// setupNodeModules creates a fake node_modules tree for testing:
//
//	dir/
//	  node_modules/
//	    .yarn-state.yml   (dotfile — not matched by **/* shell glob)
//	    vite/
//	      bin/
//	        vite.js
//	    react/
//	      index.js
func setupNodeModules(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"node_modules/.yarn-state.yml":  "yarn state",
		"node_modules/vite/bin/vite.js": "vite binary",
		"node_modules/react/index.js":   "react module",
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	return dir
}

func TestGlobs_SimpleGlob(t *testing.T) {
	t.Parallel()
	dir := setupNodeModules(t)

	globs := []*ast.Glob{
		{Glob: "node_modules/.yarn-state.yml"},
	}
	files, err := fingerprint.Globs(dir, globs)
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(dir, "node_modules/.yarn-state.yml"),
	}, files)
}

func TestGlobs_WithFingerprint_ReturnsOnlyFingerprintFile(t *testing.T) {
	t.Parallel()
	dir := setupNodeModules(t)

	globs := []*ast.Glob{
		{Glob: "node_modules/**/*", Fingerprint: "node_modules/.yarn-state.yml"},
	}
	files, err := fingerprint.Globs(dir, globs)
	require.NoError(t, err)

	// Globs() should return only the fingerprint file, not all of node_modules
	assert.Equal(t, []string{
		filepath.Join(dir, "node_modules/.yarn-state.yml"),
	}, files)
}

func TestGlobs_WithFingerprint_MissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	globs := []*ast.Glob{
		{Glob: "node_modules/**/*", Fingerprint: "node_modules/.yarn-state.yml"},
	}
	files, err := fingerprint.Globs(dir, globs)
	require.NoError(t, err)

	// Fingerprint file doesn't exist, so nothing returned
	assert.Empty(t, files)
}

func TestCacheGlobs_SimpleGlob(t *testing.T) {
	t.Parallel()
	dir := setupNodeModules(t)

	globs := []*ast.Glob{
		{Glob: "node_modules/.yarn-state.yml"},
	}
	files, err := fingerprint.CacheGlobs(dir, globs)
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(dir, "node_modules/.yarn-state.yml"),
	}, files)
}

func TestCacheGlobs_WithFingerprint_ReturnsGlobFilesAndFingerprintFile(t *testing.T) {
	t.Parallel()
	dir := setupNodeModules(t)

	globs := []*ast.Glob{
		{Glob: "node_modules/**/*", Fingerprint: "node_modules/.yarn-state.yml"},
	}
	files, err := fingerprint.CacheGlobs(dir, globs)
	require.NoError(t, err)

	// CacheGlobs() expands the full glob AND includes the fingerprint file
	// (which is a dotfile not matched by shell **/* glob)
	expected := []string{
		filepath.Join(dir, "node_modules/.yarn-state.yml"),
		filepath.Join(dir, "node_modules/react/index.js"),
		filepath.Join(dir, "node_modules/vite/bin/vite.js"),
	}
	assert.Equal(t, expected, files)
}

func TestCacheGlobs_WithExclude(t *testing.T) {
	t.Parallel()
	dir := setupNodeModules(t)

	globs := []*ast.Glob{
		{Glob: "node_modules/**/*", Fingerprint: "node_modules/.yarn-state.yml"},
		{Glob: "node_modules/react/**/*", Negate: true},
	}
	files, err := fingerprint.CacheGlobs(dir, globs)
	require.NoError(t, err)

	expected := []string{
		filepath.Join(dir, "node_modules/.yarn-state.yml"),
		filepath.Join(dir, "node_modules/vite/bin/vite.js"),
	}
	assert.Equal(t, expected, files)
}

func TestGlobs_MixedEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create files
	files := map[string]string{
		"build/app.js":                 "app",
		"build/app.css":                "css",
		"node_modules/.yarn-state.yml": "state",
		"node_modules/pkg/index.js":    "pkg",
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	globs := []*ast.Glob{
		{Glob: "build/**/*"},
		{Glob: "node_modules/**/*", Fingerprint: "node_modules/.yarn-state.yml"},
	}

	// Globs: build files + fingerprint file only
	fingerprintFiles, err := fingerprint.Globs(dir, globs)
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(dir, "build/app.css"),
		filepath.Join(dir, "build/app.js"),
		filepath.Join(dir, "node_modules/.yarn-state.yml"),
	}, fingerprintFiles)

	// CacheGlobs: build files + all node_modules files (including fingerprint dotfile)
	cacheFiles, err := fingerprint.CacheGlobs(dir, globs)
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(dir, "build/app.css"),
		filepath.Join(dir, "build/app.js"),
		filepath.Join(dir, "node_modules/.yarn-state.yml"),
		filepath.Join(dir, "node_modules/pkg/index.js"),
	}, cacheFiles)
}
