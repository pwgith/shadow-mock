package blob_test

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/blob"
	"github.com/pwgith/shadow-mock/internal/blob/blobtest"
)

func TestMemoryPassesTheSuite(t *testing.T) {
	t.Parallel()
	blobtest.Run(t, func(*testing.T) blob.Store { return blob.NewMemory() })
}

func TestDirPassesTheSuite(t *testing.T) {
	t.Parallel()
	blobtest.Run(t, func(t *testing.T) blob.Store {
		t.Helper()
		d, err := blob.NewDir(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		return d
	})
}

func TestFileKey(t *testing.T) {
	t.Parallel()
	key := blob.FileKey("acme", sha256.Sum256([]byte("hello")))
	want := "files/acme/2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if key != want {
		t.Errorf("key = %s, want %s", key, want)
	}
	if err := blob.ValidKey(key); err != nil {
		t.Errorf("the engine's own key is refused: %v", err)
	}
	if !strings.HasPrefix(key, blob.FilesPrefix("acme")) {
		t.Errorf("key %s is not under its org's prefix", key)
	}
}
