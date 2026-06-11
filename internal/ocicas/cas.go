package ocicas

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// zstdLevel is part of the format in the weak sense: a different level (or
// library version) yields different compressed bytes, which only costs
// deduplication misses against older entries, never correctness.
const zstdLevel = 3 // zstd.SpeedDefault

var (
	zstdEnc, _ = zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		// single goroutine + single segment: deterministic output
		zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(MaxChunkSize))
	// maxIndexSize caps DecodeAll output: a zstd bomb fails instead of
	// allocating (chunks are far smaller, the index is the largest input)
	zstdDec, _ = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(maxIndexSize))
)

// Sink stores one compressed chunk. Build calls it once per distinct digest
// (duplicates within the stream are already deduplicated client-side).
type Sink func(digest string, compressed []byte) error

// Source returns the compressed bytes of a chunk; Assemble verifies the
// digest and the decompressed size, so the source can be a dumb fetch.
type Source func(digest string) ([]byte, error)

// Build concatenates the given files (slash-separated paths relative to
// baseDir, duplicates ignored) into the content stream, chunks it, and
// emits the compressed chunks through sink. The returned index lists files
// sorted by path, so the stream and the serialization are deterministic for
// a given tree.
func Build(baseDir string, paths []string, sink Sink) (*Index, error) {
	idx := newIndex()

	uniq := slices.Clone(paths)
	slices.Sort(uniq)
	uniq = slices.Compact(uniq)

	st := &streamChunker{sink: sink, seen: make(map[string]bool)}
	for _, p := range uniq {
		if !safeRelPath(p) {
			return nil, fmt.Errorf("ocicas: unsafe path %q", p)
		}
		full := filepath.Join(baseDir, filepath.FromSlash(p))
		fi, err := os.Lstat(full)
		if err != nil {
			return nil, fmt.Errorf("ocicas: %w", err)
		}
		entry := FileEntry{Path: p, Mode: uint32(fi.Mode().Perm())}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				return nil, fmt.Errorf("ocicas: %w", err)
			}
			entry.Symlink = target
		case fi.Mode().IsRegular():
			n, err := st.addFile(full)
			if err != nil {
				return nil, err
			}
			// the size actually streamed wins over Lstat: the file may
			// still be changing, the index must match the stream
			entry.Size = n
		default:
			return nil, fmt.Errorf("ocicas: %q is neither a regular file nor a symlink", p)
		}
		idx.Files = append(idx.Files, entry)
	}
	if err := st.finish(); err != nil {
		return nil, err
	}
	idx.Chunks = st.chunks
	return idx, nil
}

// streamChunker feeds file contents through the CDC cutter, compressing and
// emitting each chunk as soon as its boundary is known.
type streamChunker struct {
	sink   Sink
	seen   map[string]bool
	buf    []byte
	chunks []ChunkRef
}

func (st *streamChunker) addFile(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("ocicas: %w", err)
	}
	defer f.Close()

	var total int64
	if st.buf == nil {
		st.buf = make([]byte, 0, 2*MaxChunkSize)
	}
	for {
		if len(st.buf) >= 2*MaxChunkSize {
			if err := st.drain(false); err != nil {
				return 0, err
			}
		}
		n, err := f.Read(st.buf[len(st.buf) : 2*MaxChunkSize])
		st.buf = st.buf[:len(st.buf)+n]
		total += int64(n)
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return 0, fmt.Errorf("ocicas: reading %s: %w", path, err)
		}
	}
}

// drain cuts and emits chunks from the buffer: every boundary the cutter
// can decide on a full window when final is false, everything when true.
func (st *streamChunker) drain(final bool) error {
	for {
		if len(st.buf) == 0 || (!final && len(st.buf) < MaxChunkSize) {
			return nil
		}
		n := cut(st.buf[:min(len(st.buf), MaxChunkSize)])
		if err := st.emit(st.buf[:n]); err != nil {
			return err
		}
		st.buf = st.buf[:copy(st.buf, st.buf[n:])]
	}
}

func (st *streamChunker) finish() error { return st.drain(true) }

func (st *streamChunker) emit(raw []byte) error {
	compressed := zstdEnc.EncodeAll(raw, nil)
	sum := sha256.Sum256(compressed)
	ref := ChunkRef{
		Digest:  "sha256:" + hex.EncodeToString(sum[:]),
		Size:    int64(len(compressed)),
		RawSize: int64(len(raw)),
	}
	st.chunks = append(st.chunks, ref)
	if !st.seen[ref.Digest] {
		st.seen[ref.Digest] = true
		return st.sink(ref.Digest, compressed)
	}
	return nil
}

