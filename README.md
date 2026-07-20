# transit-monitor

Real-time delay & disruption monitor for Calgary Transit, built on the City of
Calgary's public GTFS-Realtime feeds (refreshed every 30s, no API key).

Instead of the noisy raw protobuf feed, it turns service alerts into
plain-language disruptions you can read, filter by route, and subscribe to by
email — plus a per-route history so you can see which lines are disrupted most.

**Live demo:** dashboard https://transit-monitor.vercel.app · API https://transit-monitor.onrender.com/api/health

## Architecture

A single Go binary runs three things off one 30-second poll loop: it fetches
and decodes the GTFS-RT service-alerts feed, persists normalized disruption
events to Postgres, serves a read-only JSON API, and emails subscribers when a
new disruption hits one of their routes. A Next.js dashboard reads the API.

| Component | Stack | Hosting |
|---|---|---|
| Poller + API + notifier | Go (one binary) | Render (Docker) |
| Database | Postgres | Supabase |
| Dashboard | Next.js + TypeScript | Vercel |
| Email | Resend HTTP API | — |

## Running locally

```
go run ./cmd/devdb           # local Postgres on :5433, zero install (data in .devdb/)
go run ./cmd/ingest -db "postgres://postgres:postgres@127.0.0.1:5433/transit?sslmode=disable"
go run ./cmd/ingest          # stdout-only mode, no DB
go run ./cmd/ingest -once    # single poll, then exit
go run ./cmd/dbstats         # row counts sanity check
go run ./cmd/dumpalert -n 2  # debug: dump raw feed entities as prototext
go test ./...                # includes integration tests vs a real embedded Postgres
go test ./... -short         # unit tests only, skips embedded Postgres
```

`-db` also reads `DATABASE_URL`; empty means stdout-only. Run the dashboard
with `cd dashboard && npm install && npm run dev` (set `NEXT_PUBLIC_API_URL`
to point at the API; default `http://127.0.0.1:8080`).

## Data pipeline

A Go service polls the Service Alerts feed, decodes the protobuf, normalizes
each alert into a structured event, and diffs it against durable state in
Postgres — so only genuinely new/updated/resolved alerts produce events, and
restarts never re-alert on known alerts.

- `alerts` — current state per alert ID, upserted; `resolved_at` is set when an
  alert leaves the feed and cleared if it reappears.
- `alert_events` — append-only history (`new` / `updated` / `resolved`), one
  row per real state transition; powers per-route history.
- Idempotency comes from diffing against fingerprints loaded from the DB, not
  memory; the poller only advances its diff state after a successful commit, so
  a failed write retries next tick instead of dropping events.
- Nil routes/stops from sparse alerts are coalesced to empty arrays (the live
  feed really does send route-less alerts).

## API

With `-db` set (and not `-once`), the binary serves a read-only JSON API
(default `127.0.0.1:8080`, `-listen` to change):

| Endpoint | Returns |
|---|---|
| `GET /api/health` | poller liveness — 200 while the last successful poll is < 3 intervals old, else 503 |
| `GET /api/disruptions[?route=N]` | active disruptions, newest first |
| `GET /api/routes?days=N` | per-route summary: active now + new disruptions in window |
| `GET /api/routes/{route}/history?days=N` | daily new-disruption counts + event log (`days` clamped 1–90, default 14) |
| `POST /api/subscribe` | start a double-opt-in email subscription (rate-limited per IP and per email) |
| `GET /api/verify` · `GET /api/unsubscribe` | confirm / cancel a subscription via emailed token link |

## Dashboard

Ops-tool aesthetic on purpose: dense left-aligned tables, monospace, muted
GitHub-dark palette, status colors that carry meaning (red = no service,
amber = delays/detours, gray = informational; always paired with text).
Views: active disruptions (filter by text/route chips, sort, click a row for
full detail), per-route page (current status + daily new-disruption bar chart +
event log), and an always-visible health strip — LIVE / STALE / OFFLINE with
last-poll age, so the site is honest about its own data freshness.

## Email alerts

- Subscribe with an email + routes → **double opt-in**: a confirmation email
  with a verify link; zero alerts until clicked. Re-subscribing (even to change
  routes) rotates the token and requires re-confirmation.
- The notifier runs on the poll cadence (no second poller): new `alert_events`
  rows are matched to verified subscriptions by route overlap and claimed + sent
  in a single transaction (`notifications_sent` log) — a restart mid-batch can't
  duplicate, a failed send retries next pass.
- New subscribers don't get the existing backlog (events predating verification
  are excluded). Every alert has a **one-click unsubscribe** link (no login).
- Sender: Resend when `RESEND_API_KEY` is set (free tier: 3k/month, 100/day;
  unverified domains can only send to your own account email); otherwise dev
  mode writes `.html` files to `-outbox` (default `.outbox/`).
- `go run ./cmd/simulate -routes 999` injects a synthetic disruption to test
  the pipeline end to end.

## Deployment

Deploy config lives in `render.yaml` (blueprint) and `Dockerfile` (distroless
static build). Environment variables:

- `DATABASE_URL` — Postgres connection (use Supabase's **session pooler** on
  :5432; the free Render tier is IPv4-only and Supabase's direct host is IPv6).
- `RESEND_API_KEY` — enables real email sending.
- `PUBLIC_URL` — the API's public base URL, used in email verify/unsubscribe links.
- `DASHBOARD_URL` — the dashboard base URL, used in "view status" email links.
- `PORT` — read automatically when the platform provides it.

A free Render web service sleeps after 15 idle minutes; an UptimeRobot ping on
`/api/health` keeps it warm and doubles as alerting (the endpoint returns 503
when the poller goes stale). A paid instance removes the cold start.

## Feed URLs

| Feed | Socrata dataset | URL |
|---|---|---|
| Service Alerts | `jhgn-ynqj` | https://data.calgary.ca/download/jhgn-ynqj/application%2Foctet-stream |
| Trip Updates | `gs4m-mdc2` | https://data.calgary.ca/download/gs4m-mdc2/application%2Foctet-stream |
| Vehicle Positions | `am7c-qe3u` | https://data.calgary.ca/download/am7c-qe3u/application%2Foctet-stream |

## Calgary feed quirks (learned from live data)

- `header_text` is always present but **empty** — the real content is in
  `description_text` as raw HTML. It's stripped to plain text with a headline
  synthesized from the first sentence.
- The proto `effect` field is unset; the true effect is embedded in the
  description HTML as `<div data-effect="DETOUR"></div>`. It's extracted, but an
  explicitly set proto effect still wins.
- The feed reports `gtfs_realtime_version: "0.1"` in its header; the payload
  parses fine with standard GTFS-RT v2 bindings.

## Possible next steps

- Ingest the Trip Updates feed for live vehicle lateness (not just planned alerts).
- Resolve numeric route IDs to names using the static GTFS schedule dataset.
- Web push notifications alongside email.
