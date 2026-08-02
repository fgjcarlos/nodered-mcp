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
}

type ipEntry struct {
	limiter *rate.Limiter
	last    time.Time
}

const limiterTTL = 10 * time.Minute

const evictEvery = 1024

func newPerIPLimiter(rateLimit rate.Limit, burst int) *perIPLimiter {
	if burst < 1 {
		burst = 1
	}
	return &perIPLimiter{
		limiters: make(map[string]*ipEntry),
		rate:     rateLimit,
		burst:    burst,
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
	if len(l.limiters) > evictEvery {
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
