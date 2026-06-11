package ocicas

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// memStore is an in-memory chunk store for tests.
type memStore map[string][]byte

func (m memStore) sink(digest string, compressed []byte) error {
	m[digest] = bytes.Clone(compressed)
	return nil
}

func (m memStore) source(digest string) ([]byte, error) {
	c, ok := m[digest]
	if !ok {
		return nil, os.ErrNotExist
	}
	return c, nil
}

func randomBytes(t *testing.T, n int, seed uint64) []byte {
	t.Helper()
	r := rand.NewChaCha8([32]byte{byte(seed), byte(seed >> 8)})
	data := make([]byte, n)
	r.Read(data)
	return data
}

func chunkAll(t *testing.T, data []byte) []ChunkRef {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Build(dir, []string{"f"}, memStore{}.sink)
	if err != nil {
		t.Fatal(err)
	}
	return idx.Chunks
}

func TestChunkBoundsAndDeterminism(t *testing.T) {
	data := randomBytes(t, 10<<20, 1)
	chunks := chunkAll(t, data)
	if len(chunks) < 5 {
		t.Fatalf("expected several chunks for 10MiB, got %d", len(chunks))
	}
	var total int64
	for i, c := range chunks {
		if c.RawSize > MaxChunkSize {
			t.Errorf("chunk %d exceeds max: %d", i, c.RawSize)
		}
		if i < len(chunks)-1 && c.RawSize < MinChunkSize {
			t.Errorf("non-final chunk %d below min: %d", i, c.RawSize)
		}
		total += c.RawSize
	}
	if total != int64(len(data)) {
		t.Fatalf("chunks cover %d bytes, want %d", total, len(data))
	}
	again := chunkAll(t, data)
	for i := range chunks {
		if chunks[i] != again[i] {
			t.Fatalf("chunking is not deterministic at chunk %d", i)
		}
	}
}

// TestInsertionResync is the content-defined property: inserting bytes near
// the start must leave most downstream chunk digests unchanged.
func TestInsertionResync(t *testing.T) {
	data := randomBytes(t, 8<<20, 2)
	mutated := append(append(bytes.Clone(data[:100]), randomBytes(t, 100, 3)...), data[100:]...)

	digests := func(chunks []ChunkRef) map[string]bool {
		m := make(map[string]bool)
		for _, c := range chunks {
			m[c.Digest] = true
		}
		return m
	}
	a, b := digests(chunkAll(t, data)), digests(chunkAll(t, mutated))
	shared := 0
	for d := range b {
		if a[d] {
			shared++
		}
	}
	if ratio := float64(shared) / float64(len(b)); ratio < 0.7 {
		t.Fatalf("only %.0f%% of chunks shared after a 100-byte insertion", ratio*100)
	}
}

// TestZeroRuns: long zero runs (sparse images) must collapse to a handful
// of distinct chunk digests.
func TestZeroRuns(t *testing.T) {
	chunks := chunkAll(t, make([]byte, 64<<20))
	uniq := make(map[string]bool)
	for _, c := range chunks {
		uniq[c.Digest] = true
	}
	if len(uniq) > 2 {
		t.Fatalf("64MiB of zeros produced %d distinct chunks, want <=2", len(uniq))
	}
}

