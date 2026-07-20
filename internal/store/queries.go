package store

import (
	"context"
	"time"
)

// Alert is a full row from the alerts table, as served by the API.
type Alert struct {
	ID          string     `json:"id"`
	Header      string     `json:"header"`
	Description string     `json:"description"`
	Cause       string     `json:"cause"`
	Effect      string     `json:"effect"`
	Routes      []string   `json:"routes"`
	Stops       []string   `json:"stops"`
	StartsAt    *time.Time `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
}

const alertCols = `id, header, description, cause, effect, routes, stops,
	starts_at, ends_at, first_seen_at, last_seen_at, resolved_at`

func scanAlerts(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]Alert, error) {
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.Header, &a.Description, &a.Cause, &a.Effect,
			&a.Routes, &a.Stops, &a.StartsAt, &a.EndsAt,
			&a.FirstSeenAt, &a.LastSeenAt, &a.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActiveDisruptions returns unresolved alerts, newest first. route == ""
// returns all; otherwise only alerts affecting that route.
func (s *Store) ActiveDisruptions(ctx context.Context, route string) ([]Alert, error) {
	q := `SELECT ` + alertCols + ` FROM alerts WHERE resolved_at IS NULL`
	args := []any{}
	if route != "" {
		q += ` AND $1 = ANY(routes)`
		args = append(args, route)
	}
	q += ` ORDER BY first_seen_at DESC`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanAlerts(rows)
}

// RouteSummary aggregates one route's disruption picture.
type RouteSummary struct {
	Route      string `json:"route"`
	ActiveNow  int    `json:"active_now"`
	NewInRange int    `json:"new_in_range"` // new disruptions in the last N days
}

// RoutesSummary returns per-route counts: currently active alerts, and new
// disruptions recorded in the last `days` days. Routes with neither are
// omitted (they have no rows to derive from).
func (s *Store) RoutesSummary(ctx context.Context, days int) ([]RouteSummary, error) {
	rows, err := s.pool.Query(ctx, `
		WITH per_route AS (
			SELECT unnest(a.routes) AS route,
			       (a.resolved_at IS NULL)::int AS active,
			       (EXISTS (
			           SELECT 1 FROM alert_events e
			           WHERE e.alert_id = a.id AND e.kind = 'new'
			             AND e.occurred_at > now() - make_interval(days => $1)
			       ))::int AS fresh
			FROM alerts a
		)
		SELECT route, sum(active), sum(fresh)
		FROM per_route
		GROUP BY route
		HAVING sum(active) > 0 OR sum(fresh) > 0
		ORDER BY sum(active) DESC, sum(fresh) DESC, route`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RouteSummary{}
	for rows.Next() {
		var r RouteSummary
		if err := rows.Scan(&r.Route, &r.ActiveNow, &r.NewInRange); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DayCount is one day's new-disruption count for a route.
type DayCount struct {
	Day   time.Time `json:"day"`
	Count int       `json:"count"`
}

// HistoryEvent is one state transition affecting a route.
type HistoryEvent struct {
	AlertID    string    `json:"alert_id"`
	Kind       string    `json:"kind"`
	Header     string    `json:"header"`
	Effect     string    `json:"effect"`
	OccurredAt time.Time `json:"occurred_at"`
}

// RouteHistory returns daily new-disruption counts plus the raw event log
// for one route over the last `days` days.
func (s *Store) RouteHistory(ctx context.Context, route string, days int) ([]DayCount, []HistoryEvent, error) {
	daily := []DayCount{}
	rows, err := s.pool.Query(ctx, `
		SELECT date_trunc('day', e.occurred_at) AS day, count(*)
		FROM alert_events e
		JOIN alerts a ON a.id = e.alert_id
		WHERE e.kind = 'new'
		  AND $1 = ANY(a.routes)
		  AND e.occurred_at > now() - make_interval(days => $2)
		GROUP BY 1 ORDER BY 1`, route, days)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			rows.Close()
			return nil, nil, err
		}
		daily = append(daily, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	events := []HistoryEvent{}
	rows, err = s.pool.Query(ctx, `
		SELECT e.alert_id, e.kind, a.header, a.effect, e.occurred_at
		FROM alert_events e
		JOIN alerts a ON a.id = e.alert_id
		WHERE $1 = ANY(a.routes)
		  AND e.occurred_at > now() - make_interval(days => $2)
		ORDER BY e.occurred_at DESC
		LIMIT 200`, route, days)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h HistoryEvent
		if err := rows.Scan(&h.AlertID, &h.Kind, &h.Header, &h.Effect, &h.OccurredAt); err != nil {
			return nil, nil, err
		}
		events = append(events, h)
	}
	return daily, events, rows.Err()
}
