package mock

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/session"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// checkSessionRoom refuses a new shadow mock when the org's plan, or else limits.maxSessions, allows no more active
// ones. A smaller plan deletes nothing: it only refuses the next one.
func (s *Service) checkSessionRoom(t tenant.ID) error {
	limit, who := s.Config.Limits.MaxSessions, "The server"
	if o := s.org(t); o != nil && o.Limits.MaxSessions > 0 {
		limit, who = o.Limits.MaxSessions, "The org's plan"
	}
	if limit <= 0 {
		return nil
	}
	if active := s.activeSessions(t); active >= limit {
		return apierr.New(http.StatusTooManyRequests, apierr.SessionLimitReached,
			"%s allows %d active shadow mocks and %d are active. Delete one that is no longer needed, or wait for one to expire.", who, limit, active)
	}
	return nil
}

func (s *Service) activeSessions(t tenant.ID) int {
	active := 0
	for _, x := range s.Sessions.All() {
		if x.Tenant == t {
			active++
		}
	}
	return active
}

// UsageWarning returns the reason to warn the caller of a shadow mock that has just been made, or "". An org beyond
// the sessions its plan includes is allowed on, up to the plan's ceiling, and is told so (NFR-4.2).
func (s *Service) UsageWarning(t tenant.ID) string {
	if o := s.org(t); o != nil && o.Limits.IncludedSessions > 0 && s.activeSessions(t) > o.Limits.IncludedSessions {
		return "sessions_over_included"
	}
	return ""
}

// planMaxTTL is the longest a shadow mock of this kind may live on the org's plan, or zero when the plan sets none.
func (s *Service) planMaxTTL(t tenant.ID, kind session.Kind) time.Duration {
	if o := s.org(t); o != nil && kind == session.URLBased {
		return o.Limits.MaxURLTTL
	}
	return 0
}

// mappingsPerSessionLimit is how many mappings a shadow mock of the org may hold: its plan's limit, else the server's.
func (s *Service) mappingsPerSessionLimit(t tenant.ID) int {
	if o := s.org(t); o != nil && o.Limits.MaxMappingsPerSession > 0 {
		return o.Limits.MaxMappingsPerSession
	}
	return s.Config.Limits.MaxMappingsPerSession
}

// globalMappingsLimit is how many mappings the org's global layer may hold.
func (s *Service) globalMappingsLimit(t tenant.ID) int {
	if o := s.org(t); o != nil && o.Limits.MaxMappings > 0 {
		return o.Limits.MaxMappings
	}
	return s.Config.Limits.MaxMappings
}

// checkMappingRoom refuses a change that would leave a scope with more mappings than the limits
// allow. incoming holds the id of each mapping being added, "" for one with no id yet. A mapping
// that replaces an existing one does not count. With replaceAll the incoming mappings take the
// place of everything in the scope.
func (s *Service) checkMappingRoom(ctx context.Context, sc store.Scope, incoming []string, replaceAll bool) error {
	limit, what := s.mappingsPerSessionLimit(sc.Tenant), "A shadow mock"
	if sc.IsGlobal() {
		limit, what = s.globalMappingsLimit(sc.Tenant), "The global session"
	}
	if limit <= 0 {
		return nil
	}
	total := len(incoming)
	if !replaceAll {
		existing, err := s.Mappings.List(ctx, sc)
		if err != nil {
			return fmt.Errorf("count mappings: %w", err)
		}
		have := make(map[string]bool, len(existing))
		for _, m := range existing {
			have[m.ID] = true
		}
		total = len(existing)
		for _, id := range incoming {
			if id == "" || !have[id] {
				total++
			}
		}
	}
	if total > limit {
		return apierr.New(http.StatusTooManyRequests, apierr.MappingLimitReached,
			"%s may hold at most %d mappings, and this would make %d.", what, limit, total)
	}
	return nil
}

// checkDelays refuses a mapping whose delays are longer than limits.maxDelay. A delay that cannot be
// known up front, such as a log-normal one with no maxValue, is shortened when it is served.
func (s *Service) checkDelays(m *model.StubMapping) error {
	limit := s.Config.Limits.MaxDelay
	if limit <= 0 {
		return nil
	}
	check := func(where string, d *model.ResponseDefinition) error {
		if d == nil {
			return nil
		}
		for what, ms := range definiteDelays(d) {
			if time.Duration(ms)*time.Millisecond > limit {
				return apierr.Invalid(where, "%s is %d ms, longer than this server's limit of %s", what, ms, limit)
			}
		}
		return nil
	}
	if err := check("/response", &m.Response); err != nil {
		return err
	}
	for i := range m.CallResponses {
		if err := check(fmt.Sprintf("/callResponses/%d/response", i), &m.CallResponses[i].Response); err != nil {
			return err
		}
	}
	return nil
}

// definiteDelays returns the longest delay each delay setting of a response can produce, where it
// is known without sampling.
func definiteDelays(d *model.ResponseDefinition) map[string]int64 {
	out := map[string]int64{}
	if d.FixedDelayMilliseconds > 0 {
		out["fixedDelayMilliseconds"] = d.FixedDelayMilliseconds
	}
	if dd := d.DelayDistribution; dd != nil {
		switch dd.Type {
		case "uniform":
			out["delayDistribution.upper"] = dd.Upper
		case "fixed":
			out["delayDistribution.milliseconds"] = dd.Milliseconds
		case "lognormal":
			if dd.MaxValue > 0 {
				out["delayDistribution.maxValue"] = dd.MaxValue
			}
		}
	}
	if d.ChunkedDribbleDelay != nil {
		out["chunkedDribbleDelay.totalDuration"] = d.ChunkedDribbleDelay.TotalDuration
	}
	return out
}
