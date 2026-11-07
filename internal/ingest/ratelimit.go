package ingest

import (
	"math"
	"sync"
	"time"
)

// RateLimiter caps how many events each project may send per minute. A
// project key is public by design (it ships inside browser and mobile
// apps), so without a cap anyone holding one could fill the disk. Each
// project gets a token bucket that holds one minute's worth of events and
// refills continuously.
type RateLimiter struct {
	PerMinute int
	Now       func() time.Time

	mu      sync.Mutex
	buckets map[int64]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimiter(perMinute int) *RateLimiter {
	return &RateLimiter{PerMinute: perMinute, buckets: map[int64]*bucket{}}
}

func (l *RateLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Allow takes n events from project's bucket. When there aren't enough it
// takes nothing and reports how long until there will be.
func (l *RateLimiter) Allow(project int64, n int) (bool, time.Duration) {
	if l == nil || l.PerMinute <= 0 || n <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	capacity := float64(l.PerMinute)
	rate := capacity / 60 // tokens per second
	now := l.now()
	b := l.buckets[project]
	if b == nil {
		b = &bucket{tokens: capacity, last: now}
		l.buckets[project] = b
	}
	b.tokens = math.Min(capacity, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
	need := float64(n)
	if need > capacity {
		need = capacity // a batch bigger than the bucket waits for a full one
	}
	if b.tokens >= need {
		b.tokens -= float64(n)
		if b.tokens < 0 {
			b.tokens = 0
		}
		return true, 0
	}
	wait := time.Duration((need - b.tokens) / rate * float64(time.Second))
	return false, wait
}
