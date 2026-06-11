package ocicas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/wallix/task/v3/errors"
)

// fetchConcurrency bounds the parallel chunk transfers (pull fetches and
// push session uploads).
const fetchConcurrency = 8

// Store pushes and pulls ocicas artifacts against an OCI target (a remote
// repository in production, an in-memory store in tests), with an optional
// local chunk CAS that makes repeated pulls incremental: only chunks absent
// from cacheDir are fetched from the registry.
type Store struct {
	target   oras.GraphTarget
	cacheDir string
}

// NewStore wraps an OCI target. cacheDir is the local chunk CAS directory
// (empty = no local cache, chunks are fetched into memory).
func NewStore(target oras.GraphTarget, cacheDir string) *Store {
	return &Store{target: target, cacheDir: cacheDir}
}

// RemoteOptions configures NewRemoteStore.
type RemoteOptions struct {
	Username string
	Password string
	// CAFile adds an extra trust anchor (the corp registry is self-signed).
	CAFile string
	// CacheDir is the local chunk CAS directory (empty = none).
	CacheDir string
	// PlainHTTP talks to the registry without TLS (local development).
	PlainHTTP bool
}

// NewRemoteStore opens "host/repo" as an OCI store.
func NewRemoteStore(ref string, o RemoteOptions) (*Store, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return nil, fmt.Errorf("ocicas: %w", err)
	}
	repo.PlainHTTP = o.PlainHTTP
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if o.CAFile != "" {
		pem, err := os.ReadFile(o.CAFile)
		if err != nil {
			return nil, fmt.Errorf("ocicas: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ocicas: no certificate in %s", o.CAFile)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	client := &auth.Client{
		Client: &http.Client{Transport: retry.NewTransport(transport)},
		Cache:  auth.NewCache(),
	}
	if o.Username != "" {
		client.Credential = auth.StaticCredential(repo.Reference.Registry, auth.Credential{
			Username: o.Username,
			Password: o.Password,
		})
	}
	repo.Client = client
	return NewStore(repo, o.CacheDir), nil
}

// ResolveAnnotations fetches just the manifest annotations of a tag, or nil
// if the tag does not exist — the cheap "is this entry already pushed and
// matching" check.
func (s *Store) ResolveAnnotations(ctx context.Context, tag string) (map[string]string, error) {
	mdesc, err := s.target.Resolve(ctx, tag)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("ocicas: resolving %s: %w", tag, err)
	}
	mraw, err := content.FetchAll(ctx, s.target, mdesc)
	if err != nil {
		return nil, fmt.Errorf("ocicas: fetching manifest: %w", err)
	}
	var manifest v1.Manifest
	if err := json.Unmarshal(mraw, &manifest); err != nil {
		return nil, fmt.Errorf("ocicas: parsing manifest: %w", err)
	}
	if manifest.ArtifactType != ArtifactType {
		return nil, fmt.Errorf("ocicas: %s is not a %s artifact", tag, ArtifactType)
	}
	if manifest.Annotations == nil {
		return map[string]string{}, nil
	}
	return manifest.Annotations, nil
}

func chunkDescriptor(ref ChunkRef) v1.Descriptor {
	return v1.Descriptor{
		MediaType: MediaTypeChunk,
		Digest:    digest.Digest(ref.Digest),
		Size:      ref.Size,
	}
}

// PushSession uploads chunks concurrently while Build is still chunking:
// the sink enqueues, a bounded worker pool does the existence check and the
// upload (the cross-entry deduplication — chunks already pushed by any
// previous entry are skipped). Call Wait before PushIndex: OCI requires the
// blobs to exist before the manifest referencing them.
type PushSession struct {
	store   *Store
	g       *errgroup.Group
	gctx    context.Context
	pushed  atomic.Int64
	skipped atomic.Int64
	bytes   atomic.Int64
}

// NewPushSession starts a push session over the store's target.
func (s *Store) NewPushSession(ctx context.Context) *PushSession {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fetchConcurrency)
	return &PushSession{store: s, g: g, gctx: gctx}
}

