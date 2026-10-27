// Package store defines the repository interfaces behind which all persistence sits
// (DESIGN.md §5, NFR-3.1). Drivers live in sub-packages: memory and fs today, postgres,
// redis and s3 for SaaS. The request hot path never calls a repository (NFR-3.3).
package store

import (
	"context"
	"errors"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// ErrNotFound is returned when a mapping, file or session does not exist.
var ErrNotFound = errors.New("not found")

// Scope identifies one layer's data: the tenant plus the session ("" for the global session).
type Scope struct {
	Tenant    tenant.ID
	SessionID string
}

// Global returns the tenant's global scope.
func Global(t tenant.ID) Scope { return Scope{Tenant: t} }

// IsGlobal reports whether the scope is the tenant's global session.
func (s Scope) IsGlobal() bool { return s.SessionID == "" }

// String identifies the scope in logs and errors.
func (s Scope) String() string {
	if s.SessionID == "" {
		return string(s.Tenant) + "/global"
	}
	return string(s.Tenant) + "/" + s.SessionID
}

// MappingRepository stores stub mappings per scope. List returns them in insertion order.
type MappingRepository interface {
	List(ctx context.Context, s Scope) ([]model.StubMapping, error)
	Get(ctx context.Context, s Scope, id string) (model.StubMapping, error)
	// Put creates a mapping, or replaces the one with the same id in place.
	Put(ctx context.Context, s Scope, m model.StubMapping) error
	Delete(ctx context.Context, s Scope, id string) error
	DeleteAll(ctx context.Context, s Scope) error
}

// Resetter restores a scope's mappings to those loaded from storage, dropping mappings
// created through the API (WireMock's /mappings/reset).
type Resetter interface {
	Reset(ctx context.Context, s Scope) error
}

// Saver persists API-created mappings (WireMock's /mappings/save, standalone only).
type Saver interface {
	Save(ctx context.Context, s Scope) error
}

// FileRepository stores response body files (__files) per scope.
type FileRepository interface {
	Read(ctx context.Context, s Scope, name string) ([]byte, error)
	Put(ctx context.Context, s Scope, name string, data []byte) error
	Delete(ctx context.Context, s Scope, name string) error
	DeleteAll(ctx context.Context, s Scope) error
	List(ctx context.Context, s Scope) ([]string, error)
}

// ChangeEvent reports a change made outside the admin API, such as an edited mapping file.
type ChangeEvent struct {
	Kind  string // "mapping" or "file"
	Scope Scope
}

// Watcher reports external changes.
type Watcher interface {
	Watch(ctx context.Context) (<-chan ChangeEvent, error)
}

// SplitMappings routes the global scope and session scopes to different repositories, for
// example fs for global mappings and memory for ephemeral sessions (DESIGN.md §5.1).
type SplitMappings struct {
	Global   MappingRepository
	Sessions MappingRepository
}

var _ MappingRepository = SplitMappings{}

func (r SplitMappings) pick(s Scope) MappingRepository {
	if s.IsGlobal() {
		return r.Global
	}
	return r.Sessions
}

// List implements MappingRepository.
func (r SplitMappings) List(ctx context.Context, s Scope) ([]model.StubMapping, error) {
	return r.pick(s).List(ctx, s)
}

// Get implements MappingRepository.
func (r SplitMappings) Get(ctx context.Context, s Scope, id string) (model.StubMapping, error) {
	return r.pick(s).Get(ctx, s, id)
}

// Put implements MappingRepository.
func (r SplitMappings) Put(ctx context.Context, s Scope, m model.StubMapping) error {
	return r.pick(s).Put(ctx, s, m)
}

// Delete implements MappingRepository.
func (r SplitMappings) Delete(ctx context.Context, s Scope, id string) error {
	return r.pick(s).Delete(ctx, s, id)
}

// DeleteAll implements MappingRepository.
func (r SplitMappings) DeleteAll(ctx context.Context, s Scope) error {
	return r.pick(s).DeleteAll(ctx, s)
}

// Reset resets the scope if its repository supports it, otherwise deletes everything.
func (r SplitMappings) Reset(ctx context.Context, s Scope) error {
	if rs, ok := r.pick(s).(Resetter); ok {
		return rs.Reset(ctx, s)
	}
	return r.pick(s).DeleteAll(ctx, s)
}

// Save saves the scope if its repository supports it.
func (r SplitMappings) Save(ctx context.Context, s Scope) error {
	if sv, ok := r.pick(s).(Saver); ok {
		return sv.Save(ctx, s)
	}
	return nil
}

// SplitFiles routes global and session files to different repositories.
type SplitFiles struct {
	Global   FileRepository
	Sessions FileRepository
}

var _ FileRepository = SplitFiles{}

func (r SplitFiles) pick(s Scope) FileRepository {
	if s.IsGlobal() {
		return r.Global
	}
	return r.Sessions
}

// Read implements FileRepository.
func (r SplitFiles) Read(ctx context.Context, s Scope, name string) ([]byte, error) {
	return r.pick(s).Read(ctx, s, name)
}

// Put implements FileRepository.
func (r SplitFiles) Put(ctx context.Context, s Scope, name string, data []byte) error {
	return r.pick(s).Put(ctx, s, name, data)
}

// Delete implements FileRepository.
func (r SplitFiles) Delete(ctx context.Context, s Scope, name string) error {
	return r.pick(s).Delete(ctx, s, name)
}

// DeleteAll implements FileRepository.
func (r SplitFiles) DeleteAll(ctx context.Context, s Scope) error {
	return r.pick(s).DeleteAll(ctx, s)
}

// List implements FileRepository.
func (r SplitFiles) List(ctx context.Context, s Scope) ([]string, error) {
	return r.pick(s).List(ctx, s)
}
