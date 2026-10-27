package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/engine"
	"github.com/pwgith/shadow-mock/internal/ids"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
)

// Validate checks that a decoded mapping compiles with the engine's own matchers.
func (s *Service) Validate(m *model.StubMapping) error {
	if _, err := engine.CompileMapping(m, s.Layers.Options()); err != nil {
		return apierr.Invalid("/request", "%s", err.Error())
	}
	return nil
}

func metadataMatches(cm match.ContentMatcher, metadata map[string]any) bool {
	b, err := json.Marshal(metadata)
	if err != nil || metadata == nil {
		b = []byte("{}")
	}
	return cm.MatchContent(match.ContentOf(b)).Matched
}

// DecodeAndValidate parses a mapping and compiles it.
func (s *Service) DecodeAndValidate(data []byte) (model.StubMapping, error) {
	m, err := model.DecodeStubMapping(data)
	if err != nil {
		return model.StubMapping{}, err
	}
	if err := s.Validate(&m); err != nil {
		return model.StubMapping{}, err
	}
	return m, nil
}

// PutMapping creates a mapping, or replaces the one with the same id. A replaced mapping's
// call counters restart (FR-4.2).
func (s *Service) PutMapping(ctx context.Context, sc store.Scope, m model.StubMapping) (model.StubMapping, error) {
	if m.ID == "" {
		m = m.WithID(ids.NewUUID())
	}
	if err := s.Mappings.Put(ctx, sc, m); err != nil {
		return model.StubMapping{}, fmt.Errorf("store mapping: %w", err)
	}
	if err := s.Counters.ForgetStub(ctx, sc, m.ID); err != nil {
		return model.StubMapping{}, fmt.Errorf("reset counters: %w", err)
	}
	return m, s.Layers.Rebuild(ctx, sc)
}

// ReplaceMapping replaces an existing mapping, keeping its id.
func (s *Service) ReplaceMapping(ctx context.Context, sc store.Scope, id string, m model.StubMapping) (model.StubMapping, error) {
	if _, err := s.Mappings.Get(ctx, sc, id); err != nil {
		return model.StubMapping{}, storeErr(err, "Mapping "+id)
	}
	return s.PutMapping(ctx, sc, m.WithID(id))
}

// GetMapping returns one mapping.
func (s *Service) GetMapping(ctx context.Context, sc store.Scope, id string) (model.StubMapping, error) {
	m, err := s.Mappings.Get(ctx, sc, id)
	if err != nil {
		return model.StubMapping{}, storeErr(err, "Mapping "+id)
	}
	return m, nil
}

// ListMappings returns the scope's mappings, most recently added first.
func (s *Service) ListMappings(ctx context.Context, sc store.Scope) ([]model.StubMapping, error) {
	ms, err := s.Mappings.List(ctx, sc)
	if err != nil {
		return nil, fmt.Errorf("list mappings: %w", err)
	}
	slices.Reverse(ms)
	return ms, nil
}

// DeleteMapping deletes one mapping and its counters.
func (s *Service) DeleteMapping(ctx context.Context, sc store.Scope, id string) error {
	if err := s.Mappings.Delete(ctx, sc, id); err != nil {
		return storeErr(err, "Mapping "+id)
	}
	if err := s.Counters.ForgetStub(ctx, sc, id); err != nil {
		return fmt.Errorf("reset counters: %w", err)
	}
	return s.Layers.Rebuild(ctx, sc)
}

// DeleteAllMappings deletes every mapping in the scope.
func (s *Service) DeleteAllMappings(ctx context.Context, sc store.Scope) error {
	if err := s.Mappings.DeleteAll(ctx, sc); err != nil {
		return fmt.Errorf("delete mappings: %w", err)
	}
	return s.Layers.Rebuild(ctx, sc)
}

// ResetMappings restores the scope's mappings to the stored ones (WireMock's /mappings/reset).
func (s *Service) ResetMappings(ctx context.Context, sc store.Scope) error {
	if err := s.Mappings.Reset(ctx, sc); err != nil {
		return fmt.Errorf("reset mappings: %w", err)
	}
	return s.Layers.Rebuild(ctx, sc)
}

// SaveMappings persists API-created mappings (standalone only).
func (s *Service) SaveMappings(ctx context.Context, sc store.Scope) error {
	if s.Config.SaaS {
		return apierr.New(http.StatusNotFound, apierr.NotAvailableInSaaS, "Saving mappings to files is not available in SaaS.")
	}
	if err := s.Mappings.Save(ctx, sc); err != nil {
		return fmt.Errorf("save mappings: %w", err)
	}
	return nil
}

// ImportOptions are WireMock's import options.
type ImportOptions struct {
	DuplicatePolicy      string `json:"duplicatePolicy"`
	DeleteAllNotInImport bool   `json:"deleteAllNotInImport"`
}

// Import adds several mappings. All are validated before any is stored.
func (s *Service) Import(ctx context.Context, sc store.Scope, ms []model.StubMapping, o ImportOptions) error {
	for i := range ms {
		if err := s.Validate(&ms[i]); err != nil {
			return err
		}
	}
	if o.DeleteAllNotInImport {
		if err := s.Mappings.DeleteAll(ctx, sc); err != nil {
			return fmt.Errorf("delete mappings: %w", err)
		}
	}
	for _, m := range ms {
		if m.ID != "" && o.DuplicatePolicy == "IGNORE" {
			if _, err := s.Mappings.Get(ctx, sc, m.ID); err == nil {
				continue
			} else if !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("import: %w", err)
			}
		}
		if m.ID == "" {
			m = m.WithID(ids.NewUUID())
		}
		if err := s.Mappings.Put(ctx, sc, m); err != nil {
			return fmt.Errorf("import: %w", err)
		}
		if err := s.Counters.ForgetStub(ctx, sc, m.ID); err != nil {
			return fmt.Errorf("import: %w", err)
		}
	}
	return s.Layers.Rebuild(ctx, sc)
}

// FindByMetadata returns the mappings whose metadata matches a content pattern.
func (s *Service) FindByMetadata(ctx context.Context, sc store.Scope, p model.ValuePattern) ([]model.StubMapping, error) {
	cm, err := match.CompileContent(p, s.Layers.Options())
	if err != nil {
		return nil, apierr.Invalid("", "%s", err.Error())
	}
	all, err := s.ListMappings(ctx, sc)
	if err != nil {
		return nil, err
	}
	var out []model.StubMapping
	for _, m := range all {
		if metadataMatches(cm, m.Metadata) {
			out = append(out, m)
		}
	}
	return out, nil
}

// RemoveByMetadata deletes the mappings whose metadata matches a content pattern.
func (s *Service) RemoveByMetadata(ctx context.Context, sc store.Scope, p model.ValuePattern) error {
	found, err := s.FindByMetadata(ctx, sc, p)
	if err != nil {
		return err
	}
	for _, m := range found {
		if err := s.Mappings.Delete(ctx, sc, m.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("remove mapping %s: %w", m.ID, err)
		}
	}
	return s.Layers.Rebuild(ctx, sc)
}

// UnmatchedMappings returns the scope's mappings that no journal entry was served by.
func (s *Service) UnmatchedMappings(ctx context.Context, sc store.Scope) ([]model.StubMapping, error) {
	all, err := s.ListMappings(ctx, sc)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, e := range s.Journal.Query(sc, nil, 0) {
		if e.Stub != nil && e.StubScope == sc {
			used[e.Stub.ID] = true
		}
	}
	out := make([]model.StubMapping, 0, len(all))
	for _, m := range all {
		if !used[m.ID] {
			out = append(out, m)
		}
	}
	return out, nil
}
