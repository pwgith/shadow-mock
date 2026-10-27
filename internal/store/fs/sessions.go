package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"time"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/memory"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// sessionsFile holds the registry when sessions are persisted (storage.sessions.driver: fs).
const sessionsFile = "sessions.json"

// SessionStore persists sessions and their mappings under one directory, so they survive a
// restart (DESIGN.md §5.1). Session keys are stored as they are: this is a local developer
// store; SaaS keeps only hashes.
type SessionStore struct {
	root *os.Root
	mem  *memory.Mappings
}

var _ store.MappingRepository = (*SessionStore)(nil)

// storedSession is the on-disk form of a session.
type storedSession struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Tenant    string         `json:"tenant"`
	Key       string         `json:"key,omitempty"`
	Tokens    []string       `json:"tokens,omitempty"`
	ParentID  string         `json:"parentId,omitempty"`
	Name      string         `json:"name,omitempty"`
	CreatedBy string         `json:"createdBy"`
	CreatedAt time.Time      `json:"createdAt"`
	ExpiresAt time.Time      `json:"expiresAt"`
	TTL       int64          `json:"ttlSeconds"`
	Masks     []model.Mask   `json:"masks,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// NewSessionStore opens (creating if needed) the sessions directory.
func NewSessionStore(dir string) (*SessionStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create sessions directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open sessions directory: %w", err)
	}
	return &SessionStore{root: root, mem: memory.NewMappings()}, nil
}

// Close releases the directory handle.
func (s *SessionStore) Close() error {
	if err := s.root.Close(); err != nil {
		return fmt.Errorf("close sessions directory: %w", err)
	}
	return nil
}

// SaveSessions writes the registry.
func (s *SessionStore) SaveSessions(sessions []session.Session) error {
	out := make([]storedSession, 0, len(sessions))
	for i := range sessions {
		x := &sessions[i]
		out = append(out, storedSession{
			ID: x.ID, Kind: string(x.Kind), Tenant: string(x.Tenant), Key: x.Key, Tokens: x.Tokens, ParentID: x.ParentID,
			Name: x.Name, CreatedBy: x.CreatedBy, CreatedAt: x.CreatedAt, ExpiresAt: x.ExpiresAt,
			TTL: int64(x.TTL / time.Second), Masks: x.Masks, Metadata: x.Metadata,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sessions: %w", err)
	}
	if err := s.root.WriteFile(sessionsFile, data, 0o600); err != nil {
		return fmt.Errorf("write sessions: %w", err)
	}
	return nil
}

// LoadSessions reads the registry and every session's mappings.
func (s *SessionStore) LoadSessions(ctx context.Context) ([]session.Session, error) {
	data, err := s.root.ReadFile(sessionsFile)
	if errors.Is(err, iofs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	var stored []storedSession
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse sessions: %w", err)
	}
	out := make([]session.Session, 0, len(stored))
	for i := range stored {
		x := &stored[i]
		sess := session.Session{
			ID: x.ID, Kind: session.Kind(x.Kind), Tenant: tenant.ID(x.Tenant), Key: x.Key, Tokens: x.Tokens, ParentID: x.ParentID,
			Name: x.Name, CreatedBy: x.CreatedBy, CreatedAt: x.CreatedAt, ExpiresAt: x.ExpiresAt,
			TTL: time.Duration(x.TTL) * time.Second, Masks: x.Masks, Metadata: x.Metadata,
		}
		if err := s.loadMappings(ctx, sess.Scope()); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

func mappingsFile(sc store.Scope) string { return sc.SessionID + "/mappings.json" }

func (s *SessionStore) loadMappings(ctx context.Context, sc store.Scope) error {
	data, err := s.root.ReadFile(mappingsFile(sc))
	if errors.Is(err, iofs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read mappings of %s: %w", sc, err)
	}
	ms, err := model.DecodeMappings(data)
	if err != nil {
		return fmt.Errorf("parse mappings of %s: %w", sc, err)
	}
	for _, m := range ms {
		if err := s.mem.Put(ctx, sc, m); err != nil {
			return fmt.Errorf("load mapping %s: %w", m.ID, err)
		}
	}
	return nil
}

// flush writes the scope's mappings after a change.
func (s *SessionStore) flush(ctx context.Context, sc store.Scope) error {
	ms, err := s.mem.List(ctx, sc)
	if err != nil {
		return fmt.Errorf("list mappings: %w", err)
	}
	if len(ms) == 0 {
		if rmErr := s.root.RemoveAll(sc.SessionID); rmErr != nil {
			return fmt.Errorf("remove mappings of %s: %w", sc, rmErr)
		}
		return nil
	}
	data, err := json.MarshalIndent(struct {
		Mappings []model.StubMapping `json:"mappings"`
	}{ms}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mappings: %w", err)
	}
	if err := s.root.MkdirAll(sc.SessionID, 0o750); err != nil {
		return fmt.Errorf("create session folder: %w", err)
	}
	if err := s.root.WriteFile(mappingsFile(sc), data, 0o600); err != nil {
		return fmt.Errorf("write mappings of %s: %w", sc, err)
	}
	return nil
}

// List implements store.MappingRepository.
func (s *SessionStore) List(ctx context.Context, sc store.Scope) ([]model.StubMapping, error) {
	return s.mem.List(ctx, sc)
}

// Get implements store.MappingRepository.
func (s *SessionStore) Get(ctx context.Context, sc store.Scope, id string) (model.StubMapping, error) {
	return s.mem.Get(ctx, sc, id)
}

// Put implements store.MappingRepository.
func (s *SessionStore) Put(ctx context.Context, sc store.Scope, m model.StubMapping) error {
	if err := s.mem.Put(ctx, sc, m); err != nil {
		return fmt.Errorf("put mapping: %w", err)
	}
	return s.flush(ctx, sc)
}

// Delete implements store.MappingRepository.
func (s *SessionStore) Delete(ctx context.Context, sc store.Scope, id string) error {
	if err := s.mem.Delete(ctx, sc, id); err != nil {
		return fmt.Errorf("delete mapping: %w", err)
	}
	return s.flush(ctx, sc)
}

// DeleteAll implements store.MappingRepository.
func (s *SessionStore) DeleteAll(ctx context.Context, sc store.Scope) error {
	if err := s.mem.DeleteAll(ctx, sc); err != nil {
		return fmt.Errorf("delete mappings: %w", err)
	}
	return s.flush(ctx, sc)
}