// Sink returns the Build sink. Errors from earlier uploads surface here
// (failing the Build fast) or in Wait.
func (p *PushSession) Sink() Sink {
	return func(dgst string, compressed []byte) error {
		if err := p.gctx.Err(); err != nil {
			return p.g.Wait() // a worker failed: report its error, not "canceled"
		}
		// EncodeAll allocated `compressed` for this chunk only: safe to hold.
		// g.Go blocks when the pool is full — natural backpressure, at most
		// fetchConcurrency chunks buffered beyond the one being built.
		p.g.Go(func() error {
			desc := v1.Descriptor{
				MediaType: MediaTypeChunk,
				Digest:    digest.Digest(dgst),
				Size:      int64(len(compressed)),
			}
			exists, err := p.store.target.Exists(p.gctx, desc)
			if err != nil {
				return fmt.Errorf("ocicas: checking %s: %w", dgst, err)
			}
			if exists {
				p.skipped.Add(1)
				return nil
			}
			if err := p.store.target.Push(p.gctx, desc, bytes.NewReader(compressed)); err != nil &&
				!errors.Is(err, errdef.ErrAlreadyExists) {
				return fmt.Errorf("ocicas: pushing %s: %w", dgst, err)
			}
			p.pushed.Add(1)
			p.bytes.Add(int64(len(compressed)))
			return nil
		})
		return nil
	}
}

// Wait blocks until every enqueued upload completed and returns the first
// error.
func (p *PushSession) Wait() error { return p.g.Wait() }

// Stats reports the uploads performed and skipped (already in the registry)
// and the bytes actually sent — the deduplication, observable per entry.
func (p *PushSession) Stats() (pushed, skipped, bytes int64) {
	return p.pushed.Load(), p.skipped.Load(), p.bytes.Load()
}

// PushIndex pushes the index blob and the manifest referencing it plus
// every chunk (so the registry GC keeps shared chunks alive as long as one
// entry needs them), then tags the manifest. Build + PushSink must have
// completed first: OCI requires blobs to exist before the manifest.
func (s *Store) PushIndex(ctx context.Context, tag string, idx *Index, annotations map[string]string) error {
	raw, err := idx.Marshal()
	if err != nil {
		return err
	}
	indexDesc := v1.Descriptor{
		MediaType: MediaTypeIndex,
		Digest:    digest.FromBytes(raw),
		Size:      int64(len(raw)),
	}
	if err := s.pushIfAbsent(ctx, indexDesc, raw); err != nil {
		return err
	}
	if err := s.pushIfAbsent(ctx, v1.DescriptorEmptyJSON, v1.DescriptorEmptyJSON.Data); err != nil {
		return err
	}

	layers := []v1.Descriptor{indexDesc}
	seen := map[string]bool{}
	for _, c := range idx.Chunks {
		if !seen[c.Digest] {
			seen[c.Digest] = true
			layers = append(layers, chunkDescriptor(c))
		}
	}
	manifest := v1.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    v1.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config:       v1.DescriptorEmptyJSON,
		Layers:       layers,
		Annotations:  annotations,
	}
	mraw, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("ocicas: %w", err)
	}
	mdesc := v1.Descriptor{
		MediaType:    v1.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Digest:       digest.FromBytes(mraw),
		Size:         int64(len(mraw)),
		Annotations:  annotations,
	}
	if err := s.pushIfAbsent(ctx, mdesc, mraw); err != nil {
		return err
	}
	if err := s.target.Tag(ctx, mdesc, tag); err != nil {
		return fmt.Errorf("ocicas: tagging %s: %w", tag, err)
	}
	return nil
}

func (s *Store) pushIfAbsent(ctx context.Context, desc v1.Descriptor, data []byte) error {
	exists, err := s.target.Exists(ctx, desc)
	if err != nil {
		return fmt.Errorf("ocicas: checking %s: %w", desc.Digest, err)
	}
	if exists {
		return nil
	}
	if err := s.target.Push(ctx, desc, bytes.NewReader(data)); err != nil &&
		!errors.Is(err, errdef.ErrAlreadyExists) {
		return fmt.Errorf("ocicas: pushing %s: %w", desc.Digest, err)
	}
	return nil
}

