package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/ids"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/fs"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// SeedFrom names a saved mapping set to copy into a new session (FR-8.7).
type SeedFrom struct {
	MappingSet string `json:"mappingSet"`
}

// CreateSessionRequest is the body of POST /__admin/sessions/key-based and /url-based.
type CreateSessionRequest struct {
	Name       string            `json:"name"`
	TTLSeconds *int64            `json:"ttlSeconds"`
	Tokens     []string          `json:"tokens"`
	Mappings   []json.RawMessage `json:"mappings"`
	Masks      []model.Mask      `json:"masks"`
	ParentID   string            `json:"parentId"`
	SeedFrom   *SeedFrom         `json:"seedFrom"`
	Metadata   map[string]any    `json:"metadata"`
	// CreatedByKey is the label of the Tool app key making the request. The admin handler sets it; a client cannot.
	CreatedByKey string `json:"-"`
}

// CreateSession validates and creates a key-based or URL-based shadow mock, with its initial
// mappings. Nothing is created if any mapping or mask is invalid.
func (s *Service) CreateSession(ctx context.Context, t tenant.ID, kind session.Kind, createdBy string, req CreateSessionRequest) (session.Session, error) {
	if kind == session.URLBased && (len(req.Tokens) > 0 || req.ParentID != "") {
		return session.Session{}, apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed,
			"URL-based shadow mocks have no match tokens and cannot be nested.")
	}
	if err := s.checkSessionRoom(t); err != nil {
		return session.Session{}, err
	}
	mappings, err := s.seedMappings(req)
	if err != nil {
		return session.Session{}, err
	}
	if limit := s.Config.Limits.MaxMappingsPerSession; limit > 0 && len(mappings) > limit {
		return session.Session{}, apierr.New(http.StatusTooManyRequests, apierr.MappingLimitReached,
			"A shadow mock may hold at most %d mappings, and this one starts with %d.", limit, len(mappings))
	}
	if maskErr := s.validateMasks(req.Masks); maskErr != nil {
		return session.Session{}, maskErr
	}
	sess, err := s.Sessions.Create(ctx, t, session.CreateRequest{
		Kind: kind, Name: req.Name, TTLSeconds: req.TTLSeconds, Tokens: req.Tokens, ParentID: req.ParentID,
		Masks: req.Masks, Metadata: req.Metadata, CreatedBy: createdBy, CreatedByKey: req.CreatedByKey,
	})
	if err != nil {
		return session.Session{}, err
	}
	sc := sess.Scope()
	for _, m := range mappings {
		if m.ID == "" {
			m = m.WithID(ids.NewUUID())
		}
		if err := s.Mappings.Put(ctx, sc, m); err != nil {
			return session.Session{}, errors.Join(fmt.Errorf("store mapping: %w", err), s.forget(ctx, []store.Scope{sc}))
		}
	}
	if err := s.Layers.Rebuild(ctx, sc); err != nil {
		return session.Session{}, err
	}
	s.Detector.Invalidate()
	s.persist(ctx)
	s.Logger.InfoContext(ctx, "session created", "session_id", sess.ID, "kind", string(kind), "tenant", string(t))
	s.noteSessions(t)
	return sess, nil
}

