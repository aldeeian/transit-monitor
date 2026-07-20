package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a small in-memory sliding-window limiter. Good enough for
// a single-instance deployment; swap for something shared if we ever run
// multiple replicas.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: map[string][]time.Time{}}
}

// allow records an attempt for key and reports whether it is within limit
// per window. Old entries are pruned lazily on access.
func (rl *rateLimiter) allow(key string, limit int, window time.Duration) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-window)
	kept := rl.buckets[key][:0]
	for _, t := range rl.buckets[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		rl.buckets[key] = kept
		return false
	}
	rl.buckets[key] = append(kept, time.Now())
	return true
}

// prune drops keys whose entries have all expired; called periodically so
// the map doesn't grow forever under scanning traffic.
func (rl *rateLimiter) prune(window time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-window)
	for k, ts := range rl.buckets {
		alive := false
		for _, t := range ts {
			if t.After(cutoff) {
				alive = true
				break
			}
		}
		if !alive {
			delete(rl.buckets, k)
		}
	}
}

// clientIP extracts the caller's IP, trusting X-Forwarded-For's first hop
// (we deploy behind a single reverse proxy on Railway/Fly/Render).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
