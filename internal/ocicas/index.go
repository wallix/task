package ocicas

import (
	"encoding/json"
	"fmt"
	"strings"
)

// IndexVersion is bumped on any incompatible change to the index schema or
// the chunking/compression parameters (see chunker.go). v2: ALL file
// contents are concatenated into one continuous stream (in index order,
// metadata stays in the index — a tar without the header noise) and the
// stream is what gets chunked: a node_modules tree is a thousand ~1M
// chunks, not a chunk per file. The index blob is zstd-compressed. v1
// entries are rejected on read: the cache treats them as misses and
// overwrites them.
const IndexVersion = 2

// ArtifactType and the media types identify the artifact and its blobs.
const (
	ArtifactType   = "application/vnd.wallix.cas.v2"
	MediaTypeIndex = "application/vnd.wallix.cas.index.v2+zstd"
	MediaTypeChunk = "application/vnd.wallix.cas.chunk.v1+zstd"
)

// maxIndexSize bounds the decompressed index (an index never legitimately
// approaches this; the cap only guards the decompression).
const maxIndexSize = 1 << 30

// ChunkRef references one compressed chunk blob of the stream.
type ChunkRef struct {
	// Digest is "sha256:<hex>" of the COMPRESSED bytes — the blob digest
	// the registry stores and verifies.
	Digest string `json:"digest"`
	// Size is the compressed (blob) size.
	Size int64 `json:"size"`
	// RawSize is the uncompressed size; it bounds decompression on restore.
	RawSize int64 `json:"rawSize"`
}

// FileEntry describes one file of the set. Parent directories are implicit
// (created 0755 on restore). The file's bytes are NOT referenced here: they
// are the next Size bytes of the stream, files being concatenated in index
// order.
type FileEntry struct {
	// Path is slash-separated, relative, without "." or ".." components.
	Path string `json:"path"`
	// Mode carries the permission bits only.
	Mode uint32 `json:"mode"`
	// Size is the file size in bytes (0 for symlinks).
	Size int64 `json:"size,omitempty"`
	// Symlink is the link target; a symlink contributes nothing to the
	// stream.
	Symlink string `json:"symlink,omitempty"`
}

// ChunkerParams pins the boundary algorithm; entries with unknown params
// must be rejected (re-chunking them would produce different digests).
type ChunkerParams struct {
	Algo string `json:"algo"`
	Min  int    `json:"min"`
	Avg  int    `json:"avg"`
	Max  int    `json:"max"`
}

// Compression pins the per-chunk compression. The level only affects
// deduplication (different bytes), never correctness.
type Compression struct {
	Algo  string `json:"algo"`
	Level int    `json:"level"`
}

// Index is the artifact's table of contents: the ordered file list and the
// ordered chunk list of their concatenated contents. Both orders are part
// of the format; files are sorted by path, so the serialization is
// deterministic for a given tree.
type Index struct {
	Version     int           `json:"version"`
	Chunker     ChunkerParams `json:"chunker"`
	Compression Compression   `json:"compression"`
	Chunks      []ChunkRef    `json:"chunks,omitempty"`
	Files       []FileEntry   `json:"files"`
}

func newIndex() *Index {
	return &Index{
		Version: IndexVersion,
		Chunker: ChunkerParams{
			Algo: "fastcdc-v1",
			Min:  MinChunkSize,
			Avg:  AvgChunkSize,
			Max:  MaxChunkSize,
		},
		Compression: Compression{Algo: "zstd", Level: zstdLevel},
	}
}

// streamSize is the total length of the concatenated contents.
func (idx *Index) streamSize() int64 {
	var n int64
	for _, f := range idx.Files {
		if f.Symlink == "" {
			n += f.Size
		}
	}
	return n
}

// Marshal renders the index as zstd-compressed deterministic JSON. Large
// trees make large indexes — 100k files are JSON megabytes, far less
// compressed.
func (idx *Index) Marshal() ([]byte, error) {
	raw, err := json.Marshal(idx)
	if err != nil {
		return nil, fmt.Errorf("ocicas: %w", err)
	}
	return zstdEnc.EncodeAll(raw, nil), nil
}

// UnmarshalIndex parses and validates an index, rejecting unknown versions,
// chunking parameters, unsafe paths and a chunk list inconsistent with the
// file sizes.
func UnmarshalIndex(data []byte) (*Index, error) {
	raw, err := zstdDec.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("ocicas: decompressing index: %w", err)
	}
	if len(raw) > maxIndexSize {
		return nil, fmt.Errorf("ocicas: index too large (%d bytes)", len(raw))
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("ocicas: parsing index: %w", err)
	}
	if idx.Version != IndexVersion {
		return nil, fmt.Errorf("ocicas: unsupported index version %d", idx.Version)
	}
	want := newIndex()
	if idx.Chunker != want.Chunker {
		return nil, fmt.Errorf("ocicas: unsupported chunker params %+v", idx.Chunker)
	}
	if idx.Compression.Algo != "zstd" {
		return nil, fmt.Errorf("ocicas: unsupported compression %q", idx.Compression.Algo)
	}
	var chunked int64
	for _, c := range idx.Chunks {
		if c.RawSize < 0 || c.Size < 0 {
			return nil, fmt.Errorf("ocicas: negative chunk size")
		}
		// the chunker never emits more: a larger RawSize is a forged index
		// trying to force a huge allocation on restore
		if c.RawSize > MaxChunkSize {
			return nil, fmt.Errorf("ocicas: chunk %s: raw size %d exceeds max %d",
				c.Digest, c.RawSize, MaxChunkSize)
		}
		chunked += c.RawSize
	}
	if chunked != idx.streamSize() {
		return nil, fmt.Errorf("ocicas: chunks cover %d bytes, files need %d",
			chunked, idx.streamSize())
	}
	for _, f := range idx.Files {
		if !safeRelPath(f.Path) {
			return nil, fmt.Errorf("ocicas: unsafe path %q in index", f.Path)
		}
		if f.Size < 0 {
			return nil, fmt.Errorf("ocicas: %s: negative size", f.Path)
		}
		// Mode carries permission bits only; anything else (setuid/setgid
		// bit positions, type bits) is a forged index
		if f.Mode&^0o777 != 0 {
			return nil, fmt.Errorf("ocicas: %s: invalid mode %o", f.Path, f.Mode)
		}
	}
	return &idx, nil
}

// safeRelPath accepts slash-separated relative paths without empty, "." or
// ".." components — the zip-slip guard of the format.
func safeRelPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
