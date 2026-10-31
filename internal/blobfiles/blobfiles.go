// Package blobfiles is a store.FileRepository for an org's global files when the bytes live in object storage and
// the names live in a database (FR-11.13). A name points at an entry in an Index: the SHA-256, size and type of the
// body. The body is an object under files/<org>/<sha256> that never changes, so a read can check the hash and a
// cache can never go stale.
package blobfiles

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/pwgith/shadow-mock/internal/blob"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// ErrCorrupt is returned when an object's bytes do not hash to the name they are stored under.
var ErrCorrupt = errors.New("file body does not match its hash")

// Entry is what the index knows about a file.
type Entry struct {
	SHA256      [sha256.Size]byte
	Size        int64
	ContentType string
}

// Index maps the names of an org's files to their entries. Postgres is the production index and MemoryIndex the
// one for tests. Lookup and Delete of a name that is not there return store.ErrNotFound.
type Index interface {
	Lookup(ctx context.Context, org tenant.ID, name string) (Entry, error)
	Put(ctx context.Context, org tenant.ID, name string, e Entry) error
	Delete(ctx context.Context, org tenant.ID, name string) error
	DeleteAll(ctx context.Context, org tenant.ID) error
	List(ctx context.Context, org tenant.ID) ([]string, error)
}

// DefaultCacheBytes bounds the bodies kept in memory.
const DefaultCacheBytes = 64 << 20

// Repository is the file repository of the global layer.
type Repository struct {
	index Index
	blobs blob.Store
	cache *cache
}

var _ store.FileRepository = (*Repository)(nil)

// New returns a repository over the index and the object store. cacheBytes bounds the bodies it keeps in memory;
// zero means DefaultCacheBytes, and a negative number turns the cache off.
func New(index Index, blobs blob.Store, cacheBytes int64) *Repository {
	if cacheBytes == 0 {
		cacheBytes = DefaultCacheBytes
	}
	return &Repository{index: index, blobs: blobs, cache: newCache(cacheBytes)}
}

func globalOnly(s store.Scope) error {
	if !s.IsGlobal() {
		return fmt.Errorf("blobfiles: only the global layer's files are stored here, not %s", s)
	}
	return nil
}

func clean(name string) string { return strings.TrimPrefix(name, "/") }

// Read implements store.FileRepository. If the object store is down and the body is not cached, the error is
// blob.ErrUnavailable, which the caller turns into a 503 (FR-11.14).
func (r *Repository) Read(ctx context.Context, s store.Scope, name string) ([]byte, error) {
	if err := globalOnly(s); err != nil {
		return nil, err
	}
	e, err := r.index.Lookup(ctx, s.Tenant, clean(name))
	if err != nil {
		return nil, fmt.Errorf("file %s in %s: %w", name, s, err)
	}
	if b, ok := r.cache.get(s.Tenant, e.SHA256); ok {
		return b, nil
	}
	b, err := blob.ReadAll(ctx, r.blobs, blob.FileKey(s.Tenant, e.SHA256), nil)
	if err != nil {
		return nil, fmt.Errorf("file %s in %s: %w", name, s, err)
	}
	if sha256.Sum256(b) != e.SHA256 {
		return nil, fmt.Errorf("file %s in %s: %w", name, s, ErrCorrupt)
	}
	r.cache.put(s.Tenant, e.SHA256, b)
	return b, nil
}

// Put implements store.FileRepository. The object is written first, so that the index never names one that is
// missing; a failure between the two leaves an unused object, which the store's lifecycle rules clear.
func (r *Repository) Put(ctx context.Context, s store.Scope, name string, data []byte) error {
	if err := globalOnly(s); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if err := r.blobs.Put(ctx, blob.FileKey(s.Tenant, sum), data, blob.Meta{}); err != nil {
		return fmt.Errorf("file %s in %s: %w", name, s, err)
	}
	if err := r.index.Put(ctx, s.Tenant, clean(name), Entry{SHA256: sum, Size: int64(len(data))}); err != nil {
		return fmt.Errorf("file %s in %s: %w", name, s, err)
	}
	r.cache.put(s.Tenant, sum, data)
	return nil
}

// Delete implements store.FileRepository. The object stays, because another name may use the same bytes.
func (r *Repository) Delete(ctx context.Context, s store.Scope, name string) error {
	if err := globalOnly(s); err != nil {
		return err
	}
	if err := r.index.Delete(ctx, s.Tenant, clean(name)); err != nil {
		return fmt.Errorf("file %s in %s: %w", name, s, err)
	}
	return nil
}

// DeleteAll implements store.FileRepository.
func (r *Repository) DeleteAll(ctx context.Context, s store.Scope) error {
	if err := globalOnly(s); err != nil {
		return err
	}
	if err := r.index.DeleteAll(ctx, s.Tenant); err != nil {
		return fmt.Errorf("files of %s: %w", s, err)
	}
	return nil
}

// List implements store.FileRepository.
func (r *Repository) List(ctx context.Context, s store.Scope) ([]string, error) {
	if err := globalOnly(s); err != nil {
		return nil, err
	}
	names, err := r.index.List(ctx, s.Tenant)
	if err != nil {
		return nil, fmt.Errorf("files of %s: %w", s, err)
	}
	return names, nil
}

// Purge deletes every object of the org and its index entries, for an org that is being deleted (FR-11.15).
func (r *Repository) Purge(ctx context.Context, org tenant.ID) error {
	if err := r.index.DeleteAll(ctx, org); err != nil {
		return fmt.Errorf("purge files of %s: %w", org, err)
	}
	keys, err := r.blobs.List(ctx, blob.FilesPrefix(org))
	if err != nil {
		return fmt.Errorf("purge files of %s: %w", org, err)
	}
	for _, k := range keys {
		if err = r.blobs.Delete(ctx, k); err != nil {
			return fmt.Errorf("purge files of %s: %w", org, err)
		}
	}
	r.cache.forget(org)
	return nil
}
