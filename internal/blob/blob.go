// Package blob holds bulky bytes behind one small interface: file bodies, journal bodies, recordings and
// exports (DESIGN.md §5, NFR-3.1). A driver is a directory (standalone), memory (tests) or S3-compatible object
// storage (the hosted service; R2, AWS S3, MinIO and the like all speak the same protocol). Another mechanism is a
// new package that satisfies Store and passes the suite in blobtest, plus one case in the opener.
//
// The interface asks for only what every object store can do. Keys are opaque to the store, and the engine writes
// file bodies once under a key made from their SHA-256, so a driver needs no versioning, locking or
// read-after-write ordering between different keys.
package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/pwgith/shadow-mock/internal/tenant"
)

var (
	// ErrNotFound is returned when the key holds no object.
	ErrNotFound = errors.New("blob not found")
	// ErrUnavailable is returned when the store cannot be reached or answers with a server error. The engine
	// serves what it has cached and answers 503 object_store_unavailable for the rest (FR-11.14).
	ErrUnavailable = errors.New("blob store unavailable")
	// ErrBadKey is returned for a key that is not allowed.
	ErrBadKey = errors.New("blob key not allowed")
)

// Range is a byte range of an object: Len bytes from offset Off. A Len of zero or less reads to the end.
type Range struct{ Off, Len int64 }

// Meta describes an object being written.
type Meta struct {
	// ContentType is kept by stores that keep one and may be empty.
	ContentType string
}

// Info describes a stored object.
type Info struct {
	Size int64
	// ContentType is what the store kept, which may be empty.
	ContentType string
}

// Store is a place for objects.
type Store interface {
	// Put stores data under key, replacing what was there.
	Put(ctx context.Context, key string, data []byte, meta Meta) error
	// Get returns the object, or the part of it in rng when rng is not nil. The caller closes the reader.
	Get(ctx context.Context, key string, rng *Range) (io.ReadCloser, error)
	// Stat returns the object's size, or ErrNotFound.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete removes the object. A key that holds nothing is not an error.
	Delete(ctx context.Context, key string) error
	// List returns the keys that start with prefix, in order.
	List(ctx context.Context, prefix string) ([]string, error)
}

// MaxKeyLength is the longest key a store has to take. S3 allows 1024 bytes; this leaves room for a prefix.
const MaxKeyLength = 512

var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._=@+-]*$`)

// ValidKey reports whether key is one the engine uses: segments of letters, digits and . _ = @ + - separated by
// slashes, none empty, none ".." and none starting with a dot. Keys are built from ids the engine made, never
// from a request, but every driver checks them so that none of them has to defend against a crafted one.
func ValidKey(key string) error {
	if key == "" || len(key) > MaxKeyLength {
		return fmt.Errorf("%w: length %d", ErrBadKey, len(key))
	}
	for part := range strings.SplitSeq(key, "/") {
		if !segment.MatchString(part) || strings.Contains(part, "..") {
			return fmt.Errorf("%w: %q", ErrBadKey, key)
		}
	}
	return nil
}

// ValidPrefix is ValidKey for a prefix, which may be empty or end in a slash.
func ValidPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	return ValidKey(strings.TrimSuffix(prefix, "/"))
}

// FileKey is where a file body of an org is stored: files/<org>/<sha256>. The same bytes of the same org share
// one object, and the object never changes.
func FileKey(org tenant.ID, sum [sha256.Size]byte) string {
	return FilesPrefix(org) + hex.EncodeToString(sum[:])
}

// FilesPrefix is the prefix of all of an org's file bodies.
func FilesPrefix(org tenant.ID) string { return "files/" + string(org) + "/" }

// ReadAll reads the object, or the part of it in rng.
func ReadAll(ctx context.Context, s Store, key string, rng *Range) ([]byte, error) {
	r, err := s.Get(ctx, key, rng)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w: %w", key, ErrUnavailable, err)
	}
	return b, nil
}
