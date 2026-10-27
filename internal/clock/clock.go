// Package clock is the only place that reads real time. Everything else takes a Clock, so
// tests and the acceptance specs control TTLs, journal timestamps and delays (§10).
package clock

import (
	"context"
	"sync"
	"time"
)

// Clock tells the time and waits.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Sleep waits for d or until ctx is done, and reports whether the full duration elapsed.
	Sleep(ctx context.Context, d time.Duration) bool
}

// Real is the system clock.
type Real struct{}

var _ Clock = Real{}

// Now returns the system time.
func (Real) Now() time.Time { return time.Now() }

// Sleep waits for d on a timer that stops early when ctx is done.
func (Real) Sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// Every calls fn every d of wall-clock time until ctx is done. It is for scheduling
// housekeeping, such as reaping expired sessions; decisions about what has expired still use
// the injected Clock, so a fake clock controls the outcome but cannot make the loop spin.
func Every(ctx context.Context, d time.Duration, fn func()) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// Fake is a manually advanced clock for tests. Sleep returns immediately and advances it.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

var _ Clock = (*Fake)(nil)

// NewFake returns a fake clock set to t.
func NewFake(t time.Time) *Fake { return &Fake{now: t} }

// Now returns the fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the fake time forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Set moves the fake time to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

// Sleep advances the fake time by d, unless ctx is already done.
func (f *Fake) Sleep(ctx context.Context, d time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	f.Advance(d)
	return true
}