func (s *Service) seedMappings(req CreateSessionRequest) ([]model.StubMapping, error) {
	var out []model.StubMapping
	if req.SeedFrom != nil && req.SeedFrom.MappingSet != "" {
		set, err := fs.LoadMappingSet(s.Config.MappingSetsDir, req.SeedFrom.MappingSet)
		if errors.Is(err, store.ErrNotFound) {
			return nil, apierr.New(http.StatusUnprocessableEntity, apierr.ValidationFailed,
				"Mapping set %q was not found.", req.SeedFrom.MappingSet)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, set...)
	}
	for i, raw := range req.Mappings {
		m, err := s.DecodeAndValidate(raw)
		if err != nil {
			return nil, prefixPointer(err, "/mappings/"+strconv.Itoa(i))
		}
		out = append(out, m)
	}
	for i := range out {
		if err := s.Validate(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// prefixPointer moves a validation error's JSON pointer under prefix.
func prefixPointer(err error, prefix string) error {
	var ve *apierr.ValidationError
	if errors.As(err, &ve) {
		for i := range ve.Errors {
			if ve.Errors[i].Source != nil {
				ve.Errors[i].Source.Pointer = prefix + ve.Errors[i].Source.Pointer
			}
		}
	}
	return err
}

func (s *Service) validateMasks(masks []model.Mask) error {
	for i, m := range masks {
		ptr := "/masks/" + strconv.Itoa(i)
		if m.ID == "" && m.Request == nil {
			return apierr.Invalid(ptr, "a mask needs an id or a request pattern")
		}
		if m.Request != nil {
			if _, err := match.Compile(m.Request, s.Layers.Options()); err != nil {
				return apierr.Invalid(ptr+"/request", "%s", err.Error())
			}
		}
	}
	return nil
}

// UpdateSession applies a patch, then refreshes the layer (masks) and detector (tokens).
func (s *Service) UpdateSession(ctx context.Context, t tenant.ID, id string, p session.Patch) (session.Session, error) {
	if err := s.validateMasks(p.AddMasks); err != nil {
		return session.Session{}, err
	}
	sess, err := s.Sessions.Update(ctx, t, id, p)
	if err != nil {
		return session.Session{}, err
	}
	if err := s.Layers.Rebuild(ctx, sess.Scope()); err != nil {
		return session.Session{}, err
	}
	s.Detector.Invalidate()
	s.persist(ctx)
	return sess, nil
}

// KeepAlive extends a session's TTL by its original duration.
func (s *Service) KeepAlive(ctx context.Context, t tenant.ID, id string) (session.Session, error) {
	sess, err := s.Sessions.KeepAlive(ctx, t, id)
	if err == nil {
		s.persist(ctx)
	}
	return sess, err
}

// DeleteSession deletes a session, any sessions nested in it, and everything they own.
func (s *Service) DeleteSession(ctx context.Context, t tenant.ID, id string) error {
	scopes, err := s.Sessions.Remove(ctx, t, id)
	if err != nil {
		return err
	}
	s.Logger.InfoContext(ctx, "session deleted", "session_id", id, "tenant", string(t))
	return s.forget(ctx, scopes)
}

// forget deletes what the scopes own and refreshes the detector.
func (s *Service) forget(ctx context.Context, scopes []store.Scope) error {
	errs := make([]error, 0, 3*len(scopes))
	for _, sc := range scopes {
		errs = append(errs,
			s.Mappings.DeleteAll(ctx, sc),
			s.Files.DeleteAll(ctx, sc),
			s.Counters.ForgetScope(ctx, sc))
		// A session's journal is discarded with it (FR-7.2).
		s.Journal.Remove(sc, nil)
		s.Layers.Drop(sc)
	}
	s.Detector.Invalidate()
	s.persist(ctx)
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("delete session data: %w", err)
	}
	return nil
}

// Reap deletes expired sessions every interval until ctx is done. Expired sessions are
// already invisible to lookups; reaping frees what they own (DESIGN.md §10.2).
func (s *Service) Reap(ctx context.Context, interval time.Duration) {
	clock.Every(ctx, interval, func() { s.ReapOnce(ctx) })
}

// ReapOnce deletes the sessions that have expired by now.
func (s *Service) ReapOnce(ctx context.Context) {
	scopes := s.Sessions.RemoveExpired(ctx)
	if len(scopes) == 0 {
		return
	}
	if err := s.forget(ctx, scopes); err != nil {
		s.Logger.ErrorContext(ctx, "reap expired sessions", "error", err.Error())
	}
	s.Logger.InfoContext(ctx, "expired sessions removed", "count", len(scopes))
}
