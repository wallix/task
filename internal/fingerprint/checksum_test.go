package fingerprint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChecksumFiles(t *testing.T) {
	t.Parallel()

	t.Run("regular files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		f1 := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(f1, []byte("hello"), 0o644))
		f2 := filepath.Join(dir, "b.txt")
		require.NoError(t, os.WriteFile(f2, []byte("world"), 0o644))

		hash, err := ChecksumFiles(dir, []string{f1, f2}, nil)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)

		// Same inputs produce the same hash
		hash2, err := ChecksumFiles(dir, []string{f1, f2}, nil)
		require.NoError(t, err)
		assert.Equal(t, hash, hash2)
	})

	t.Run("different content different hash", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		f := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(f, []byte("hello"), 0o644))

		hash1, err := ChecksumFiles(dir, []string{f}, nil)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(f, []byte("changed"), 0o644))
		hash2, err := ChecksumFiles(dir, []string{f}, nil)
		require.NoError(t, err)
		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("relative path hashing", func(t *testing.T) {
		t.Parallel()
		// Same file content in different relative paths should produce different hashes
		dir1 := t.TempDir()
		dir2 := t.TempDir()

		require.NoError(t, os.MkdirAll(filepath.Join(dir1, "sub"), 0o755))
		f1 := filepath.Join(dir1, "sub", "a.txt")
		require.NoError(t, os.WriteFile(f1, []byte("hello"), 0o644))

		f2 := filepath.Join(dir2, "a.txt")
		require.NoError(t, os.WriteFile(f2, []byte("hello"), 0o644))

		hash1, err := ChecksumFiles(dir1, []string{f1}, nil)
		require.NoError(t, err)
		hash2, err := ChecksumFiles(dir2, []string{f2}, nil)
		require.NoError(t, err)
		assert.NotEqual(t, hash1, hash2, "different relative paths should produce different hashes")
	})

	t.Run("rename changes hash", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		f1 := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(f1, []byte("hello"), 0o644))

		hash1, err := ChecksumFiles(dir, []string{f1}, nil)
		require.NoError(t, err)

		f2 := filepath.Join(dir, "b.txt")
		require.NoError(t, os.Rename(f1, f2))

		hash2, err := ChecksumFiles(dir, []string{f2}, nil)
		require.NoError(t, err)
		assert.NotEqual(t, hash1, hash2, "renaming a file should change the hash")
	})

	t.Run("symlinks hash target not content", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "target.txt")
		require.NoError(t, os.WriteFile(target, []byte("data"), 0o644))

		link := filepath.Join(dir, "link.txt")
		require.NoError(t, os.Symlink(target, link))

		hashLink, err := ChecksumFiles(dir, []string{link}, nil)
		require.NoError(t, err)

		hashTarget, err := ChecksumFiles(dir, []string{target}, nil)
		require.NoError(t, err)

		// Symlink hashes the link target string, regular file hashes content — they should differ
		assert.NotEqual(t, hashLink, hashTarget)
	})

	t.Run("symlink retarget changes hash", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		t1 := filepath.Join(dir, "t1.txt")
		t2 := filepath.Join(dir, "t2.txt")
		require.NoError(t, os.WriteFile(t1, []byte("a"), 0o644))
		require.NoError(t, os.WriteFile(t2, []byte("a"), 0o644))

		link := filepath.Join(dir, "link.txt")
		require.NoError(t, os.Symlink(t1, link))
		hash1, err := ChecksumFiles(dir, []string{link}, nil)
		require.NoError(t, err)

		require.NoError(t, os.Remove(link))
		require.NoError(t, os.Symlink(t2, link))
		hash2, err := ChecksumFiles(dir, []string{link}, nil)
		require.NoError(t, err)
		assert.NotEqual(t, hash1, hash2, "changing symlink target should change hash")
	})

	t.Run("data strings", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		hash1, err := ChecksumFiles(dir, nil, []string{"foo", "bar"})
		require.NoError(t, err)
		hash2, err := ChecksumFiles(dir, nil, []string{"foo", "baz"})
		require.NoError(t, err)
		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("missing file returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := ChecksumFiles(dir, []string{filepath.Join(dir, "nope.txt")}, nil)
		assert.Error(t, err)
	})
}
