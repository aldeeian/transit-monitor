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

// clientIP extracts the caller's IP. X-Forwarded-For is only consulted when
// the deployment declares a reverse proxy in front (TrustProxy), and then only
// its last hop — the entry appended by that proxy, which is the one a client
// cannot forge by sending its own X-Forwarded-For.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			hops := strings.Split(xff, ",")
			if last := strings.TrimSpace(hops[len(hops)-1]); last != "" {
				return last
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
