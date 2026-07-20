package api

import (
	"sync"
	"time"
)

// Health tracks poller liveness; the poll loop writes, the API reads. This
// is what tells us the ingester silently died before users do.
type Health struct {
	mu                  sync.Mutex
	lastAttempt         time.Time
	lastSuccess         time.Time
	lastFeedTS          time.Time
	activeAlerts        int
	consecutiveFailures int
}

func (h *Health) RecordSuccess(feedTS time.Time, activeAlerts int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	h.lastAttempt = now
	h.lastSuccess = now
	h.lastFeedTS = feedTS
	h.activeAlerts = activeAlerts
	h.consecutiveFailures = 0
}

func (h *Health) RecordFailure() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastAttempt = time.Now()
	h.consecutiveFailures++
}

// HealthView is the JSON shape of /api/health.
type HealthView struct {
	OK                  bool       `json:"ok"`
	LastPollAt          *time.Time `json:"last_poll_at"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	FeedTimestamp       *time.Time `json:"feed_timestamp"`
	ActiveAlerts        int        `json:"active_alerts"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
}

// View reports OK while the last successful poll is younger than staleAfter.
func (h *Health) View(staleAfter time.Duration) HealthView {
	h.mu.Lock()
	defer h.mu.Unlock()
	v := HealthView{
		ActiveAlerts:        h.activeAlerts,
		ConsecutiveFailures: h.consecutiveFailures,
		OK:                  !h.lastSuccess.IsZero() && time.Since(h.lastSuccess) < staleAfter,
	}
	if !h.lastAttempt.IsZero() {
		t := h.lastAttempt
		v.LastPollAt = &t
	}
	if !h.lastSuccess.IsZero() {
		t := h.lastSuccess
		v.LastSuccessAt = &t
	}
	if !h.lastFeedTS.IsZero() {
		t := h.lastFeedTS
		v.FeedTimestamp = &t
	}
	return v
}
