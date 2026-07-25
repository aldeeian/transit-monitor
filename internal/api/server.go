// Package api serves the read-only REST API over the store.
package api

import (
	"context"
	"encoding/json"
	"errors"
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
	store      *store.Store
	health     *Health
	staleAfter time.Duration
	sender     email.Sender
	publicURL  string
	limiter    *rateLimiter
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

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/disruptions", s.handleDisruptions)
	mux.HandleFunc("GET /api/routes", s.handleRoutes)
	mux.HandleFunc("GET /api/routes/{route}/history", s.handleRouteHistory)
	mux.HandleFunc("POST /api/subscribe", s.handleSubscribe)
	mux.HandleFunc("GET /api/verify", s.handleVerify)
	mux.HandleFunc("GET /api/unsubscribe", s.handleUnsubscribe)
	return withCORS(mux)
}

// withCORS allows the browser dashboard on another origin to read the API.
// GET-only, no credentials, so permissive is fine.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	v := s.health.View(s.staleAfter)
	code := http.StatusOK
	if !v.OK {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, v)
}

func (s *Server) handleDisruptions(w http.ResponseWriter, r *http.Request) {
	alerts, err := s.store.ActiveDisruptions(r.Context(), r.URL.Query().Get("route"))
	if err != nil {
		serverError(w, "list disruptions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disruptions": alerts, "count": len(alerts)})
}

func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
	days, ok := queryDays(w, r)
	if !ok {
		return
	}
	summary, err := s.store.RoutesSummary(r.Context(), days)
	if err != nil {
		serverError(w, "routes summary", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": summary, "days": days})
}

func (s *Server) handleRouteHistory(w http.ResponseWriter, r *http.Request) {
	route := r.PathValue("route")
	days, ok := queryDays(w, r)
	if !ok {
		return
	}
	daily, events, err := s.store.RouteHistory(r.Context(), route, days)
	if err != nil {
		serverError(w, "route history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"route": route, "days": days, "daily": daily, "events": events,
	})
}

// queryDays parses ?days=N, clamped to [1, 90], default 14 when absent. A
// non-numeric value is a client mistake, so it gets a 400 rather than
// silently returning a window the caller never asked for; ok reports
// whether the caller should keep handling the request.
func queryDays(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("days")
	if raw == "" {
		return defaultHistoryDays, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "days must be an integer"})
		return 0, false
	}
	if n < 1 {
		return defaultHistoryDays, true
	}
	if n > 90 {
		return 90, true
	}
	return n, true
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
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}