// Pull resolves tag, fetches the missing chunks (through the local CAS when
// configured) and assembles the file set under dir. It returns the index
// and the manifest annotations.
func (s *Store) Pull(ctx context.Context, tag, dir string) (*Index, map[string]string, error) {
	mdesc, err := s.target.Resolve(ctx, tag)
	if err != nil {
		return nil, nil, fmt.Errorf("ocicas: resolving %s: %w", tag, err)
	}
	mraw, err := content.FetchAll(ctx, s.target, mdesc)
	if err != nil {
		return nil, nil, fmt.Errorf("ocicas: fetching manifest: %w", err)
	}
	var manifest v1.Manifest
	if err := json.Unmarshal(mraw, &manifest); err != nil {
		return nil, nil, fmt.Errorf("ocicas: parsing manifest: %w", err)
	}
	if manifest.ArtifactType != ArtifactType {
		return nil, nil, fmt.Errorf("ocicas: %s is not a %s artifact (got %q)",
			tag, ArtifactType, manifest.ArtifactType)
	}
	var indexDesc *v1.Descriptor
	for i, l := range manifest.Layers {
		if l.MediaType == MediaTypeIndex {
			indexDesc = &manifest.Layers[i]
			break
		}
	}
	if indexDesc == nil {
		return nil, nil, fmt.Errorf("ocicas: %s has no index layer", tag)
	}
	rawIdx, err := content.FetchAll(ctx, s.target, *indexDesc)
	if err != nil {
		return nil, nil, fmt.Errorf("ocicas: fetching index: %w", err)
	}
	idx, err := UnmarshalIndex(rawIdx)
	if err != nil {
		return nil, nil, err
	}

	source, err := s.chunkSource(ctx, idx)
	if err != nil {
		return nil, nil, err
	}
	if err := Assemble(idx, dir, source); err != nil {
		return nil, nil, err
	}
	return idx, manifest.Annotations, nil
}

// chunkSource prefetches the chunks absent from the local CAS (bounded
// concurrency) and returns the Source feeding Assemble. Without a cacheDir
// the chunks are held in memory.
func (s *Store) chunkSource(ctx context.Context, idx *Index) (Source, error) {
	want := map[string]ChunkRef{}
	for _, c := range idx.Chunks {
		want[c.Digest] = c
	}

	mem := map[string][]byte{}
	var memMu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fetchConcurrency)
	for d, ref := range want {
		if s.cacheDir != "" && s.casValid(d) {
			continue
		}
		g.Go(func() error {
			data, err := content.FetchAll(gctx, s.target, chunkDescriptor(ref))
			if err != nil {
				return fmt.Errorf("ocicas: fetching chunk %s: %w", d, err)
			}
			if s.cacheDir != "" {
				return s.casWrite(d, data)
			}
			memMu.Lock()
			mem[d] = data
			memMu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return func(d string) ([]byte, error) {
		if s.cacheDir == "" {
			data, ok := mem[d]
			if !ok {
				return nil, fmt.Errorf("ocicas: chunk %s not fetched", d)
			}
			return data, nil
		}
		data, err := os.ReadFile(s.casPath(d))
		if err != nil {
			return nil, err
		}
		// self-healing CAS: a corrupted local file is refetched instead of
		// failing the restore
		if sum := sha256.Sum256(data); "sha256:"+hex.EncodeToString(sum[:]) != d {
			ref, ok := want[d]
			if !ok {
				return nil, fmt.Errorf("ocicas: corrupt CAS entry %s", d)
			}
			data, err = content.FetchAll(ctx, s.target, chunkDescriptor(ref))
			if err != nil {
				return nil, fmt.Errorf("ocicas: refetching corrupt chunk %s: %w", d, err)
			}
			if err := s.casWrite(d, data); err != nil {
				return nil, err
			}
		}
		return data, nil
	}, nil
}

func (s *Store) casPath(dgst string) string {
	hex, _ := DigestHex(dgst)
	return filepath.Join(s.cacheDir, "sha256", hex)
}

// casValid reports whether the chunk is present locally. Content integrity
// is re-checked at read time (chunkSource), not here.
func (s *Store) casValid(dgst string) bool {
	hex, ok := DigestHex(dgst)
	if !ok {
		return false
	}
	st, err := os.Stat(filepath.Join(s.cacheDir, "sha256", hex))
	return err == nil && st.Mode().IsRegular()
}

// casWrite stores a chunk atomically (tmp + rename), so concurrent pulls
// never observe a half-written entry.
func (s *Store) casWrite(dgst string, data []byte) error {
	hexd, ok := DigestHex(dgst)
	if !ok {
		return fmt.Errorf("ocicas: bad digest %q", dgst)
	}
	dir := filepath.Join(s.cacheDir, "sha256")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ocicas: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+hexd+".tmp*")
	if err != nil {
		return fmt.Errorf("ocicas: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("ocicas: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ocicas: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, hexd)); err != nil {
		return fmt.Errorf("ocicas: %w", err)
	}
	return nil
}
