# transit-monitor

Real-time delay & disruption monitor for Calgary Transit, built on the City of
Calgary's public GTFS-Realtime feeds (refreshed every 30s, no API key).

## Status: Phase 1 — ingestion pipeline (done)

A Go service polls the Service Alerts feed, decodes the protobuf, normalizes
each alert into a structured event, and prints plain-language output to
stdout. An in-memory diff means repeat polls only report new/updated/resolved
alerts, never duplicates.

```
go run ./cmd/ingest          # poll every 30s until ctrl-c
go run ./cmd/ingest -once    # single poll, print snapshot, exit
go run ./cmd/dumpalert -n 2  # debug: dump raw feed entities as prototext
go test ./...
```

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
2. Phase 2: Postgres persistence + durable diffing (only store/alert on new events)
3. Phase 3: REST API — current disruptions + history
4. Phase 4: Next.js dashboard
5. Phase 5: email subscriptions
6. Phase 6: deploy + health monitoring
