package redis_test

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/countertest"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/store/redis"
)

// newServer starts miniredis, a Redis server written in Go that speaks the real protocol, and a client for it.
func newServer(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	c := redis.New(rdb, "test:")
	t.Cleanup(func() { _ = c.Close() })
	return mr, c
}

func TestCountersPassTheSuite(t *testing.T) {
	t.Parallel()
	countertest.Run(t, func(t *testing.T) counter.Counters {
		t.Helper()
		_, c := newServer(t)
		return c.Counters()
	})
}

func TestCountersRefuseIdsThatBreakKeys(t *testing.T) {
	t.Parallel()
	_, c := newServer(t)
	k := counter.Key{RequestScope: store.Global("acme"), StubScope: store.Global("acme"), StubID: "a|b"}
	if _, err := c.Counters().Next(t.Context(), k, time.Now()); err == nil {
		t.Error("a stub id with a field separator must be refused")
	}
	k = counter.Key{RequestScope: store.Global("ac*me"), StubID: "x"}
	if _, err := c.Counters().Peek(t.Context(), k); err == nil {
		t.Error("an org with a glob character must be refused")
	}
}

func TestCountersShareStateBetweenInstances(t *testing.T) {
	t.Parallel()
	mr, first := newServer(t)
	second := redis.New(goredis.NewClient(&goredis.Options{Addr: mr.Addr()}), "test:")
	t.Cleanup(func() { _ = second.Close() })
	k := counter.Key{RequestScope: store.Global("acme"), StubScope: store.Global("acme"), StubID: "s"}
	for i := int64(1); i <= 4; i++ {
		who := first
		if i%2 == 0 {
			who = second
		}
		n, err := who.Counters().Next(t.Context(), k, time.Now())
		if err != nil || n != i {
			t.Fatalf("call %d on alternating instances = %d, %v", i, n, err)
		}
	}
}

func TestACountersFailureIsAnError(t *testing.T) {
	t.Parallel()
	mr, c := newServer(t)
	mr.Close()
	k := counter.Key{RequestScope: store.Global("acme"), StubScope: store.Global("acme"), StubID: "s"}
	if _, err := c.Counters().Next(t.Context(), k, time.Now()); err == nil {
		t.Error("a counter that cannot be reached must say so, not answer 1")
	}
	if err := c.Ping(t.Context()); err == nil {
		t.Error("ping of a server that is down must fail")
	}
}

func TestReplayIsSharedAndExpires(t *testing.T) {
	t.Parallel()
	mr, c := newServer(t)
	clk := clock.NewFake(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC))
	var failures []error
	r := c.Replay(clk, func(err error) { failures = append(failures, err) })
	until := clk.Now().Add(2 * time.Minute)
	if r.Seen(t.Context(), "mgmt", "jti-1", until) {
		t.Fatal("the first use of a token id must pass")
	}
	if !r.Seen(t.Context(), "mgmt", "jti-1", until) {
		t.Error("the second use must be refused")
	}
	if r.Seen(t.Context(), "mgmt", "jti-2", until) || r.Seen(t.Context(), "other", "jti-1", until) {
		t.Error("another id, or another issuer's id, is a different token")
	}
	other := redis.New(goredis.NewClient(&goredis.Options{Addr: mr.Addr()}), "test:").Replay(clk, nil)
	if !other.Seen(t.Context(), "mgmt", "jti-1", until) {
		t.Error("a second instance must see the first one's use")
	}
	mr.FastForward(3 * time.Minute)
	if r.Seen(t.Context(), "mgmt", "jti-1", clk.Now().Add(time.Minute)) {
		t.Error("after the token has expired its id is forgotten")
	}
	if len(failures) != 0 {
		t.Errorf("unexpected failures: %v", failures)
	}
}

func TestReplayFailsClosed(t *testing.T) {
	t.Parallel()
	mr, c := newServer(t)
	clk := clock.NewFake(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC))
	var got error
	r := c.Replay(clk, func(err error) { got = err })
	mr.Close()
	if !r.Seen(t.Context(), "mgmt", "jti-9", clk.Now().Add(time.Minute)) {
		t.Error("with Redis down the token must be refused")
	}
	if got == nil {
		t.Error("the failure must be reported")
	}
}

func TestBusDeliversChanges(t *testing.T) {
	t.Parallel()
	mr, pub := newServer(t)
	sub := redis.New(goredis.NewClient(&goredis.Options{Addr: mr.Addr()}), "test:")
	t.Cleanup(func() { _ = sub.Close() })
	ch, err := sub.Bus().Watch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := store.ChangeEvent{Kind: "mapping", Scope: store.Scope{Tenant: "acme", SessionID: "ses_1"}}
	if err = pub.Bus().Publish(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ch:
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	case <-t.Context().Done():
		t.Fatal("no notice arrived")
	}
}
