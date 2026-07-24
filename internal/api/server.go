// Package api serves the read-only REST API over the store.
package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"transit-monitor/internal/email"
	"transit-monitor/internal/store"
)

const defaultHistoryDays = 14

type Server struct {
	store        *store.Store
	health       *Health
	staleAfter   time.Duration
	sender       email.Sender
	publicURL    string
	limiter      *rateLimiter
	writeOrigins map[string]bool
	trustProxy   bool
}

func NewServer(st *store.Store, h *Health, staleAfter time.Duration) *Server {
	return &Server{store: st, health: h, staleAfter: staleAfter, limiter: newRateLimiter()}
}

// EnableSubscriptions turns on the subscribe/verify/unsubscribe endpoints.
// publicURL is the externally reachable base URL for email links.
func (s *Server) EnableSubscriptions(sender email.Sender, publicURL string) {
	s.sender = sender
	s.publicURL = strings.TrimRight(publicURL, "/")
}

// AllowWriteOrigins restricts which browser origins may call the state-changing
// endpoints cross-origin. Reads stay open to any origin; writes (subscribe)
// send email, so an unrestricted origin lets any site drive them from a
// visitor's browser.
func (s *Server) AllowWriteOrigins(origins ...string) {
	s.writeOrigins = map[string]bool{}
	for _, o := range origins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			s.writeOrigins[o] = true
		}
	}
}

// TrustProxy declares that the service runs behind a reverse proxy that
// appends X-Forwarded-For, so the header may be used for client identity.
// Off by default: a spoofable header would make per-IP rate limits useless.
func (s *Server) TrustProxy(v bool) { s.trustProxy = v }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/disruptions", s.handleDisruptions)
	mux.HandleFunc("GET /api/routes", s.handleRoutes)
	mux.HandleFunc("GET /api/routes/{route}/history", s.handleRouteHistory)
	mux.HandleFunc("POST /api/subscribe", s.handleSubscribe)
	mux.HandleFunc("GET /api/verify", s.handleVerify)
	mux.HandleFunc("GET /api/unsubscribe", s.handleUnsubscribe)
	mux.HandleFunc("OPTIONS /api/", s.handlePreflight)
	return s.withCORS(mux)
}

// withCORS lets any origin read the public data, while cross-origin writes are
// limited to the configured dashboard origins (none configured = same-origin
// only). No credentials are ever accepted.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if s.isWriteMethod(r) {
			if origin != "" && !s.writeOrigins[strings.TrimRight(origin, "/")] {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin not allowed"})
				return
			}
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		next.ServeHTTP(w, r)
	})
}

// isWriteMethod reports whether the request is a state-changing call or the
// preflight for one.
func (s *Server) isWriteMethod(r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return r.Header.Get("Access-Control-Request-Method") == http.MethodPost
	}
	return r.Method != http.MethodGet && r.Method != http.MethodHead
}

func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	v := s.health.View(s.staleAfter)
	code := http.StatusOK
	if !v.OK {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, v)
}

// maxRouteLen bounds route identifiers accepted from the URL; real GTFS route
// IDs are a handful of characters.
const maxRouteLen = 10

func (s *Server) handleDisruptions(w http.ResponseWriter, r *http.Request) {
	route := r.URL.Query().Get("route")
	if len(route) > maxRouteLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid route"})
		return
	}
	alerts, err := s.store.ActiveDisruptions(r.Context(), route)
	if err != nil {
		serverError(w, "list disruptions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disruptions": alerts, "count": len(alerts)})
}

func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
	days := queryDays(r)
	summary, err := s.store.RoutesSummary(r.Context(), days)
	if err != nil {
		serverError(w, "routes summary", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": summary, "days": days})
}

func (s *Server) handleRouteHistory(w http.ResponseWriter, r *http.Request) {
	route := r.PathValue("route")
	if route == "" || len(route) > maxRouteLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid route"})
		return
	}
	days := queryDays(r)
	daily, events, err := s.store.RouteHistory(r.Context(), route, days)
	if err != nil {
		serverError(w, "route history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"route": route, "days": days, "daily": daily, "events": events,
	})
}

// queryDays parses ?days=N, clamped to [1, 90], default 14.
func queryDays(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || n < 1 {
		return defaultHistoryDays
	}
	if n > 90 {
		return 90
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	// Explicit charset: clients like Windows PowerShell 5.1 mis-decode
	// UTF-8 as Latin-1 when the charset is omitted.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("ERROR encode response: %v", err)
	}
}

func serverError(w http.ResponseWriter, what string, err error) {
	log.Printf("ERROR api %s: %v", what, err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	go func() {
		// Keep the rate-limit map bounded under scanner traffic.
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.limiter.prune(24 * time.Hour)
			}
		}
	}()
	log.Printf("api listening on http://%s", addr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}