func TestRoundTrip(t *testing.T) {
	src := t.TempDir()
	files := map[string][]byte{
		"big.bin":       randomBytes(t, 3<<20, 4),
		"empty":         {},
		"sub/dir/small": []byte("hello"),
		"sub/exec":      []byte("#!/bin/sh\n"),
	}
	for p, content := range files {
		full := filepath.Join(src, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if p == "sub/exec" {
			mode = 0o755
		}
		if err := os.WriteFile(full, content, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("dir/small", filepath.Join(src, "sub/link")); err != nil {
		t.Fatal(err)
	}

	store := memStore{}
	paths := []string{"big.bin", "empty", "sub/dir/small", "sub/exec", "sub/link"}
	idx, err := Build(src, paths, store.sink)
	if err != nil {
		t.Fatal(err)
	}

	// serialize/parse round-trip, as the registry path will do
	raw, err := idx.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	idx, err = UnmarshalIndex(raw)
	if err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	if err := Assemble(idx, dst, store.source); err != nil {
		t.Fatal(err)
	}

	for p, want := range files {
		got, err := os.ReadFile(filepath.Join(dst, p))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: content mismatch", p)
		}
	}
	st, err := os.Stat(filepath.Join(dst, "sub/exec"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("exec mode lost: %v", st.Mode())
	}
	target, err := os.Readlink(filepath.Join(dst, "sub/link"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "dir/small" {
		t.Errorf("symlink target %q", target)
	}
}

func TestAssembleRejectsUnsafePath(t *testing.T) {
	idx := newIndex()
	idx.Files = []FileEntry{{Path: "../escape", Mode: 0o644}}
	if err := Assemble(idx, t.TempDir(), memStore{}.source); err == nil {
		t.Fatal("expected an error for a ../ path")
	}
	raw, _ := (&Index{Version: IndexVersion, Chunker: newIndex().Chunker,
		Compression: newIndex().Compression,
		Files:       []FileEntry{{Path: "/abs", Mode: 0o644}}}).Marshal()
	if _, err := UnmarshalIndex(raw); err == nil {
		t.Fatal("expected an error for an absolute path")
	}
}

func TestAssembleDetectsCorruption(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), randomBytes(t, 1<<20, 5), 0o644); err != nil {
		t.Fatal(err)
	}
	store := memStore{}
	idx, err := Build(src, []string{"f"}, store.sink)
	if err != nil {
		t.Fatal(err)
	}
	for d := range store {
		store[d][0] ^= 0xff // flip one byte
	}
	if err := Assemble(idx, t.TempDir(), store.source); err == nil {
		t.Fatal("expected a digest mismatch error")
	}
}

func TestBuildRejectsUnsafePath(t *testing.T) {
	if _, err := Build(t.TempDir(), []string{"../x"}, memStore{}.sink); err == nil {
		t.Fatal("expected an error for a ../ path")
	}
}

// TestSmallFilesPack: thousands of small files must collapse into few
// stream chunks — the node_modules case that motivated format v2.
func TestSmallFilesPack(t *testing.T) {
	src := t.TempDir()
	var paths []string
	for i := range 3000 {
		p := filepath.Join("mod", string(rune('a'+i%26)), fmt.Sprintf("f%04d", i))
		full := filepath.Join(src, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, randomBytes(t, 2048, uint64(i)), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, filepath.ToSlash(p))
	}
	store := memStore{}
	idx, err := Build(src, paths, store.sink)
	if err != nil {
		t.Fatal(err)
	}
	// ~6MB of content: a handful of chunks, NOT 3000
	if len(idx.Chunks) > 30 {
		t.Fatalf("3000 small files produced %d chunks", len(idx.Chunks))
	}
	dst := t.TempDir()
	if err := Assemble(idx, dst, store.source); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{paths[0], paths[1500], paths[2999]} {
		a, _ := os.ReadFile(filepath.Join(src, p))
		b, err := os.ReadFile(filepath.Join(dst, p))
		if err != nil || !bytes.Equal(a, b) {
			t.Fatalf("%s: mismatch after pack round-trip (%v)", p, err)
		}
	}
}

// TestStreamResyncOnFileChange: changing ONE small file in a big tree must
// leave most stream chunks shared (CDC resync across the concatenation).
func TestStreamResyncOnFileChange(t *testing.T) {
	build := func(victim []byte) map[string]bool {
		src := t.TempDir()
		var paths []string
		for i := range 2000 {
			p := fmt.Sprintf("m/f%04d", i)
			full := filepath.Join(src, p)
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			content := randomBytes(t, 3000, uint64(1000+i))
			if i == 1000 {
				content = victim
			}
			if err := os.WriteFile(full, content, 0o644); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, p)
		}
		idx, err := Build(src, paths, memStore{}.sink)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, c := range idx.Chunks {
			m[c.Digest] = true
		}
		return m
	}
	a := build(randomBytes(t, 3000, 7777))
	b := build(randomBytes(t, 5000, 8888)) // different content AND size: stream shifts
	shared := 0
	for d := range b {
		if a[d] {
			shared++
		}
	}
	if ratio := float64(shared) / float64(len(b)); ratio < 0.6 {
		t.Fatalf("only %.0f%% of stream chunks shared after one file changed", ratio*100)
	}
}

// TestAssembleReplacesStaleEntries: restoring over a previous restore must
// replace entries whose type changed — in particular a regular file must
// never be written through a stale symlink — and re-enforce file modes.
func TestAssembleReplacesStaleEntries(t *testing.T) {
	src := t.TempDir()
	files := map[string][]byte{
		"was-link": []byte("now a file"),
		"was-file": []byte("kept a file"),
	}
	writeTree(t, src, files)
	if err := os.Symlink("was-file", filepath.Join(src, "now-link")); err != nil {
		t.Fatal(err)
	}
	store := memStore{}
	idx, err := Build(src, []string{"now-link", "was-file", "was-link"}, store.sink)
	if err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	// stale tree: was-link is a symlink to victim, now-link a plain file,
	// was-file has the wrong mode
	if err := os.WriteFile(filepath.Join(dst, "victim"), []byte("victim"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("victim", filepath.Join(dst, "was-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "now-link"), []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "was-file"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Assemble(idx, dst, store.source); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "victim")); !bytes.Equal(got, []byte("victim")) {
		t.Errorf("stale symlink was written through: victim = %q", got)
	}
	if fi, err := os.Lstat(filepath.Join(dst, "was-link")); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("was-link is not a regular file: %v", fi.Mode())
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "was-link")); !bytes.Equal(got, files["was-link"]) {
		t.Errorf("was-link content %q", got)
	}
	if target, err := os.Readlink(filepath.Join(dst, "now-link")); err != nil || target != "was-file" {
		t.Errorf("now-link is not the expected symlink: %q, %v", target, err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "was-file")); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("was-file mode not enforced: %v", fi.Mode())
	}
}

// TestAssembleRejectsSymlinkTraversal: a malicious index must not write
// through a symlink of the set pointing outside the restore root — neither
// directly nor by redirecting a directory used by an earlier entry.
func TestAssembleRejectsSymlinkTraversal(t *testing.T) {
	store := memStore{}
	content := []byte("pwned")
	compressed := zstdEnc.EncodeAll(content, nil)
	sum := sha256.Sum256(compressed)
	d := "sha256:" + hex.EncodeToString(sum[:])
	store[d] = compressed
	chunk := ChunkRef{Digest: d, Size: int64(len(compressed)), RawSize: int64(len(content))}

	cases := []struct {
		name  string
		files func(outside string) []FileEntry
	}{
		{"direct", func(outside string) []FileEntry {
			return []FileEntry{
				{Path: "exit", Mode: 0o777, Symlink: outside},
				{Path: "exit/pwned", Mode: 0o644, Size: int64(len(content))},
			}
		}},
		// the directory is legitimately used first, then redirected
		{"redirected dir", func(outside string) []FileEntry {
			return []FileEntry{
				{Path: "exit/ok", Mode: 0o644, Size: int64(len(content))},
				{Path: "exit", Mode: 0o777, Symlink: outside},
				{Path: "exit/pwned", Mode: 0o644, Size: 0},
			}
		}},
	}
	for _, c := range cases {
		outside := t.TempDir()
		idx := newIndex()
		idx.Files = c.files(outside)
		idx.Chunks = []ChunkRef{chunk}
		if err := Assemble(idx, t.TempDir(), store.source); err == nil {
			t.Errorf("%s: expected a traversal rejection", c.name)
		}
		if _, err := os.Lstat(filepath.Join(outside, "pwned")); !os.IsNotExist(err) {
			t.Errorf("%s: restore escaped the root", c.name)
		}
	}
}

// TestIndexRejectsForeignFormat: entries written with other parameters must
// be treated as unreadable (the caller turns that into a cache miss).
func TestIndexRejectsForeignFormat(t *testing.T) {
	corrupt := []struct {
		name   string
		mutate func(*Index)
	}{
		{"version", func(idx *Index) { idx.Version = 1 }},
		{"chunker", func(idx *Index) { idx.Chunker.Avg = AvgChunkSize / 2 }},
		{"compression", func(idx *Index) { idx.Compression.Algo = "gzip" }},
		{"oversized chunk", func(idx *Index) {
			idx.Files = []FileEntry{{Path: "f", Mode: 0o644, Size: 2 * MaxChunkSize}}
			idx.Chunks = []ChunkRef{{Digest: "sha256:00", Size: 1, RawSize: 2 * MaxChunkSize}}
		}},
	}
	for _, c := range corrupt {
		idx := newIndex()
		c.mutate(idx)
		raw, err := idx.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := UnmarshalIndex(raw); err == nil {
			t.Errorf("%s: expected a rejection", c.name)
		}
	}
}

// TestBuildDeduplicatesSinkCalls: repeated chunks are emitted once.
func TestBuildDeduplicatesSinkCalls(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "zeros"), make([]byte, 16<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	idx, err := Build(src, []string{"zeros"}, func(string, []byte) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Chunks) < 4 {
		t.Fatalf("expected several chunks, got %d", len(idx.Chunks))
	}
	if calls > 2 {
		t.Fatalf("identical chunks hit the sink %d times", calls)
	}
}

// TestIndexRejectsInconsistentStream: chunk list shorter than the files.
func TestIndexRejectsInconsistentStream(t *testing.T) {
	idx := newIndex()
	idx.Files = []FileEntry{{Path: "f", Mode: 0o644, Size: 100}}
	raw, err := idx.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalIndex(raw); err == nil {
		t.Fatal("expected a stream/files inconsistency error")
	}
}
