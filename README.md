# transit-monitor

Real-time delay & disruption monitor for Calgary Transit, built on the City of
Calgary's public GTFS-Realtime feeds (refreshed every 30s, no API key).

## Status: Phase 2 — Postgres persistence (done)

A Go service polls the Service Alerts feed, decodes the protobuf, normalizes
each alert into a structured event, and diffs it against durable state in
Postgres — so only genuinely new/updated/resolved alerts produce events, and
restarts never re-alert on known alerts.

```
go run ./cmd/devdb           # local Postgres on :5433, zero install (data in .devdb/)
go run ./cmd/ingest -db "postgres://postgres:postgres@127.0.0.1:5433/transit?sslmode=disable"
go run ./cmd/ingest          # stdout-only mode, no DB (Phase 1 behavior)
go run ./cmd/ingest -once    # single poll, then exit
go run ./cmd/dbstats         # row counts sanity check
go run ./cmd/dumpalert -n 2  # debug: dump raw feed entities as prototext
go test ./...                # includes integration tests vs real embedded Postgres
go test ./... -short         # unit tests only, skips embedded Postgres
```

`-db` also reads `DATABASE_URL`. Empty = stdout-only.

### Persistence design

- `alerts` — current state per alert ID, upserted; `resolved_at` set when an
  alert leaves the feed, cleared if it reappears.
- `alert_events` — append-only history (`new` / `updated` / `resolved`), one
  row per real state transition; powers per-route disruption history later.
- Idempotency comes from diffing against fingerprints loaded from the DB, not
  memory; the poller only advances its diff state after a successful commit,
  so a failed write retries next tick instead of dropping events.
- Nil routes/stops from sparse alerts are coalesced to empty arrays (the live
  feed really does send route-less alerts).

## Feed URLs (verified live 2026-07-19)

| Feed | Socrata dataset | URL |
|---|---|---|
| Service Alerts | `jhgn-ynqj` | https://data.calgary.ca/download/jhgn-ynqj/application%2Foctet-stream |
| Trip Updates | `gs4m-mdc2` | https://data.calgary.ca/download/gs4m-mdc2/application%2Foctet-stream |
| Vehicle Positions | `am7c-qe3u` | https://data.calgary.ca/download/am7c-qe3u/application%2Foctet-stream |

## Calgary feed quirks (learned from live data)

- `header_text` is always present but **empty** — the real content is in
  `description_text` as raw HTML. We strip it to plain text and synthesize a
  headline from the first sentence.
- The proto `effect` field is unset; the true effect is embedded in the
  description HTML as `<div data-effect="DETOUR"></div>`. We extract it, but
  an explicitly set proto effect still wins.
- Feed reports `gtfs_realtime_version: "0.1"` in its header; the payload
  parses fine with standard GTFS-RT v2 bindings.

## Roadmap

1. ~~Phase 1: poll + parse + normalized stdout events~~
2. ~~Phase 2: Postgres persistence + durable diffing~~
3. Phase 3: REST API — current disruptions + history
4. Phase 4: Next.js dashboard
5. Phase 5: email subscriptions
6. Phase 6: deploy + health monitoring
