// Package fs reads and writes the WireMock file layout: <root>/mappings/**/*.json and
// <root>/__files (FR-1.1, FR-1.2). It serves the global scope; sessions stay in memory.
// All file access goes through os.Root, so names can never escape the directory.
package fs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	iofs "io/fs"
	"log/slog"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/memory"
)

// Mappings is a MappingRepository over a WireMock mappings directory. Mappings created through
// the API are kept in memory unless they are persistent or saved, as in WireMock.
type Mappings struct {
	root   *os.Root
	logger *slog.Logger
	mem    *memory.Mappings

	// namer, when set, names new files from a template instead of the default rule.
	namer Namer

	mu sync.Mutex
	// files maps a file-backed mapping id to the file that defines it, and whether that file
	// holds several mappings.
	files map[string]fileRef
}

type fileRef struct {
	path  string
	multi bool
}

var (
	_ store.MappingRepository = (*Mappings)(nil)
	_ store.Resetter          = (*Mappings)(nil)
	_ store.Saver             = (*Mappings)(nil)
)

// SetNamer makes new mapping files be named by a filename template.
func (r *Mappings) SetNamer(n Namer) { r.namer = n }

// nameFor is the file name for a mapping: from the template when there is one that renders.
func (r *Mappings) nameFor(m model.StubMapping) string {
	if r.namer != nil {
		if out, err := r.namer(FilenameData(m)); err == nil {
			if name := slug(out); name != "" {
				return name
			}
		}
	}
	return fileName(m)
}

// NewMappings opens (creating if needed) the mappings directory.
func NewMappings(dir string, logger *slog.Logger) (*Mappings, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create mappings directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open mappings directory: %w", err)
	}
	return &Mappings{root: root, logger: logger, mem: memory.NewMappings(), files: map[string]fileRef{}}, nil
}

// Close releases the directory handle.
func (r *Mappings) Close() error {
	if err := r.root.Close(); err != nil {
		return fmt.Errorf("close mappings directory: %w", err)
	}
	return nil
}

// Load reads every mapping file into scope s, replacing previously loaded file mappings and
// keeping mappings created through the API. Invalid files are logged and skipped; mappings
// with scenario fields are reported and never loaded (FR-4.4).
func (r *Mappings) Load(ctx context.Context, s store.Scope) error {
	loaded, refs, err := r.readAll(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := r.mem.List(ctx, s)
	if err != nil {
		return fmt.Errorf("list mappings: %w", err)
	}
	if err := r.mem.DeleteAll(ctx, s); err != nil {
		return fmt.Errorf("clear mappings: %w", err)
	}
	for _, m := range loaded {
		if err := r.mem.Put(ctx, s, m); err != nil {
			return fmt.Errorf("load mapping %s: %w", m.ID, err)
		}
	}
	for _, m := range current {
		if _, fromFile := r.files[m.ID]; fromFile {
			continue
		}
		if _, reloaded := refs[m.ID]; reloaded {
			continue
		}
		if err := r.mem.Put(ctx, s, m); err != nil {
			return fmt.Errorf("keep mapping %s: %w", m.ID, err)
		}
	}
	r.files = refs
	return nil
}

func (r *Mappings) readAll(ctx context.Context) ([]model.StubMapping, map[string]fileRef, error) {
	var names []string
	err := iofs.WalkDir(r.root.FS(), ".", func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(p), ".json") {
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan mappings directory: %w", err)
	}
	sort.Strings(names)
	var out []model.StubMapping
	refs := map[string]fileRef{}
	for _, name := range names {
		ms, err := r.readFile(name)
		if err != nil {
			r.logger.ErrorContext(ctx, "skipping mapping file", "file", "mappings/"+name, "error", err.Error())
			continue
		}
		for i, m := range ms {
			if m.ID == "" {
				m = m.WithID(stableID(name, i))
			}
			refs[m.ID] = fileRef{path: name, multi: len(ms) > 1}
			out = append(out, m)
		}
	}
	return out, refs, nil
}

func (r *Mappings) readFile(name string) ([]model.StubMapping, error) {
	data, err := r.root.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return model.DecodeMappings(data)
}

// stableID derives a UUID from the file and position, so a mapping without an id keeps the
// same id across reloads and can be overridden by id from a session (FR-7.7).
func stableID(file string, index int) string {
	sum := sha256.Sum256([]byte(file + "#" + strconv.Itoa(index)))
	h := hex.EncodeToString(sum[:16])
	return h[0:8] + "-" + h[8:12] + "-5" + h[13:16] + "-a" + h[17:20] + "-" + h[20:32]
}

// List implements store.MappingRepository.
func (r *Mappings) List(ctx context.Context, s store.Scope) ([]model.StubMapping, error) {
	return r.mem.List(ctx, s)
}

// Get implements store.MappingRepository.
func (r *Mappings) Get(ctx context.Context, s store.Scope, id string) (model.StubMapping, error) {
	return r.mem.Get(ctx, s, id)
}

// Put implements store.MappingRepository. Persistent mappings are written to disk at once.
func (r *Mappings) Put(ctx context.Context, s store.Scope, m model.StubMapping) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.mem.Put(ctx, s, m); err != nil {
		return fmt.Errorf("put mapping: %w", err)
	}
	ref, fromFile := r.files[m.ID]
	ownFile := fromFile && !ref.multi
	if !m.Persistent && !ownFile {
		return nil
	}
	return r.write(m)
}

