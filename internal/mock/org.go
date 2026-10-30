package mock

import (
	"context"
	"errors"
	"fmt"

	"github.com/pwgith/shadow-mock/internal/audit"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// noteSessions tells the usage meter how many shadow mocks the org has active, for the busiest moment of the hour.
func (s *Service) noteSessions(t tenant.ID) {
	if s.Usage != nil {
		s.Usage.Sessions(t, len(s.Sessions.List(t, "")))
	}
}

// PurgeOrg deletes the runtime state the engine holds for an org: its shadow mocks and what they own, its global
// journal and counters, its stats and its usage (FR-11.15). Configuration is not touched: the management app
// removes that. It can be run again, and it reports what it removed.
func (s *Service) PurgeOrg(ctx context.Context, t tenant.ID) (PurgeResult, error) {
	var res PurgeResult
	all := s.Sessions.ListAll(t)
	for i := range all {
		if _, err := s.Sessions.Remove(ctx, t, all[i].ID); err != nil {
			continue // it went in the meantime
		}
		res.Sessions++
	}
	scopes := make([]store.Scope, 0, len(all)+1)
	for i := range all {
		scopes = append(scopes, all[i].Scope())
	}
	global := store.Global(t)
	res.JournalEntries = s.Journal.Count(global, nil)
	scopes = append(scopes, global)
	errs := []error{s.forgetRuntime(ctx, scopes)}
	if s.Recorder != nil {
		s.Recorder.Stop(global)
	}
	s.Stats.Forget(t)
	if s.Usage != nil {
		s.Usage.Forget(t)
	}
	s.Detector.Invalidate()
	s.persist(ctx)
	if err := errors.Join(errs...); err != nil {
		return res, fmt.Errorf("purge org %s: %w", t, err)
	}
	return res, nil
}

// PurgeResult says what a purge removed.
type PurgeResult struct {
	Sessions       int `json:"sessions"`
	JournalEntries int `json:"journalEntries"`
}

// forgetRuntime deletes the journal and counters of the scopes, and the mappings and files only a session owns.
func (s *Service) forgetRuntime(ctx context.Context, scopes []store.Scope) error {
	var errs []error
	for _, sc := range scopes {
		s.Journal.Remove(sc, nil)
		errs = append(errs, s.Counters.ForgetScope(ctx, sc))
		if !sc.IsGlobal() {
			errs = append(errs, s.Mappings.DeleteAll(ctx, sc), s.Files.DeleteAll(ctx, sc))
		}
	}
	return errors.Join(errs...)
}

// AnonymisePerson replaces a person as the creator of the org's shadow mocks with the anonymous marker, and
// returns how many it changed.
func (s *Service) AnonymisePerson(ctx context.Context, t tenant.ID, person string) int {
	n := s.Sessions.Anonymise(t, person, audit.Anonymous)
	if n > 0 {
		s.persist(ctx)
	}
	return n
}
