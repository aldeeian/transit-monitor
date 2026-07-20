-- Current known state of every alert we have ever seen.
CREATE TABLE IF NOT EXISTS alerts (
    id            TEXT PRIMARY KEY,          -- GTFS-RT feed entity id
    fingerprint   TEXT NOT NULL,             -- content hash for change detection
    header        TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    cause         TEXT NOT NULL DEFAULT '',
    effect        TEXT NOT NULL DEFAULT '',
    routes        TEXT[] NOT NULL DEFAULT '{}',
    stops         TEXT[] NOT NULL DEFAULT '{}',
    starts_at     TIMESTAMPTZ,
    ends_at       TIMESTAMPTZ,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at   TIMESTAMPTZ                -- NULL while the alert is active
);

-- Append-only history: one row per state transition, powers the
-- "disruption frequency per route" view and (later) notifications.
CREATE TABLE IF NOT EXISTS alert_events (
    id          BIGSERIAL PRIMARY KEY,
    alert_id    TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('new', 'updated', 'resolved')),
    snapshot    JSONB,                       -- normalized event at that moment (NULL for resolved)
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_alert_events_alert ON alert_events (alert_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_alert_events_time  ON alert_events (occurred_at);
CREATE INDEX IF NOT EXISTS idx_alerts_active      ON alerts (last_seen_at) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_alerts_routes      ON alerts USING gin (routes);
