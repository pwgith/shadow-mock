package ratelimit_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/ratelimit"
)

var start = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

func allowed(l *ratelimit.Limiter) int {
	ok := 0
	for range 10 {
		if a, _ := l.Allow("a"); a {
			ok++
		}
	}
	return ok
}

func TestABurstIsAllowedThenTheRateApplies(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(start)
	l := ratelimit.New(clk, 2, 5)
	if got := allowed(l); got != 5 {
		t.Errorf("allowed %d of 10 at once, want the burst of 5", got)
	}
	clk.Advance(time.Second) // two tokens come back
	if got := allowed(l); got != 2 {
		t.Errorf("allowed %d after one second, want 2", got)
	}
	clk.Advance(time.Minute) // the bucket refills, to the burst and no further
	if got := allowed(l); got != 5 {
		t.Errorf("allowed %d after a long wait, want the burst of 5", got)
	}
}

func TestEachKeyHasItsOwnBucket(t *testing.T) {
	t.Parallel()
	l := ratelimit.New(clock.NewFake(start), 1, 1)
	if a, _ := l.Allow("a"); !a {
		t.Fatal("the first request is allowed")
	}
	if a, _ := l.Allow("a"); a {
		t.Error("a second request at once should wait")
	}
	if b, _ := l.Allow("b"); !b {
		t.Error("another key must not be limited by the first")
	}
}

func TestTheWaitSaysWhenTheNextRequestWillBeAllowed(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(start)
	l := ratelimit.New(clk, 4, 1)
	l.Allow("a")
	ok, wait := l.Allow("a")
	if ok || wait != 250*time.Millisecond {
		t.Errorf("allowed %v, wait %v; want a refusal and 250ms", ok, wait)
	}
	clk.Advance(wait)
	if ok, _ := l.Allow("a"); !ok {
		t.Error("the request should be allowed once the wait is over")
	}
}

func TestADefaultBurstIsTheRateRoundedUp(t *testing.T) {
	t.Parallel()
	if got := allowed(ratelimit.New(clock.NewFake(start), 2.5, 0)); got != 3 {
		t.Errorf("allowed %d, want 3 (2.5 rounded up)", got)
	}
	if got := allowed(ratelimit.New(clock.NewFake(start), 0.2, 0)); got != 1 {
		t.Errorf("allowed %d, want at least one", got)
	}
}

func TestIdleBucketsAreDroppedWhenThereAreTooMany(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(start)
	l := ratelimit.New(clk, 1, 1)
	for i := range 10_000 {
		l.Allow(fmt.Sprintf("10.0.%d.%d", i/250, i%250))
	}
	clk.Advance(time.Minute) // every bucket is full again
	l.Allow("new")
	if n := l.Keys(); n > 2 {
		t.Errorf("%d keys are still tracked after they all went idle", n)
	}
}
