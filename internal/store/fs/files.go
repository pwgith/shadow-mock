package fs

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/pwgith/shadow-mock/internal/store"
)

// Files is a FileRepository over a WireMock __files directory.
type Files struct {
	root *os.Root
}

var _ store.FileRepository = (*Files)(nil)

// NewFiles opens (creating if needed) the files directory.
func NewFiles(dir string) (*Files, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create files directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open files directory: %w", err)
	}
	return &Files{root: root}, nil
}

// Close releases the directory handle.
func (r *Files) Close() error {
	if err := r.root.Close(); err != nil {
		return fmt.Errorf("close files directory: %w", err)
	}
	return nil
}

// name turns a file id into a path inside the root. os.Root rejects any path that escapes it.
func name(n string) string { return path.Clean(strings.TrimPrefix(n, "/")) }

func notFound(err error, n string, s store.Scope) error {
	if errors.Is(err, iofs.ErrNotExist) {
		return fmt.Errorf("file %s in %s: %w", n, s, store.ErrNotFound)
	}
	return fmt.Errorf("file %s: %w", n, err)
}

// Read implements store.FileRepository.
func (r *Files) Read(_ context.Context, s store.Scope, n string) ([]byte, error) {
	b, err := r.root.ReadFile(name(n))
	if err != nil {
		return nil, notFound(err, n, s)
	}
	return b, nil
}

// Put implements store.FileRepository.
func (r *Files) Put(_ context.Context, _ store.Scope, n string, data []byte) error {
	p := name(n)
	if err := r.root.MkdirAll(path.Dir(p), 0o750); err != nil {
		return fmt.Errorf("create folder for %s: %w", n, err)
	}
	if err := r.root.WriteFile(p, data, 0o600); err != nil {
		return fmt.Errorf("write file %s: %w", n, err)
	}
	return nil
}

// Delete implements store.FileRepository.
func (r *Files) Delete(_ context.Context, s store.Scope, n string) error {
	if err := r.root.Remove(name(n)); err != nil {
		return notFound(err, n, s)
	}
	return nil
}

// DeleteAll is not supported for the global __files directory; it is a no-op.
func (r *Files) DeleteAll(context.Context, store.Scope) error { return nil }

// List implements store.FileRepository.
func (r *Files) List(_ context.Context, _ store.Scope) ([]string, error) {
	var out []string
	err := iofs.WalkDir(r.root.FS(), ".", func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	sort.Strings(out)
	return out, nil
}
