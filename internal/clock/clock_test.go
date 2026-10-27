package clock_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
)

func TestReal(t *testing.T) {
	t.Parallel()
	var c clock.Real
	if d := time.Since(c.Now()); d < 0 || d > time.Second {
		t.Errorf("Now() is %v away from the system time", d)
	}
	if !c.Sleep(t.Context(), 0) || !c.Sleep(t.Context(), -time.Second) {
		t.Error("Sleep(<= 0) should return true at once")
	}
	start := time.Now()
	if !c.Sleep(t.Context(), 30*time.Millisecond) || time.Since(start) < 25*time.Millisecond {
		t.Errorf("Sleep(30ms) returned early after %v", time.Since(start))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start = time.Now()
	if c.Sleep(ctx, time.Hour) || time.Since(start) > time.Second {
		t.Error("Sleep should stop at once, and report false, when the context is cancelled")
	}
}

func TestFake(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)
	if !f.Now().Equal(start) {
		t.Fatalf("Now() = %v, want %v", f.Now(), start)
	}
	f.Advance(90 * time.Minute)
	if want := start.Add(90 * time.Minute); !f.Now().Equal(want) {
		t.Errorf("after Advance, Now() = %v, want %v", f.Now(), want)
	}
	later := start.Add(24 * time.Hour)
	f.Set(later)
	if !f.Now().Equal(later) {
		t.Errorf("after Set, Now() = %v, want %v", f.Now(), later)
	}
	if !f.Sleep(t.Context(), time.Minute) || !f.Now().Equal(later.Add(time.Minute)) {
		t.Errorf("Sleep should advance the fake time by the duration, got %v", f.Now())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := f.Now()
	if f.Sleep(ctx, time.Hour) || !f.Now().Equal(before) {
		t.Error("a cancelled Sleep should return false and leave the time alone")
	}
}

func TestEvery(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	var n atomic.Int32
	reached := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		clock.Every(ctx, 5*time.Millisecond, func() {
			if n.Add(1) == 3 {
				close(reached)
			}
		})
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("Every did not call the function three times")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Every did not stop when the context was cancelled")
	}
}
