package mock

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// checkSessionRoom refuses a new shadow mock when limits.maxSessions are already active.
func (s *Service) checkSessionRoom(t tenant.ID) error {
	limit := s.Config.Limits.MaxSessions
	if limit <= 0 {
		return nil
	}
	active := 0
	for _, x := range s.Sessions.All() {
		if x.Tenant == t {
			active++
		}
	}
	if active >= limit {
		return apierr.New(http.StatusTooManyRequests, apierr.SessionLimitReached,
			"The server already has %d active shadow mocks, which is its limit. Delete one that is no longer needed, or wait for one to expire.", active)
	}
	return nil
}

// checkMappingRoom refuses a change that would leave a scope with more mappings than the limits
// allow. incoming holds the id of each mapping being added, "" for one with no id yet. A mapping
// that replaces an existing one does not count. With replaceAll the incoming mappings take the
// place of everything in the scope.
func (s *Service) checkMappingRoom(ctx context.Context, sc store.Scope, incoming []string, replaceAll bool) error {
	limit, what := s.Config.Limits.MaxMappingsPerSession, "A shadow mock"
	if sc.IsGlobal() {
		limit, what = s.Config.Limits.MaxMappings, "The global session"
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
