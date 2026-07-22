package api

import (
	"sync"
	"time"
)

// rateLimiter is a minimal token bucket (dependency-free): tokens refill at
// `rate` per second up to `burst`, and each request spends one. It is process-
// wide, not per-client — a coarse backstop against a runaway producer, not a
// fairness mechanism.
type rateLimiter struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64 // max tokens
	tokens float64
	last   time.Time
	now    func() time.Time // injectable for tests
}

func newRateLimiter(rate, burst float64) *rateLimiter {
	if burst < 1 {
		burst = 1
	}
	return &rateLimiter{rate: rate, burst: burst, tokens: burst}
}

func (l *rateLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// allow reports whether a request may proceed, spending a token if so.
func (l *rateLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.clock()
	if l.last.IsZero() {
		l.last = t
	}
	// Refill for the elapsed time.
	l.tokens += l.rate * t.Sub(l.last).Seconds()
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = t
	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}
