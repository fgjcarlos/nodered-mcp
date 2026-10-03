package mcp

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type perIPLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipEntry
	rate     rate.Limit
	burst    int

	// Sweep throttling for amortized eviction (see issue #313).
	//
	// insertsSinceSweep counts new-key inserts since the last eviction sweep;
	// lastSweep is the wall-clock time of the last sweep. A sweep only runs
	// when insertsSinceSweep has reached evictEvery AND at least limiterTTL has
	// elapsed since lastSweep, so the worst-case per-request cost is O(1)
	// amortized and no single request pays for more than one O(n) pass per
	// TTL window.
	insertsSinceSweep int
	lastSweep         time.Time
}

type ipEntry struct {
	limiter *rate.Limiter
	last    time.Time
}

const limiterTTL = 10 * time.Minute

// evictEvery is the number of new-IP inserts (or the time elapsed since the
// last sweep) that triggers an eviction pass. It is NOT a hard cap on the
// map size: entries are only deleted when their last-seen time is older
// than limiterTTL, so memory retention stays bounded by TTL * arrival rate.
// A hard cap would let an attacker churn IPs to evict honest clients'
// buckets, which is worse than a bounded sweep.
const evictEvery = 1024

func newPerIPLimiter(rateLimit rate.Limit, burst int) *perIPLimiter {
	if burst < 1 {
		burst = 1
	}
	return &perIPLimiter{
		limiters:  make(map[string]*ipEntry),
		rate:      rateLimit,
		burst:     burst,
		lastSweep: time.Now(),
	}
}

func (l *perIPLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if e, ok := l.limiters[ip]; ok {
		e.last = now
		return e.limiter
	}
	lim := rate.NewLimiter(l.rate, l.burst)
	l.limiters[ip] = &ipEntry{limiter: lim, last: now}
	l.insertsSinceSweep++

	// Amortized sweep (issue #313): run at most once per TTL window, and only
	// when the insert budget is exhausted. New-key inserts stay O(1) under
	// the lock. The map itself is unbounded on purpose; TTL is the bound.
	// ponytail: single O(n) pass per window is the deliberate cost ceiling,
	// upgrade to sharded locks or a background sweeper if a profile shows it.
	if l.insertsSinceSweep >= evictEvery && now.Sub(l.lastSweep) >= limiterTTL {
		l.lastSweep = now
		l.insertsSinceSweep = 0
		for k, e := range l.limiters {
			if now.Sub(e.last) > limiterTTL {
				delete(l.limiters, k)
			}
		}
	}
	return lim
}

func rateLimitByIP(limiter *perIPLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.get(ip).Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	if r.RemoteAddr == "" {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
