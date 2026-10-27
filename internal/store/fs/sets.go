package fs

import (
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
)

// LoadMappingSet reads a saved mapping set for seeding a session (FR-8.7): either
// <dir>/<name>.json or every mapping file under <dir>/<name>/.
func LoadMappingSet(dir, name string) ([]model.StubMapping, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("mapping set %s: %w", name, store.ErrNotFound)
	}
	defer root.Close()
	name = path.Clean(name)
	// A name is a set inside dir: not dir itself, and nothing that climbs out of it.
	if name == "." || !iofs.ValidPath(name) {
		return nil, fmt.Errorf("mapping set %s: %w", name, store.ErrNotFound)
	}
	if data, readErr := root.ReadFile(name + ".json"); readErr == nil {
		return model.DecodeMappings(data)
	}
	var files []string
	err = iofs.WalkDir(root.FS(), name, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(p), ".json") {
			files = append(files, p)
		}
		return nil
	})
	if errors.Is(err, iofs.ErrNotExist) || (err == nil && len(files) == 0) {
		return nil, fmt.Errorf("mapping set %s: %w", name, store.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("mapping set %s: %w", name, err)
	}
	sort.Strings(files)
	var out []model.StubMapping
	for _, f := range files {
		data, err := root.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("mapping set %s: %w", name, err)
		}
		ms, err := model.DecodeMappings(data)
		if err != nil {
			return nil, fmt.Errorf("mapping set %s, %s: %w", name, f, err)
		}
		out = append(out, ms...)
	}
	return out, nil
}
