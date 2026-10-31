package app_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/pwgith/shadow-mock/internal/auth/authtest"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/store"
)

// onRedis points an engine's counters and token-id cache at a Redis. miniredis speaks the real protocol, so this
// is the Redis driver talking to a Redis, not a stub of it.
func onRedis(addr string) func(*config.Config) {
	return func(c *config.Config) {
		c.Storage.Counters.Driver = "redis"
		c.Storage.Redis = config.Redis{Addr: addr, Prefix: "sm:"}
		c.Auth.Issuers[0].ReplayCheck = true
	}
}

func TestTwoEnginesOnOneRedisShareCountersAndTokenIds(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	clk := clock.NewFake(epoch)
	issuer := authtest.NewIssuer("management-app", "https://shadowmock.example", "mocks-test", clk)
	one := newManagedWith(t, clk, issuer, onRedis(mr.Addr()))
	two := newManagedWith(t, clk, issuer, onRedis(mr.Addr()))
	one.org("acme")
	two.org("acme")

	// Call counters count calls to the org, whichever instance gets them.
	key := counter.Key{RequestScope: store.Global("acme"), StubScope: store.Global("acme"), StubID: "s"}
	for i, m := range []*managed{one, two, one, two} {
		n, err := m.app.Service().Counters.Next(t.Context(), key, clk.Now())
		if err != nil || n != int64(i+1) {
			t.Fatalf("call %d on alternating engines = %d, %v: the counter must be shared", i+1, n, err)
		}
	}

	// A token used on one engine is refused on the other.
	tok := issuer.TokenFor("alice", "acme", "member", nil)
	if code, body, _ := one.do("GET", "/acme/__admin/sessions", tok, ""); code != 200 {
		t.Fatalf("first use: %d %s", code, body)
	}
	code, body, _ := two.do("GET", "/acme/__admin/sessions", tok, "")
	if code != 401 || errorOf(body) != "token_replayed" {
		t.Errorf("the same token on the other engine: %d %s, want 401 token_replayed", code, body)
	}
}

func TestReadinessReportsASharedStoreThatIsDown(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	m := newManaged(t, onRedis(mr.Addr()))
	ready := func() (string, map[string]string) {
		_, body, _ := m.do("GET", "/__ready", "", "")
		var h struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}
		if err := json.Unmarshal([]byte(body), &h); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		return h.Status, h.Checks
	}
	if status, checks := ready(); status != "healthy" || checks["redis"] != "up" {
		t.Errorf("with Redis up: %s %v", status, checks)
	}
	mr.Close()
	status, checks := ready()
	if status != "degraded" || checks["redis"] != "down" {
		t.Errorf("with Redis down: %s %v, want degraded and redis down", status, checks)
	}
	// The engine keeps serving: a mock request does not touch Redis unless a mapping counts calls.
	m.org("acme")
	if code, _, _ := m.do("GET", "/acme/nothing", "", ""); code != 404 {
		t.Errorf("a mock request while Redis is down: %d, want the usual 404", code)
	}
}

func TestAnEngineWontStartAgainstARedisThatIsNotThere(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	mr.Close()
	cfg := config.Default()
	cfg.Mode, cfg.Root = "managed", t.TempDir()
	cfg.Managed.SingleInstance = true
	cfg.Admin.GlobalWrites, cfg.Mappings.UnknownFields, cfg.Egress.BlockPrivate = "deny", "keep", true
	issuer := authtest.NewIssuer("m", "https://i.example", "aud", clock.Real{})
	cfg.Auth.Issuers = []config.IssuerConfig{{Name: "m", Issuer: issuer.URL, Audience: "aud", PublicKey: issuer.PublicPEM()}}
	cfg.Storage.Redis = config.Redis{Addr: addr}
	cfg.Storage.Counters.Driver = "redis"
	start := time.Now()
	_, err := newApp(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "storage.redis") {
		t.Errorf("error = %v, want one naming storage.redis", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Error("a missing Redis must fail the start quickly, not hang it")
	}
}
