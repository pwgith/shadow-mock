package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/pwgith/shadow-mock/internal/ids"
)

// Dir is a Store in a directory, one file for each object. It is the standalone driver. The os.Root it works
// through refuses any path that leaves the directory.
type Dir struct {
	root *os.Root
}

var _ Store = (*Dir)(nil)

// NewDir opens the directory, creating it if needed.
func NewDir(dir string) (*Dir, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create blob directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open blob directory: %w", err)
	}
	return &Dir{root: root}, nil
}

// Close releases the directory.
func (d *Dir) Close() error {
	if err := d.root.Close(); err != nil {
		return fmt.Errorf("close blob directory: %w", err)
	}
	return nil
}

// Put implements Store. The object is written to a temporary name and renamed, so a reader sees all of it or none.
func (d *Dir) Put(_ context.Context, key string, data []byte, _ Meta) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	if err := d.root.MkdirAll(path.Dir(key), 0o750); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	tmp := key + "." + ids.NewUUID() + ".tmp"
	if err := d.root.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if err := d.root.Rename(tmp, key); err != nil {
		_ = d.root.Remove(tmp) //nolint:errcheck // best-effort removal of a temporary file
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return nil
}

// Get implements Store.
func (d *Dir) Get(_ context.Context, key string, rng *Range) (io.ReadCloser, error) {
	if err := ValidKey(key); err != nil {
		return nil, err
	}
	b, err := d.root.ReadFile(key)
	if err != nil {
		return nil, missing(err, key)
	}
	return io.NopCloser(bytes.NewReader(Slice(b, rng))), nil
}

// Stat implements Store.
func (d *Dir) Stat(_ context.Context, key string) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	st, err := d.root.Stat(key)
	if err != nil {
		return Info{}, missing(err, key)
	}
	return Info{Size: st.Size()}, nil
}

// Delete implements Store.
func (d *Dir) Delete(_ context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	if err := d.root.Remove(key); err != nil && !errors.Is(err, iofs.ErrNotExist) {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return nil
}

// List implements Store.
func (d *Dir) List(_ context.Context, prefix string) ([]string, error) {
	if err := ValidPrefix(prefix); err != nil {
		return nil, err
	}
	out := []string{}
	err := iofs.WalkDir(d.root.FS(), ".", func(p string, e iofs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case e.IsDir(), strings.HasSuffix(p, ".tmp"):
			return nil
		case strings.HasPrefix(p, prefix):
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	slices.Sort(out)
	return out, nil
}

func missing(err error, key string) error {
	if errors.Is(err, iofs.ErrNotExist) {
		return fmt.Errorf("%s: %w", key, ErrNotFound)
	}
	return fmt.Errorf("%s: %w: %w", key, ErrUnavailable, err)
}
