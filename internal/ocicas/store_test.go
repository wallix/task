package ocicas

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

// countingTarget counts chunk pushes and fetches going to the registry.
type countingTarget struct {
	oras.GraphTarget
	chunkPushes  atomic.Int64
	chunkFetches atomic.Int64
}

func (c *countingTarget) Push(ctx context.Context, desc v1.Descriptor, r io.Reader) error {
	if desc.MediaType == MediaTypeChunk {
		c.chunkPushes.Add(1)
	}
	return c.GraphTarget.Push(ctx, desc, r)
}

func (c *countingTarget) Fetch(ctx context.Context, desc v1.Descriptor) (io.ReadCloser, error) {
	if desc.MediaType == MediaTypeChunk {
		c.chunkFetches.Add(1)
	}
	return c.GraphTarget.Fetch(ctx, desc)
}

func writeTree(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func pushTree(t *testing.T, store *Store, dir, tag string, paths []string, ann map[string]string) *Index {
	t.Helper()
	ctx := context.Background()
	sess := store.NewPushSession(ctx)
	idx, err := Build(dir, paths, sess.Sink())
	if werr := sess.Wait(); err == nil {
		err = werr
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PushIndex(ctx, tag, idx, ann); err != nil {
		t.Fatal(err)
	}
	return idx
}

// failingTarget rejects every push, to exercise the session error path.
type failingTarget struct{ oras.GraphTarget }

func (f *failingTarget) Push(context.Context, v1.Descriptor, io.Reader) error {
	return errors.New("registry on fire")
}

func TestPushSessionPropagatesErrors(t *testing.T) {
	store := NewStore(&failingTarget{memory.New()}, "")
	src := t.TempDir()
	writeTree(t, src, map[string][]byte{"f": randomBytes(t, 8<<20, 20)})

	sess := store.NewPushSession(context.Background())
	_, berr := Build(src, []string{"f"}, sess.Sink())
	werr := sess.Wait()
	if berr == nil && werr == nil {
		t.Fatal("expected the upload failure to surface")
	}
	if werr != nil && !strings.Contains(werr.Error(), "registry on fire") {
		t.Fatalf("unexpected error: %v", werr)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	target := &countingTarget{GraphTarget: memory.New()}
	store := NewStore(target, t.TempDir())

	src := t.TempDir()
	files := map[string][]byte{
		"big.bin": randomBytes(t, 5<<20, 10),
		"small":   []byte("data"),
	}
	writeTree(t, src, files)
	pushTree(t, store, src, "entry-1", []string{"big.bin", "small"},
		map[string]string{"task": "build:thing"})

	dst := t.TempDir()
	_, ann, err := store.Pull(context.Background(), "entry-1", dst)
	if err != nil {
		t.Fatal(err)
	}
	if ann["task"] != "build:thing" {
		t.Errorf("annotations lost: %v", ann)
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
}

// TestStorePushDeduplicates: pushing a mutated entry only uploads the new
// chunks — the whole point of the CDC format.
func TestStorePushDeduplicates(t *testing.T) {
	target := &countingTarget{GraphTarget: memory.New()}
	store := NewStore(target, "")

	data := randomBytes(t, 8<<20, 11)
	src1 := t.TempDir()
	writeTree(t, src1, map[string][]byte{"image.bin": data})
	pushTree(t, store, src1, "v1", []string{"image.bin"}, nil)
	firstPushes := target.chunkPushes.Load()

	// mutate 100 bytes in the middle: most chunks must be reused
	mutated := bytes.Clone(data)
	copy(mutated[4<<20:], randomBytes(t, 100, 12))
	src2 := t.TempDir()
	writeTree(t, src2, map[string][]byte{"image.bin": mutated})
	pushTree(t, store, src2, "v2", []string{"image.bin"}, nil)

	delta := target.chunkPushes.Load() - firstPushes
	if delta == 0 || delta > firstPushes/2 {
		t.Fatalf("expected a small chunk delta, got %d new pushes (first push: %d)",
			delta, firstPushes)
	}
}

// TestStoreIncrementalPull: with a warm local CAS, a second pull fetches no
// chunk from the registry.
func TestStoreIncrementalPull(t *testing.T) {
	target := &countingTarget{GraphTarget: memory.New()}
	store := NewStore(target, t.TempDir())

	src := t.TempDir()
	writeTree(t, src, map[string][]byte{"image.bin": randomBytes(t, 5<<20, 13)})
	pushTree(t, store, src, "v1", []string{"image.bin"}, nil)

	if _, _, err := store.Pull(context.Background(), "v1", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cold := target.chunkFetches.Load()
	if cold == 0 {
		t.Fatal("first pull fetched nothing")
	}
	if _, _, err := store.Pull(context.Background(), "v1", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if warm := target.chunkFetches.Load() - cold; warm != 0 {
		t.Fatalf("warm pull fetched %d chunks from the registry", warm)
	}
}

// TestStoreSelfHealingCAS: a corrupted local CAS entry is refetched instead
// of failing the restore.
func TestStoreSelfHealingCAS(t *testing.T) {
	cas := t.TempDir()
	target := &countingTarget{GraphTarget: memory.New()}
	store := NewStore(target, cas)

	src := t.TempDir()
	writeTree(t, src, map[string][]byte{"f": randomBytes(t, 2<<20, 14)})
	pushTree(t, store, src, "v1", []string{"f"}, nil)
	if _, _, err := store.Pull(context.Background(), "v1", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(cas, "sha256"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no CAS entries: %v", err)
	}
	victim := filepath.Join(cas, "sha256", entries[0].Name())
	if err := os.WriteFile(victim, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	if _, _, err := store.Pull(context.Background(), "v1", dst); err != nil {
		t.Fatalf("pull did not heal the corrupt CAS entry: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "f"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(src, "f"))
	if !bytes.Equal(got, want) {
		t.Fatal("healed pull produced wrong content")
	}
}

// TestStoreResolveAnnotations: the cheap pre-push check — nil for a missing
// tag, the manifest annotations for an existing entry.
func TestStoreResolveAnnotations(t *testing.T) {
	store := NewStore(memory.New(), "")
	ctx := context.Background()

	ann, err := store.ResolveAnnotations(ctx, "absent")
	if err != nil {
		t.Fatal(err)
	}
	if ann != nil {
		t.Fatalf("expected nil for a missing tag, got %v", ann)
	}

	src := t.TempDir()
	writeTree(t, src, map[string][]byte{"f": []byte("data")})
	pushTree(t, store, src, "v1", []string{"f"}, map[string]string{"generates": "abc"})

	ann, err = store.ResolveAnnotations(ctx, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if ann["generates"] != "abc" {
		t.Fatalf("annotations %v", ann)
	}
}

func TestStoreRejectsForeignArtifact(t *testing.T) {
	target := memory.New()
	store := NewStore(target, "")
	// push a plain (non-cas) manifest under a tag
	desc := v1.DescriptorEmptyJSON
	if err := target.Push(context.Background(), desc, bytes.NewReader(desc.Data)); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":` +
		`{"mediaType":"application/vnd.oci.empty.v1+json","digest":"` + string(desc.Digest) + `","size":2},"layers":[]}`)
	mdesc := v1.Descriptor{
		MediaType: v1.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifest),
		Size:      int64(len(manifest)),
	}
	if err := target.Push(context.Background(), mdesc, bytes.NewReader(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := target.Tag(context.Background(), mdesc, "foreign"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Pull(context.Background(), "foreign", t.TempDir()); err == nil {
		t.Fatal("expected an artifact-type rejection")
	}
}
