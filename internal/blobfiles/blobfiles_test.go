package blobfiles_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/pwgith/shadow-mock/internal/blob"
	"github.com/pwgith/shadow-mock/internal/blobfiles"
	"github.com/pwgith/shadow-mock/internal/store"
)

var (
	acme   = store.Global("acme")
	globex = store.Global("globex")
)

// flaky wraps a store and can be told to fail, to count reads, and to hand back wrong bytes.
type flaky struct {
	blob.Store
	down  atomic.Bool
	reads atomic.Int32
	swap  atomic.Pointer[[]byte]
}

var errDown = errors.New("connection refused")

func (f *flaky) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, error) {
	f.reads.Add(1)
	if f.down.Load() {
		return nil, errors.Join(blob.ErrUnavailable, errDown)
	}
	if b := f.swap.Load(); b != nil {
		return io.NopCloser(bytesReader(*b)), nil
	}
	return f.Store.Get(ctx, key, rng)
}

func (f *flaky) Put(ctx context.Context, key string, data []byte, meta blob.Meta) error {
	if f.down.Load() {
		return errors.Join(blob.ErrUnavailable, errDown)
	}
	return f.Store.Put(ctx, key, data, meta)
}

func bytesReader(b []byte) io.Reader { return &sliceReader{b: b} }

type sliceReader struct{ b []byte }

func (r *sliceReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

func newRepo(cacheBytes int64) (*blobfiles.Repository, *flaky) {
	fl := &flaky{Store: blob.NewMemory()}
	return blobfiles.New(blobfiles.NewMemoryIndex(), fl, cacheBytes), fl
}

func TestFilesRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r, fl := newRepo(-1)
	if err := r.Put(ctx, acme, "/data/a.json", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	got, err := r.Read(ctx, acme, "data/a.json")
	if err != nil || string(got) != `{"a":1}` {
		t.Fatalf("read = %q, %v", got, err)
	}
	if names, _ := r.List(ctx, acme); !slices.Equal(names, []string{"data/a.json"}) {
		t.Errorf("list = %v", names)
	}
	// The object is stored under its org and hash, and never changes.
	sum := sha256.Sum256([]byte(`{"a":1}`))
	if _, err = fl.Stat(ctx, blob.FileKey("acme", sum)); err != nil {
		t.Errorf("the body should be at its content address: %v", err)
	}
	if err = r.Delete(ctx, acme, "data/a.json"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Read(ctx, acme, "data/a.json"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err = r.Delete(ctx, acme, "data/a.json"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestOrgsDoNotSeeEachOthersFiles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r, _ := newRepo(0)
	_ = r.Put(ctx, acme, "same.txt", []byte("acme's"))
	if _, err := r.Read(ctx, globex, "same.txt"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("globex read acme's file: %v", err)
	}
	_ = r.Put(ctx, globex, "same.txt", []byte("globex's"))
	if got, _ := r.Read(ctx, acme, "same.txt"); string(got) != "acme's" {
		t.Errorf("acme's file changed: %q", got)
	}
}

func TestTheCacheServesWhenTheStoreIsDown(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r, fl := newRepo(1 << 20)
	_ = r.Put(ctx, acme, "cached.txt", []byte("hello"))
	_ = r.Put(ctx, acme, "other.txt", []byte("other"))
	fl.down.Store(true)
	if got, err := r.Read(ctx, acme, "cached.txt"); err != nil || string(got) != "hello" {
		t.Errorf("a cached file should be served while the store is down: %q, %v", got, err)
	}
	if reads := fl.reads.Load(); reads != 0 {
		t.Errorf("a cached read touched the store %d times", reads)
	}
	// With no cache, the same outage is an error the caller can tell from "not found".
	cold := blobfiles.New(blobfiles.NewMemoryIndex(), fl, -1)
	fl.down.Store(false)
	_ = cold.Put(ctx, acme, "x.txt", []byte("x"))
	fl.down.Store(true)
	if _, err := cold.Read(ctx, acme, "x.txt"); !errors.Is(err, blob.ErrUnavailable) {
		t.Errorf("an uncached file with the store down: %v, want blob.ErrUnavailable", err)
	}
}

func TestACorruptBodyIsRefused(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r, fl := newRepo(-1)
	_ = r.Put(ctx, acme, "a.txt", []byte("the real body"))
	wrong := []byte("somebody else's body")
	fl.swap.Store(&wrong)
	if _, err := r.Read(ctx, acme, "a.txt"); !errors.Is(err, blobfiles.ErrCorrupt) {
		t.Errorf("read of bytes that do not match their hash: %v, want ErrCorrupt", err)
	}
}

func TestACorruptBodyIsNeverCached(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	fl := &flaky{Store: blob.NewMemory()}
	// Put caches the true body, so a second repository does the reading and shows what a read caches.
	idx := blobfiles.NewMemoryIndex()
	w := blobfiles.New(idx, fl, -1)
	_ = w.Put(ctx, acme, "a.txt", []byte("true"))
	reader := blobfiles.New(idx, fl, 1<<20)
	wrong := []byte("false")
	fl.swap.Store(&wrong)
	_, _ = reader.Read(ctx, acme, "a.txt")
	fl.swap.Store(nil)
	if got, err := reader.Read(ctx, acme, "a.txt"); err != nil || string(got) != "true" {
		t.Errorf("after the store recovers: %q, %v", got, err)
	}
}

func TestTheCacheIsBounded(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r, fl := newRepo(10)
	_ = r.Put(ctx, acme, "a", []byte("aaaaaa")) // 6 bytes
	_ = r.Put(ctx, acme, "b", []byte("bbbbbb")) // 6 bytes: a is evicted
	fl.reads.Store(0)
	_, _ = r.Read(ctx, acme, "b")
	if fl.reads.Load() != 0 {
		t.Error("b should be cached")
	}
	_, _ = r.Read(ctx, acme, "a")
	if fl.reads.Load() != 1 {
		t.Errorf("a should have been evicted and read again, reads = %d", fl.reads.Load())
	}
	big := make([]byte, 100)
	_ = r.Put(ctx, acme, "big", big)
	fl.reads.Store(0)
	_, _ = r.Read(ctx, acme, "big")
	if fl.reads.Load() != 1 {
		t.Error("a body over the limit must not be cached")
	}
}

func TestSessionScopesAreNotStoredHere(t *testing.T) {
	t.Parallel()
	r, _ := newRepo(0)
	if err := r.Put(t.Context(), store.Scope{Tenant: "acme", SessionID: "ses_1"}, "a", []byte("x")); err == nil {
		t.Error("a shadow mock's files belong in memory, not in object storage")
	}
}

func TestPurgeRemovesObjectsAndNames(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	r, fl := newRepo(1 << 20)
	_ = r.Put(ctx, acme, "a", []byte("1"))
	_ = r.Put(ctx, acme, "b", []byte("2"))
	_ = r.Put(ctx, globex, "a", []byte("3"))
	if err := r.Purge(ctx, "acme"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := fl.List(ctx, blob.FilesPrefix("acme")); len(keys) != 0 {
		t.Errorf("objects left: %v", keys)
	}
	if names, _ := r.List(ctx, acme); len(names) != 0 {
		t.Errorf("names left: %v", names)
	}
	if got, err := r.Read(ctx, globex, "a"); err != nil || string(got) != "3" {
		t.Errorf("another org's file was touched: %q, %v", got, err)
	}
	if err := r.Purge(ctx, "acme"); err != nil {
		t.Errorf("purging again: %v", err)
	}
}
