package api

import (
	"sync"
	"time"
)

type rateLimiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
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

func (l *rateLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.clock()
	if l.last.IsZero() {
		l.last = t
	}

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
