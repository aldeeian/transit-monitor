# Build the ingest service (poller + API + notifier in one binary).
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /ingest ./cmd/ingest

# Minimal runtime: static binary, CA certs for HTTPS (feed + Resend).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /ingest /ingest
# Config via env: DATABASE_URL, RESEND_API_KEY, PORT (or LISTEN_ADDR),
# PUBLIC_URL, DASHBOARD_URL, EMAIL_FROM, ALERTS_URL.
ENTRYPOINT ["/ingest"]
