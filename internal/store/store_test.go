package store

import (
	"context"
	"testing"

	"transit-monitor/internal/gtfsrt"
	"transit-monitor/internal/testpg"
)

func event(id, header string) gtfsrt.Event {
	ev := gtfsrt.Event{
		ID:     id,
		Header: header,
		Cause:  "construction",
		Effect: "detour",
		Routes: []string{"3", "90"},
		Stops:  []string{"5501"},
	}
	// Reuse the real fingerprint logic path: hash must change with content.
	ev.Fingerprint = header // stand-in is fine; store treats it as opaque
	return ev
}

func mustApply(t *testing.T, s *Store, prev map[string]string, curr []gtfsrt.Event) map[string]string {
	t.Helper()
	d := gtfsrt.Diff(prev, curr)
	if err := s.ApplyPoll(context.Background(), d, curr); err != nil {
		t.Fatalf("ApplyPoll: %v", err)
	}
	return gtfsrt.FingerprintMap(curr)
}

func TestStoreLifecycle(t *testing.T) {
	dsn := testpg.Start(t)
	ctx := context.Background()

	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Poll 1: two new alerts on an empty DB. b1 has nil routes/stops —
	// regression: the live feed has such alerts and pgx binds nil slices
	// as SQL NULL, which must not violate NOT NULL.
	a := event("a1", "detour on route 3")
	b := event("b1", "stop closed on route 90")
	b.Routes = nil
	b.Stops = nil
	prev, err := s.ActiveFingerprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prev) != 0 {
		t.Fatalf("fresh db should have no fingerprints, got %d", len(prev))
	}
	prev = mustApply(t, s, prev, []gtfsrt.Event{a, b})

	st, _ := s.Stats(ctx)
	if st.ActiveAlerts != 2 || st.EventRows != 2 {
		t.Fatalf("after poll1: %+v, want 2 active / 2 events", st)
	}

	// Poll 2: identical snapshot — must write zero new event rows.
	prev = mustApply(t, s, prev, []gtfsrt.Event{a, b})
	st, _ = s.Stats(ctx)
	if st.EventRows != 2 {
		t.Fatalf("idempotency broken: identical re-poll grew events to %d", st.EventRows)
	}

	// Poll 3: a1 edited, b1 gone, c1 new.
	a2 := event("a1", "detour extended")
	c := event("c1", "delays on Blue Line")
	prev = mustApply(t, s, prev, []gtfsrt.Event{a2, c})

	st, _ = s.Stats(ctx)
	if st.ActiveAlerts != 2 || st.ResolvedAlerts != 1 {
		t.Fatalf("after poll3: %+v, want 2 active / 1 resolved", st)
	}
	// 2 new + 1 updated + 1 resolved + 1 new = 5
	if st.EventRows != 5 {
		t.Fatalf("after poll3: %d event rows, want 5", st.EventRows)
	}

	// Restart simulation: fingerprints from DB must match in-memory state,
	// and diffing against them must produce no changes.
	fromDB, err := s.ActiveFingerprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := gtfsrt.Diff(fromDB, []gtfsrt.Event{a2, c})
	if len(d.New)+len(d.Changed)+len(d.Resolved) != 0 {
		t.Fatalf("restart would re-alert: %+v", d)
	}

	// Poll 4: resolved alert b1 reappears — must reactivate as new.
	prev = mustApply(t, s, fromDB, []gtfsrt.Event{a2, c, b})
	st, _ = s.Stats(ctx)
	if st.ActiveAlerts != 3 || st.ResolvedAlerts != 0 {
		t.Fatalf("reappearing alert not reactivated: %+v", st)
	}
	if st.EventRows != 6 {
		t.Fatalf("after poll4: %d event rows, want 6", st.EventRows)
	}

	// Double-resolve guard: applying the same Resolved diff twice only
	// records one resolved event.
	d = gtfsrt.DiffResult{Resolved: []string{"c1"}}
	if err := s.ApplyPoll(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyPoll(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Stats(ctx)
	if st.EventRows != 7 {
		t.Fatalf("double-resolve wrote duplicate events: %d rows, want 7", st.EventRows)
	}
}
