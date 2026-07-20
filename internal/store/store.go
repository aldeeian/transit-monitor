// Package store persists normalized disruption events to Postgres.
//
// Design: the poller diffs each snapshot against fingerprints loaded from
// the DB (not memory), so restarts never re-alert on known alerts, and
// re-applying an unchanged snapshot writes no event rows — idempotency
// comes from diffing against durable state, not from constraint tricks.
package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"transit-monitor/internal/gtfsrt"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	pool *pgxpool.Pool
}

// Open connects, pings, and applies the schema (idempotent DDL).
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// ActiveFingerprints returns id -> fingerprint for all unresolved alerts;
// this seeds the poller's diff state on startup.
func (s *Store) ActiveFingerprints(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, fingerprint FROM alerts WHERE resolved_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := map[string]string{}
	for rows.Next() {
		var id, fp string
		if err := rows.Scan(&id, &fp); err != nil {
			return nil, err
		}
		m[id] = fp
	}
	return m, rows.Err()
}

// ApplyPoll writes one poll's diff in a single transaction: upserts for
// new/changed alerts, resolution marks, last-seen touches, and one
// alert_events row per state transition.
func (s *Store) ApplyPoll(ctx context.Context, d gtfsrt.DiffResult, current []gtfsrt.Event) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, ev := range d.New {
		if err := upsertAlert(ctx, tx, ev); err != nil {
			return fmt.Errorf("upsert new %s: %w", ev.ID, err)
		}
		if err := insertEvent(ctx, tx, ev.ID, "new", &ev); err != nil {
			return fmt.Errorf("event new %s: %w", ev.ID, err)
		}
	}
	for _, ev := range d.Changed {
		if err := upsertAlert(ctx, tx, ev); err != nil {
			return fmt.Errorf("upsert changed %s: %w", ev.ID, err)
		}
		if err := insertEvent(ctx, tx, ev.ID, "updated", &ev); err != nil {
			return fmt.Errorf("event updated %s: %w", ev.ID, err)
		}
	}
	for _, id := range d.Resolved {
		// Guard on resolved_at IS NULL so a crashed/retried poll can't
		// double-insert a resolved event.
		tag, err := tx.Exec(ctx,
			`UPDATE alerts SET resolved_at = now() WHERE id = $1 AND resolved_at IS NULL`, id)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", id, err)
		}
		if tag.RowsAffected() > 0 {
			if err := insertEvent(ctx, tx, id, "resolved", nil); err != nil {
				return fmt.Errorf("event resolved %s: %w", id, err)
			}
		}
	}

	if len(current) > 0 {
		ids := make([]string, len(current))
		for i, ev := range current {
			ids[i] = ev.ID
		}
		if _, err := tx.Exec(ctx,
			`UPDATE alerts SET last_seen_at = now() WHERE id = ANY($1)`, ids); err != nil {
			return fmt.Errorf("touch last_seen: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// upsertAlert inserts or refreshes an alert's current state. A previously
// resolved alert that reappears is reactivated (resolved_at cleared).
func upsertAlert(ctx context.Context, tx pgx.Tx, ev gtfsrt.Event) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO alerts (id, fingerprint, header, description, cause, effect,
		                    routes, stops, starts_at, ends_at)
		-- COALESCE: alerts with no routes/stops arrive as nil slices, which
		-- pgx binds as SQL NULL rather than an empty array.
		VALUES ($1, $2, $3, $4, $5, $6,
		        COALESCE($7, '{}'::text[]), COALESCE($8, '{}'::text[]), $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			fingerprint = EXCLUDED.fingerprint,
			header      = EXCLUDED.header,
			description = EXCLUDED.description,
			cause       = EXCLUDED.cause,
			effect      = EXCLUDED.effect,
			routes      = EXCLUDED.routes,
			stops       = EXCLUDED.stops,
			starts_at   = EXCLUDED.starts_at,
			ends_at     = EXCLUDED.ends_at,
			last_seen_at = now(),
			resolved_at  = NULL`,
		ev.ID, ev.Fingerprint, ev.Header, ev.Description, ev.Cause, ev.Effect,
		ev.Routes, ev.Stops, ev.Starts, ev.Ends)
	return err
}

func insertEvent(ctx context.Context, tx pgx.Tx, alertID, kind string, snapshot *gtfsrt.Event) error {
	var snap []byte
	if snapshot != nil {
		b, err := json.Marshal(snapshot)
		if err != nil {
			return fmt.Errorf("marshal snapshot: %w", err)
		}
		snap = b
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO alert_events (alert_id, kind, snapshot) VALUES ($1, $2, $3)`,
		alertID, kind, snap)
	return err
}

// Stats is a quick operational summary used by cmd/dbstats and health checks.
type Stats struct {
	ActiveAlerts   int
	ResolvedAlerts int
	EventRows      int
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM alerts WHERE resolved_at IS NULL),
			(SELECT count(*) FROM alerts WHERE resolved_at IS NOT NULL),
			(SELECT count(*) FROM alert_events)`).
		Scan(&st.ActiveAlerts, &st.ResolvedAlerts, &st.EventRows)
	return st, err
}
