package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterWindow(t *testing.T) {
	rl := newRateLimiter()
	for i := 0; i < 5; i++ {
		if !rl.allow("k", 5, time.Hour) {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if rl.allow("k", 5, time.Hour) {
		t.Fatal("6th attempt should be blocked")
	}
	// Different key unaffected.
	if !rl.allow("other", 5, time.Hour) {
		t.Fatal("other key should be allowed")
	}
}

func TestRateLimiterExpiry(t *testing.T) {
	rl := newRateLimiter()
	if !rl.allow("k", 1, 10*time.Millisecond) {
		t.Fatal("first should pass")
	}
	if rl.allow("k", 1, 10*time.Millisecond) {
		t.Fatal("second should block")
	}
	time.Sleep(20 * time.Millisecond)
	if !rl.allow("k", 1, 10*time.Millisecond) {
		t.Fatal("should pass after window expires")
	}
}

func TestRateLimiterPrune(t *testing.T) {
	rl := newRateLimiter()
	rl.allow("dead", 5, time.Hour)
	time.Sleep(30 * time.Millisecond)
	rl.allow("alive", 5, time.Hour)
	rl.prune(10 * time.Millisecond)
	rl.mu.Lock()
	_, deadThere := rl.buckets["dead"]
	_, aliveThere := rl.buckets["alive"]
	rl.mu.Unlock()
	if deadThere {
		t.Error("expired key should be pruned")
	}
	if !aliveThere {
		t.Error("live key should survive prune")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/subscribe", nil)
	r.RemoteAddr = "10.0.0.7:1234"
	if ip := clientIP(r, false); ip != "10.0.0.7" {
		t.Errorf("RemoteAddr ip = %q", ip)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if ip := clientIP(r, false); ip != "10.0.0.7" {
		t.Errorf("untrusted XFF must be ignored, got %q", ip)
	}
	if ip := clientIP(r, true); ip != "10.0.0.1" {
		t.Errorf("trusted XFF should use the last hop, got %q", ip)
	}
}
