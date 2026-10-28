// Package ratelimit limits how fast each client may send requests. It is a token bucket per key,
// driven by the injected clock so tests control time.
package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
)

// maxKeys bounds the memory a flood of distinct addresses can use. When it is reached, buckets
// that have been idle long enough to be full again are dropped, which loses nothing.
const maxKeys = 10_000

// Limiter allows each key a steady rate of requests with a burst on top.
type Limiter struct {
	rate  float64 // tokens added per second
	burst float64
	clk   clock.Clock

	mu      sync.Mutex
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

// New returns a limiter that lets each key send perSecond requests a second, plus a burst of burst
// at once. A burst below one is the rate rounded up, so a limit of 5 a second allows 5 together.
func New(clk clock.Clock, perSecond float64, burst int) *Limiter {
	b := float64(burst)
	if burst < 1 {
		b = math.Max(1, math.Ceil(perSecond))
	}
	return &Limiter{rate: perSecond, burst: b, clk: clk, buckets: map[string]*bucket{}}
}

// Allow takes a token for key. When none is left it returns how long to wait for the next.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.clk.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.at).Seconds()*l.rate)
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// sweep drops full buckets when there are too many, at most once a second.
func (l *Limiter) sweep(now time.Time) {
	if len(l.buckets) < maxKeys || now.Sub(l.swept) < time.Second {
		return
	}
	l.swept = now
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.at).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// Keys reports how many keys are tracked, for tests.
func (l *Limiter) Keys() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