// Delete implements store.MappingRepository. A mapping with a file of its own loses the file.
func (r *Mappings) Delete(ctx context.Context, s store.Scope, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.mem.Delete(ctx, s, id); err != nil {
		return fmt.Errorf("delete mapping: %w", err)
	}
	ref, ok := r.files[id]
	if !ok || ref.multi {
		return nil
	}
	delete(r.files, id)
	if err := r.root.Remove(ref.path); err != nil && !errors.Is(err, iofs.ErrNotExist) {
		return fmt.Errorf("remove mapping file %s: %w", ref.path, err)
	}
	return nil
}

// DeleteAll implements store.MappingRepository. Files stay on disk, so Reset restores them.
func (r *Mappings) DeleteAll(ctx context.Context, s store.Scope) error {
	return r.mem.DeleteAll(ctx, s)
}

// Reset drops API-created mappings and reloads the files (WireMock's /mappings/reset).
func (r *Mappings) Reset(ctx context.Context, s store.Scope) error {
	if err := r.mem.DeleteAll(ctx, s); err != nil {
		return fmt.Errorf("reset mappings: %w", err)
	}
	return r.Load(ctx, s)
}

// Save writes every API-created mapping to its own file (WireMock's /mappings/save).
func (r *Mappings) Save(ctx context.Context, s store.Scope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	ms, err := r.mem.List(ctx, s)
	if err != nil {
		return fmt.Errorf("list mappings: %w", err)
	}
	for _, m := range ms {
		if _, ok := r.files[m.ID]; ok {
			continue
		}
		if err := r.write(m); err != nil {
			return err
		}
	}
	return nil
}

// write saves m to its own file, or to the single-mapping file it came from. Callers hold mu.
func (r *Mappings) write(m model.StubMapping) error {
	name := r.nameFor(m)
	if ref, ok := r.files[m.ID]; ok && !ref.multi {
		name = ref.path
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mapping %s: %w", m.ID, err)
	}
	if err := r.root.MkdirAll(path.Dir(name), 0o750); err != nil {
		return fmt.Errorf("create mapping folder: %w", err)
	}
	if err := r.root.WriteFile(name, data, 0o600); err != nil {
		return fmt.Errorf("write mapping file %s: %w", name, err)
	}
	r.files[m.ID] = fileRef{path: name}
	return nil
}
