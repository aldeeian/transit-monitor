package store

import (
	"testing"

	"transit-monitor/internal/gtfsrt"
)

func TestQueriesOnEmptyStore(t *testing.T) {
	s, ctx := openStore(t)

	// Empty results must marshal as [] for the API, never nil.
	alerts, err := s.ActiveDisruptions(ctx, "")
	if err != nil || alerts == nil || len(alerts) != 0 {
		t.Fatalf("ActiveDisruptions = %v, %v; want empty non-nil slice", alerts, err)
	}
	summary, err := s.RoutesSummary(ctx, 14)
	if err != nil || summary == nil || len(summary) != 0 {
		t.Fatalf("RoutesSummary = %v, %v; want empty non-nil slice", summary, err)
	}
	daily, events, err := s.RouteHistory(ctx, "3", 14)
	if err != nil || daily == nil || events == nil || len(daily)+len(events) != 0 {
		t.Fatalf("RouteHistory = %v, %v, %v; want empty non-nil slices", daily, events, err)
	}
}

func TestActiveDisruptionsFilterAndResolution(t *testing.T) {
	s, ctx := openStore(t)

	a := event("a1", "detour on route 3") // routes 3, 90
	b := event("b1", "route 10 delays")
	b.Routes = []string{"10"}
	prev := mustApply(t, s, map[string]string{}, []gtfsrt.Event{a, b})

	all, err := s.ActiveDisruptions(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all = %d alerts, want 2", len(all))
	}

	only10, err := s.ActiveDisruptions(ctx, "10")
	if err != nil {
		t.Fatal(err)
	}
	if len(only10) != 1 || only10[0].ID != "b1" {
		t.Fatalf("route 10 = %v, want just b1", only10)
	}
	if only10[0].Cause != "construction" || only10[0].ResolvedAt != nil {
		t.Errorf("alert = %+v, want cause populated and unresolved", only10[0])
	}

	// b1 leaves the feed: it must drop out of active disruptions.
	mustApply(t, s, prev, []gtfsrt.Event{a})
	only10, err = s.ActiveDisruptions(ctx, "10")
	if err != nil {
		t.Fatal(err)
	}
	if len(only10) != 0 {
		t.Fatalf("resolved alert still active: %v", only10)
	}
}

func TestRoutesSummaryAndHistory(t *testing.T) {
	s, ctx := openStore(t)

	a := event("a1", "detour on route 3") // routes 3, 90
	prev := mustApply(t, s, map[string]string{}, []gtfsrt.Event{a})
	mustApply(t, s, prev, nil) // a1 resolves

	summary, err := s.RoutesSummary(ctx, 14)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]int{}
	for _, r := range summary {
		got[r.Route] = [2]int{r.ActiveNow, r.NewInRange}
	}
	for _, route := range []string{"3", "90"} {
		if got[route] != [2]int{0, 1} {
			t.Errorf("route %s = %v, want {0 1} (resolved, one new in window)", route, got[route])
		}
	}

	daily, events, err := s.RouteHistory(ctx, "3", 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 1 || daily[0].Count != 1 {
		t.Errorf("daily = %v, want a single day with one new disruption", daily)
	}
	if len(events) != 2 {
		t.Fatalf("events = %v, want new + resolved", events)
	}
	if events[0].Kind != "resolved" && events[len(events)-1].Kind != "resolved" {
		t.Errorf("events = %v, want a resolved entry", events)
	}
}
