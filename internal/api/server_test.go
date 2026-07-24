package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"transit-monitor/internal/gtfsrt"
	"transit-monitor/internal/store"
	"transit-monitor/internal/testpg"
)

func seedStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, testpg.Start(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	ev := func(id string, routes ...string) gtfsrt.Event {
		return gtfsrt.Event{
			ID: id, Header: "hdr " + id, Effect: "detour",
			Routes: routes, Fingerprint: "fp-" + id,
		}
	}
	// Poll 1: r3 alert + r90 alert. Poll 2: r90 alert resolves.
	poll1 := []gtfsrt.Event{ev("a1", "3"), ev("b1", "90", "3")}
	if err := s.ApplyPoll(ctx, gtfsrt.Diff(map[string]string{}, poll1), poll1); err != nil {
		t.Fatal(err)
	}
	poll2 := []gtfsrt.Event{ev("a1", "3")}
	if err := s.ApplyPoll(ctx, gtfsrt.Diff(gtfsrt.FingerprintMap(poll1), poll2), poll2); err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s: bad json: %v (%s)", path, err, rec.Body.String())
	}
	return rec.Code, body
}

func TestAPI(t *testing.T) {
	s := seedStore(t)
	health := &Health{}
	health.RecordSuccess(time.Now(), 1)
	h := NewServer(s, health, time.Minute).Handler()

	t.Run("disruptions all", func(t *testing.T) {
		code, body := get(t, h, "/api/disruptions")
		if code != 200 || body["count"].(float64) != 1 {
			t.Fatalf("code=%d body=%v, want 1 active (b1 resolved)", code, body)
		}
	})

	t.Run("disruptions by route", func(t *testing.T) {
		code, body := get(t, h, "/api/disruptions?route=3")
		if code != 200 || body["count"].(float64) != 1 {
			t.Fatalf("route 3: code=%d body=%v", code, body)
		}
		code, body = get(t, h, "/api/disruptions?route=90")
		if code != 200 || body["count"].(float64) != 0 {
			t.Fatalf("route 90 should have no active: code=%d body=%v", code, body)
		}
	})

	t.Run("routes summary", func(t *testing.T) {
		code, body := get(t, h, "/api/routes")
		if code != 200 {
			t.Fatalf("code=%d", code)
		}
		routes := body["routes"].([]any)
		// Route 3: 1 active (a1) + 2 new in range; route 90: 0 active, 1 new.
		found := map[string][2]int{}
		for _, r := range routes {
			m := r.(map[string]any)
			found[m["route"].(string)] = [2]int{int(m["active_now"].(float64)), int(m["new_in_range"].(float64))}
		}
		if found["3"] != [2]int{1, 2} {
			t.Errorf("route 3 = %v, want {1 2}", found["3"])
		}
		if found["90"] != [2]int{0, 1} {
			t.Errorf("route 90 = %v, want {0 1}", found["90"])
		}
	})

	t.Run("route history", func(t *testing.T) {
		code, body := get(t, h, "/api/routes/90/history?days=7")
		if code != 200 {
			t.Fatalf("code=%d", code)
		}
		daily := body["daily"].([]any)
		if len(daily) != 1 || int(daily[0].(map[string]any)["count"].(float64)) != 1 {
			t.Errorf("daily = %v, want one day with count 1", daily)
		}
		events := body["events"].([]any)
		// b1 history: new + resolved = 2 events touching route 90.
		if len(events) != 2 {
			t.Errorf("events = %d, want 2", len(events))
		}
	})

	t.Run("health ok and stale", func(t *testing.T) {
		code, body := get(t, h, "/api/health")
		if code != 200 || body["ok"] != true {
			t.Fatalf("health: code=%d body=%v", code, body)
		}
		staleH := &Health{}
		staleSrv := NewServer(s, staleH, time.Minute).Handler()
		code, body = get(t, staleSrv, "/api/health")
		if code != 503 || body["ok"] != false {
			t.Fatalf("never-polled health should be 503: code=%d body=%v", code, body)
		}
	})

	t.Run("days clamped", func(t *testing.T) {
		code, body := get(t, h, "/api/routes?days=9999")
		if code != 200 || body["days"].(float64) != 90 {
			t.Fatalf("days should clamp to 90: %v", body["days"])
		}
		code, body = get(t, h, "/api/routes?days=-3")
		if code != 200 || body["days"].(float64) != 14 {
			t.Fatalf("bad days should default to 14: %v", body["days"])
		}
	})

	t.Run("non-numeric days rejected", func(t *testing.T) {
		code, body := get(t, h, "/api/routes?days=abc")
		if code != 400 || body["error"] == nil {
			t.Fatalf("non-numeric days should be 400: code=%d body=%v", code, body)
		}
		code, body = get(t, h, "/api/routes/90/history?days=abc")
		if code != 400 || body["error"] == nil {
			t.Fatalf("non-numeric days should be 400: code=%d body=%v", code, body)
		}
	})

	t.Run("cors header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Error("missing CORS header")
		}
	})
}

func TestHealthStalenessWindow(t *testing.T) {
	h := &Health{}
	h.RecordSuccess(time.Now(), 5)
	if !h.View(time.Minute).OK {
		t.Error("fresh success should be OK")
	}
	// Zero window can never be satisfied (elapsed >= 0), regardless of
	// Windows timer granularity returning 0ns elapsed.
	if v := h.View(0); v.OK {
		t.Error("expired window should not be OK")
	}
	h.RecordFailure()
	h.RecordFailure()
	if v := h.View(time.Minute); !v.OK || v.ConsecutiveFailures != 2 {
		t.Errorf("failures within window: %+v, want OK with 2 failures", v)
	}
}
