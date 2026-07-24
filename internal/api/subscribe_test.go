package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"transit-monitor/internal/store"
	"transit-monitor/internal/testpg"
)

// recordingSender captures sent mail and can fail on demand.
type recordingSender struct {
	mu   sync.Mutex
	sent []struct{ To, Subject, Body string }
	err  error
}

func (s *recordingSender) Send(_ context.Context, to, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, struct{ To, Subject, Body string }{to, subject, body})
	return nil
}

func (s *recordingSender) last() (string, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return "", "", ""
	}
	m := s.sent[len(s.sent)-1]
	return m.To, m.Subject, m.Body
}

func emptyStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), testpg.Start(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// subscribeServer returns a handler with subscriptions enabled plus its sender.
func subscribeServer(t *testing.T, st *store.Store) (http.Handler, *recordingSender) {
	t.Helper()
	sender := &recordingSender{}
	srv := NewServer(st, &Health{}, time.Minute)
	srv.EnableSubscriptions(sender, "https://api.example.com/")
	if srv.publicURL != "https://api.example.com" {
		t.Fatalf("publicURL = %q, want trailing slash trimmed", srv.publicURL)
	}
	return srv.Handler(), sender
}

// postSubscribe sends a subscribe request from a unique IP so per-IP rate
// limits from other subtests don't interfere.
func postSubscribe(t *testing.T, h http.Handler, ip, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", strings.NewReader(body))
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, out
}

func TestSubscribeDisabled(t *testing.T) {
	st := emptyStore(t)
	h := NewServer(st, &Health{}, time.Minute).Handler()
	code, body := postSubscribe(t, h, "10.0.0.1", `{"email":"a@b.com","routes":["3"]}`)
	if code != http.StatusServiceUnavailable || !strings.Contains(body["error"].(string), "not enabled") {
		t.Fatalf("code=%d body=%v, want 503 subscriptions not enabled", code, body)
	}
}

func TestSubscribeHappyPath(t *testing.T) {
	st := emptyStore(t)
	h, sender := subscribeServer(t, st)

	code, body := postSubscribe(t, h, "10.0.1.1", `{"email":" Rider@Example.COM ","routes":["3"," 90 ",""]}`)
	if code != http.StatusOK || body["status"] != "confirmation_sent" {
		t.Fatalf("code=%d body=%v", code, body)
	}

	to, subject, mailBody := sender.last()
	if to != "rider@example.com" {
		t.Errorf("recipient = %q, want lowercased address", to)
	}
	if !strings.Contains(subject, "Confirm") {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(mailBody, "https://api.example.com/api/verify?token=") {
		t.Errorf("confirmation body missing verify link: %s", mailBody)
	}

	// Nothing is active until the link is clicked.
	sub, err := st.VerifySubscription(context.Background(), tokenFrom(t, mailBody))
	if err != nil {
		t.Fatal(err)
	}
	if sub.VerifiedAt == nil {
		t.Error("subscription should be verified after following the link")
	}
	if strings.Join(sub.Routes, ",") != "3,90" {
		t.Errorf("routes = %v, want blank entries dropped and values trimmed", sub.Routes)
	}
}

func TestSubscribeRejectsBadInput(t *testing.T) {
	st := emptyStore(t)
	h, _ := subscribeServer(t, st)

	tests := []struct {
		name, ip, body, wantErr string
	}{
		{"malformed json", "10.0.2.1", `{"email":`, "invalid JSON"},
		{"bad email", "10.0.2.2", `{"email":"not-an-email","routes":["3"]}`, "invalid email"},
		{"no routes", "10.0.2.3", `{"email":"none@b.com","routes":[]}`, "1 and 20 routes"},
		{"only blank routes", "10.0.2.4", `{"email":"blank@b.com","routes":["","  "]}`, "1 and 20 routes"},
		{"route too long", "10.0.2.5", `{"email":"long@b.com","routes":["12345678901"]}`, "1 and 20 routes"},
		{"too many routes", "10.0.2.6", `{"email":"many@b.com","routes":` + routesJSON(21) + `}`, "1 and 20 routes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := postSubscribe(t, h, tt.ip, tt.body)
			if code != http.StatusBadRequest || !strings.Contains(body["error"].(string), tt.wantErr) {
				t.Fatalf("code=%d body=%v, want 400 containing %q", code, body, tt.wantErr)
			}
		})
	}
}

func TestSubscribeRateLimits(t *testing.T) {
	st := emptyStore(t)
	h, _ := subscribeServer(t, st)

	t.Run("per email", func(t *testing.T) {
		// 3 confirmation emails per address per day, from any IP.
		for i := range 3 {
			code, body := postSubscribe(t, h, fmt.Sprintf("10.1.0.%d", i), `{"email":"limit@b.com","routes":["3"]}`)
			if code != http.StatusOK {
				t.Fatalf("attempt %d: code=%d body=%v", i, code, body)
			}
		}
		code, body := postSubscribe(t, h, "10.1.0.9", `{"email":"limit@b.com","routes":["3"]}`)
		if code != http.StatusTooManyRequests || !strings.Contains(body["error"].(string), "this address") {
			t.Fatalf("code=%d body=%v, want 429 per-email limit", code, body)
		}
	})

	t.Run("per ip", func(t *testing.T) {
		// 5 subscribe attempts per IP per hour, across addresses.
		for i := range 5 {
			code, body := postSubscribe(t, h, "10.2.0.1", fmt.Sprintf(`{"email":"ip%d@b.com","routes":["3"]}`, i))
			if code != http.StatusOK {
				t.Fatalf("attempt %d: code=%d body=%v", i, code, body)
			}
		}
		code, body := postSubscribe(t, h, "10.2.0.1", `{"email":"ip9@b.com","routes":["3"]}`)
		if code != http.StatusTooManyRequests || !strings.Contains(body["error"].(string), "subscribe attempts") {
			t.Fatalf("code=%d body=%v, want 429 per-IP limit", code, body)
		}
	})
}

func TestSubscribeSenderFailures(t *testing.T) {
	st := emptyStore(t)

	t.Run("sandbox 403 surfaces as 503", func(t *testing.T) {
		srv := NewServer(st, &Health{}, time.Minute)
		srv.EnableSubscriptions(&recordingSender{err: errors.New("resend returned 403: not verified")}, "https://api.example.com")
		code, body := postSubscribe(t, srv.Handler(), "10.3.0.1", `{"email":"sandbox@b.com","routes":["3"]}`)
		if code != http.StatusServiceUnavailable || !strings.Contains(body["error"].(string), "limited beta") {
			t.Fatalf("code=%d body=%v, want 503 beta message", code, body)
		}
	})

	t.Run("other failure is 500", func(t *testing.T) {
		srv := NewServer(st, &Health{}, time.Minute)
		srv.EnableSubscriptions(&recordingSender{err: errors.New("connection reset")}, "https://api.example.com")
		code, body := postSubscribe(t, srv.Handler(), "10.3.0.2", `{"email":"boom@b.com","routes":["3"]}`)
		if code != http.StatusInternalServerError || body["error"] != "internal error" {
			t.Fatalf("code=%d body=%v, want 500 internal error", code, body)
		}
	})
}

func TestVerifyAndUnsubscribePages(t *testing.T) {
	st := emptyStore(t)
	h, sender := subscribeServer(t, st)

	code, _ := postSubscribe(t, h, "10.4.0.1", `{"email":"pages@b.com","routes":["3","10"]}`)
	if code != http.StatusOK {
		t.Fatalf("subscribe failed: %d", code)
	}
	_, _, mailBody := sender.last()
	token := tokenFrom(t, mailBody)

	getHTML := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s content-type = %q, want html", path, ct)
		}
		return rec.Code, rec.Body.String()
	}

	code, page := getHTML("/api/verify?token=" + token)
	if code != http.StatusOK || !strings.Contains(page, "Subscription confirmed") ||
		!strings.Contains(page, "3, 10") || !strings.Contains(page, "pages@b.com") {
		t.Fatalf("verify page: code=%d body=%s", code, page)
	}

	code, page = getHTML("/api/verify?token=bogus")
	if code != http.StatusNotFound || !strings.Contains(page, "Invalid link") {
		t.Fatalf("bad token: code=%d body=%s", code, page)
	}

	code, page = getHTML("/api/unsubscribe?token=" + token)
	if code != http.StatusOK || !strings.Contains(page, "Unsubscribed") || !strings.Contains(page, "pages@b.com") {
		t.Fatalf("unsubscribe page: code=%d body=%s", code, page)
	}

	// One-click links are single use.
	code, page = getHTML("/api/unsubscribe?token=" + token)
	if code != http.StatusNotFound || !strings.Contains(page, "Already unsubscribed") {
		t.Fatalf("second unsubscribe: code=%d body=%s", code, page)
	}
}

func TestCORSPreflight(t *testing.T) {
	st := emptyStore(t)
	h := NewServer(st, &Health{}, time.Minute).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/api/subscribe", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight code = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("preflight response missing allowed methods")
	}
}

func tokenFrom(t *testing.T, mailBody string) string {
	t.Helper()
	const marker = "token="
	i := strings.Index(mailBody, marker)
	if i < 0 {
		t.Fatalf("no token in email body: %s", mailBody)
	}
	rest := mailBody[i+len(marker):]
	if j := strings.IndexAny(rest, `"'<`); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func routesJSON(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf(`"r%d"`, i)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
