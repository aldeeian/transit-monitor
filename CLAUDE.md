# CLAUDE.md

Guidance for AI assistants (and humans) working in this repo.

## What this is

A real-time delay/disruption monitor for Calgary Transit, built on the City of
Calgary's public GTFS-Realtime feeds. One Go binary does four jobs off a single
30-second poll loop; a Next.js dashboard reads its API.

## Architecture

```
Calgary GTFS-RT feed ──poll 30s──> [ Go binary: cmd/ingest ]
                                      ├─ fetch + decode protobuf   (internal/gtfsrt)
                                      ├─ normalize to plain events (internal/gtfsrt)
                                      ├─ diff vs DB + persist       (internal/store)  ──> Postgres (Supabase)
                                      ├─ read-only JSON API         (internal/api)    <── Next.js dashboard (Vercel)
                                      └─ email notifier             (internal/notify) ──> Resend
```

- **`cmd/ingest`** — the production entrypoint: poller + API + notifier in one process.
- **`cmd/devdb`** — spins up a local embedded Postgres on :5433 (zero install).
- **`cmd/simulate`** — injects a synthetic disruption (route 999) for end-to-end testing.
- **`cmd/dbstats`, `cmd/sqlq`, `cmd/dumpalert`** — local debugging helpers.
- **`internal/gtfsrt`** — feed fetch, protobuf decode, alert normalization + diff.
- **`internal/store`** — Postgres persistence, idempotent diffing, subscriptions.
- **`internal/api`** — HTTP handlers, health, rate limiting.
- **`internal/notify`** — matches new events to verified subscribers and sends email.
- **`internal/email`** — Resend sender with a dev-mode `.outbox/` fallback.
- **`dashboard/`** — Next.js + TypeScript read-only UI.

## Run

```bash
go run ./cmd/devdb      # terminal 1: local Postgres on :5433 (data in .devdb/)
go run ./cmd/ingest -db "postgres://postgres:postgres@127.0.0.1:5433/transit?sslmode=disable"
# or: go run ./cmd/ingest            # stdout-only, no DB
#     go run ./cmd/ingest -once      # single poll then exit

cd dashboard && npm install && npm run dev   # dashboard on :3000
```

Config is via flags or env (`DATABASE_URL`, `RESEND_API_KEY`, `PUBLIC_URL`,
`DASHBOARD_URL`, `EMAIL_FROM`, `PORT`/`LISTEN_ADDR`, `ALERTS_URL`). See
`.env.example`. Empty `DATABASE_URL` = stdout-only, no persistence.

## Test

```bash
go vet ./...
go build ./...
go test ./... -short   # fast unit tests
go test ./...          # full: boots a throwaway embedded Postgres (downloads binaries once)
```

CI (`.github/workflows/ci.yml`) runs vet, build, and both test modes on every
push and pull request. Keep the nil-slice/NULL regression tests
(`internal/store/store_test.go`, `TestUpsertSubscriptionNilRoutes`) — they pin a
real bug in the live feed (route-less alerts binding as SQL NULL).

## Deploy

- Backend: Render Docker web service (`render.yaml` + `Dockerfile`), Postgres on Supabase.
- Dashboard: Vercel (`NEXT_PUBLIC_API_URL` points at the Render API, baked at build).
- Free tiers sleep/pause when idle — see the Reliability section in `README.md`.

## Rules

- **Secrets never go in the repo.** No API keys, tokens, or real connection
  strings in any tracked file — not in code, not in `render.yaml` (use
  `sync: false`), not in committed config. Real values live only in Render and
  Vercel environment variables and in a local, gitignored `.env`. The only
  connection string allowed in source is the local dev default
  (`postgres:postgres@127.0.0.1:5433`). If a secret ever lands in a commit,
  rotate it immediately — scrubbing history is not enough once it's pushed.
- **Ground rule for claims:** don't describe a feature the code doesn't have.
  Docker, CI, the REST API, tests, rate limiting, and health checks are all
  really implemented — keep README/docs matched to the code.
- Don't commit `.env`, `.devdb/`, `.outbox/`, or `node_modules/`.
