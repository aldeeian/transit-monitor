package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"transit-monitor/internal/notify"
	"transit-monitor/internal/store"
)

type subscribeRequest struct {
	Email  string   `json:"email"`
	Routes []string `json:"routes"`
}

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.sender == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "subscriptions not enabled"})
		return
	}
	// The endpoint sends email, so it gets abuse limits: per-IP and
	// per-email. Generous for humans, hostile to scripts.
	if !s.limiter.allow("ip:"+clientIP(r), 5, time.Hour) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many subscribe attempts, try again later"})
		return
	}
	var req subscribeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(req.Email))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email address"})
		return
	}
	if !s.limiter.allow("email:"+strings.ToLower(addr.Address), 3, 24*time.Hour) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many confirmation emails for this address today"})
		return
	}
	var routes []string
	for _, rt := range req.Routes {
		rt = strings.TrimSpace(rt)
		if rt != "" && len(rt) <= 10 {
			routes = append(routes, rt)
		}
	}
	if len(routes) == 0 || len(routes) > 20 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pick between 1 and 20 routes"})
		return
	}

	sub, err := s.store.UpsertSubscription(r.Context(), strings.ToLower(addr.Address), routes)
	if err != nil {
		serverError(w, "upsert subscription", err)
		return
	}
	subject, body := notify.ConfirmationEmail(s.publicURL, sub)
	if err := s.sender.Send(r.Context(), sub.Email, subject, body); err != nil {
		serverError(w, "send confirmation", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "confirmation_sent",
		"detail": "Check your email and click the confirmation link. No alerts are sent until then.",
	})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	sub, err := s.store.VerifySubscription(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) {
		htmlPage(w, http.StatusNotFound, "Invalid link",
			"This confirmation link is invalid or was replaced by a newer one. Subscribe again to get a fresh link.")
		return
	}
	if err != nil {
		serverError(w, "verify", err)
		return
	}
	htmlPage(w, http.StatusOK, "Subscription confirmed",
		fmt.Sprintf("You'll now get alerts for route(s) <strong>%s</strong> at %s. New disruptions reach you within about a minute of Calgary Transit publishing them.",
			html.EscapeString(strings.Join(sub.Routes, ", ")), html.EscapeString(sub.Email)))
}

func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	email, err := s.store.Unsubscribe(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) {
		htmlPage(w, http.StatusNotFound, "Already unsubscribed",
			"This link was already used or is invalid. You will not receive further alerts.")
		return
	}
	if err != nil {
		serverError(w, "unsubscribe", err)
		return
	}
	htmlPage(w, http.StatusOK, "Unsubscribed",
		fmt.Sprintf("%s will receive no further alerts. Resubscribe on the dashboard any time.", html.EscapeString(email)))
}

// htmlPage renders the tiny pages behind email links, styled to match the
// dashboard's utilitarian look.
func htmlPage(w http.ResponseWriter, code int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s — transit-monitor</title>
<body style="background:#0b0e11;color:#c9d1d9;font-family:ui-monospace,Consolas,monospace;display:grid;place-items:center;min-height:90vh;margin:0">
<div style="max-width:480px;padding:24px;border:1px solid #21262f">
<div style="color:#8b949e;font-size:12px;margin-bottom:8px">CALGARY TRANSIT MONITOR</div>
<h1 style="font-size:18px;margin:0 0 12px">%s</h1>
<p style="line-height:1.6;color:#9da7b1;font-size:14px;margin:0">%s</p>
</div>`, html.EscapeString(title), html.EscapeString(title), msg)
}
