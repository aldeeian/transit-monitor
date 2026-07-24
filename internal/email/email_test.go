package email

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevSenderWritesFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbox")
	s := &DevSender{Dir: dir}
	if err := s.Send(context.Background(), "rider@example.com", "Route 3: detour", "<p>hi</p>"); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("wrote %d files, want 1", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasSuffix(name, ".html") || !strings.Contains(name, "rider@example.com") {
		t.Errorf("filename = %q, want recipient and .html suffix", name)
	}
	if strings.ContainsAny(name, " :/") {
		t.Errorf("filename %q should have unsafe characters sanitized", name)
	}

	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<!-- to: rider@example.com -->", "<!-- subject: Route 3: detour -->", "<p>hi</p>"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestDevSenderTwoSendsTwoFiles(t *testing.T) {
	dir := t.TempDir()
	s := &DevSender{Dir: dir}
	for _, sub := range []string{"first", "second"} {
		if err := s.Send(context.Background(), "a@b.com", sub, "x"); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("wrote %d files, want 2 (filenames must not collide)", len(entries))
	}
}

func TestDevSenderUnwritableDir(t *testing.T) {
	// A file where the directory should be: MkdirAll must fail.
	f := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &DevSender{Dir: filepath.Join(f, "outbox")}
	if err := s.Send(context.Background(), "a@b.com", "s", "b"); err == nil {
		t.Fatal("want error when the outbox dir cannot be created")
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"rider@example.com", "rider@example.com"},
		{"a b/c:d", "a_b_c_d"},
		{"keep-dots.and-dashes", "keep-dots.and-dashes"},
		{"héllo", "h_llo"}, // multi-byte runes collapse to a single '_'
		{strings.Repeat("x", 100), strings.Repeat("x", 60)},
	}
	for _, tt := range tests {
		if got := sanitize(tt.in); got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResendSenderSuccess(t *testing.T) {
	var gotAuth, gotType, gotPath string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("bad json payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &ResendSender{APIKey: "key123", From: "TM <t@m.dev>", Client: redirectTo(srv.URL)}
	if err := s.Send(context.Background(), "rider@example.com", "subj", "<p>b</p>"); err != nil {
		t.Fatal(err)
	}

	if gotPath != "/emails" {
		t.Errorf("path = %q, want /emails", gotPath)
	}
	if gotAuth != "Bearer key123" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotType != "application/json" {
		t.Errorf("content-type = %q", gotType)
	}
	if payload["from"] != "TM <t@m.dev>" || payload["subject"] != "subj" || payload["html"] != "<p>b</p>" {
		t.Errorf("payload = %v", payload)
	}
	to, ok := payload["to"].([]any)
	if !ok || len(to) != 1 || to[0] != "rider@example.com" {
		t.Errorf("to = %v, want single recipient array", payload["to"])
	}
}

func TestResendSenderNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "domain not verified")
	}))
	defer srv.Close()

	s := &ResendSender{APIKey: "k", Client: redirectTo(srv.URL)}
	err := s.Send(context.Background(), "a@b.com", "s", "b")
	if err == nil {
		t.Fatal("want error for 403")
	}
	// handleSubscribe keys its sandbox message off the status in this string.
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "domain not verified") {
		t.Errorf("err = %v, want status and body", err)
	}
}

func TestResendSenderTransportError(t *testing.T) {
	s := &ResendSender{APIKey: "k", Client: &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: refused")
		}),
	}}
	if err := s.Send(context.Background(), "a@b.com", "s", "b"); err == nil {
		t.Fatal("want error when the transport fails")
	}
}

func TestResendSenderCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &ResendSender{APIKey: "k"} // nil Client exercises the default client
	if err := s.Send(ctx, "a@b.com", "s", "b"); err == nil {
		t.Fatal("want error for a cancelled context")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// redirectTo sends requests for api.resend.com to a test server instead.
func redirectTo(base string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		u := *r.URL
		target, err := http.NewRequestWithContext(r.Context(), r.Method, base+u.Path, r.Body)
		if err != nil {
			return nil, err
		}
		target.Header = r.Header
		return http.DefaultTransport.RoundTrip(target)
	})}
}