// Assemble restores the file set under baseDir, fetching the stream chunks
// through src. Every chunk is digest-verified and its decompressed size
// checked, so a corrupt or substituted blob fails the restore instead of
// landing on disk. Existing files are overwritten.
func Assemble(idx *Index, baseDir string, src Source) error {
	stream := &streamReader{idx: idx, src: src}
	for _, entry := range idx.Files {
		if !safeRelPath(entry.Path) {
			return fmt.Errorf("ocicas: unsafe path %q in index", entry.Path)
		}
		// safeRelPath rejects "..", but a symlink entry of the set can still
		// point outside; a later entry written under it would escape the
		// root. Reject any write whose parent chain crosses a symlink.
		if err := checkNoSymlinkParents(baseDir, entry.Path); err != nil {
			return err
		}
		full := filepath.Join(baseDir, filepath.FromSlash(entry.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("ocicas: %w", err)
		}
		if entry.Symlink != "" {
			if err := os.RemoveAll(full); err != nil {
				return fmt.Errorf("ocicas: %w", err)
			}
			if err := os.Symlink(entry.Symlink, full); err != nil {
				return fmt.Errorf("ocicas: %w", err)
			}
			continue
		}
		if err := assembleFile(entry, full, stream); err != nil {
			return err
		}
	}
	return nil
}

// streamReader walks the decompressed stream chunk by chunk; consumers read
// exactly the file sizes of the index, in index order.
type streamReader struct {
	idx  *Index
	src  Source
	next int    // next chunk to fetch
	raw  []byte // unconsumed tail of the current chunk
}

func (sr *streamReader) read(n int64) ([]byte, error) {
	for int64(len(sr.raw)) < n {
		if sr.next >= len(sr.idx.Chunks) {
			return nil, fmt.Errorf("ocicas: stream exhausted (index inconsistent)")
		}
		ref := sr.idx.Chunks[sr.next]
		sr.next++
		compressed, err := sr.src(ref.Digest)
		if err != nil {
			return nil, fmt.Errorf("ocicas: fetching %s: %w", ref.Digest, err)
		}
		sum := sha256.Sum256(compressed)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != ref.Digest {
			return nil, fmt.Errorf("ocicas: chunk digest mismatch: want %s, got %s", ref.Digest, got)
		}
		raw, err := zstdDec.DecodeAll(compressed, make([]byte, 0, ref.RawSize))
		if err != nil {
			return nil, fmt.Errorf("ocicas: decompressing %s: %w", ref.Digest, err)
		}
		if int64(len(raw)) != ref.RawSize {
			return nil, fmt.Errorf("ocicas: chunk %s: raw size %d, index says %d",
				ref.Digest, len(raw), ref.RawSize)
		}
		sr.raw = append(sr.raw, raw...)
	}
	out := sr.raw[:n]
	sr.raw = sr.raw[n:]
	return out, nil
}

func assembleFile(entry FileEntry, full string, stream *streamReader) error {
	// O_TRUNC on a stale symlink would write through it into the link
	// target: replace anything that is not a regular file
	if fi, err := os.Lstat(full); err == nil && !fi.Mode().IsRegular() {
		if err := os.RemoveAll(full); err != nil {
			return fmt.Errorf("ocicas: %w", err)
		}
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(entry.Mode))
	if err != nil {
		return fmt.Errorf("ocicas: %w", err)
	}
	defer f.Close()
	// the file may pre-exist with other permissions: enforce the index's
	if err := f.Chmod(os.FileMode(entry.Mode)); err != nil {
		return fmt.Errorf("ocicas: %w", err)
	}

	remaining := entry.Size
	for remaining > 0 {
		data, err := stream.read(min(remaining, MaxChunkSize))
		if err != nil {
			return fmt.Errorf("ocicas: %s: %w", entry.Path, err)
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("ocicas: %w", err)
		}
		remaining -= int64(len(data))
	}
	return nil
}

// checkNoSymlinkParents rejects a path any of whose existing parent
// directories is a symlink: writing under it would follow the link out of
// baseDir. The leaf is not checked — it is the entry being (re)created.
func checkNoSymlinkParents(baseDir, slashPath string) error {
	parts := strings.Split(slashPath, "/")
	dir := baseDir
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		fi, err := os.Lstat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // nothing deeper exists yet, nothing to follow
			}
			return fmt.Errorf("ocicas: %w", err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ocicas: %q: parent %q is a symlink", slashPath, part)
		}
	}
	return nil
}

// DigestHex returns the bare hex of a "sha256:<hex>" digest — the layout
// helper for content-addressed stores (.../sha256/<hex>).
func DigestHex(digest string) (string, bool) {
	hex, ok := strings.CutPrefix(digest, "sha256:")
	return hex, ok && len(hex) == 64
}
