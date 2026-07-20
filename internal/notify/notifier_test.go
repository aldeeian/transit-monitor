package notify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"transit-monitor/internal/gtfsrt"
	"transit-monitor/internal/store"
	"transit-monitor/internal/testpg"
)

// fakeSender records sends and can fail on demand per recipient.
type fakeSender struct {
	mu     sync.Mutex
	sent   []string // "email|subject"
	failTo map[string]bool
}

func (f *fakeSender) Send(_ context.Context, to, subject, htmlBody string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failTo[to] {
		return errors.New("simulated provider outage")
	}
	f.sent = append(f.sent, to+"|"+subject+"|"+htmlBody)
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func setup(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, testpg.Start(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, ctx
}

func ingest(t *testing.T, s *store.Store, ctx context.Context, prev map[string]string, events ...gtfsrt.Event) map[string]string {
	t.Helper()
	if err := s.ApplyPoll(ctx, gtfsrt.Diff(prev, events), events); err != nil {
		t.Fatal(err)
	}
	return gtfsrt.FingerprintMap(events)
}

func alert(id string, routes ...string) gtfsrt.Event {
	return gtfsrt.Event{
		ID: id, Header: "hdr " + id, Description: "desc " + id,
		Effect: "detour", Routes: routes, Fingerprint: "fp-" + id,
	}
}

func verifiedSub(t *testing.T, s *store.Store, ctx context.Context, email string, routes ...string) store.Subscription {
	t.Helper()
	sub, err := s.UpsertSubscription(ctx, email, routes)
	if err != nil {
		t.Fatal(err)
	}
	sub, err = s.VerifySubscription(ctx, sub.Token)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestNotifierSendsOncePerSubscriberEvent(t *testing.T) {
	s, ctx := setup(t)
	verifiedSub(t, s, ctx, "a@example.com", "3")
	verifiedSub(t, s, ctx, "b@example.com", "3", "90")

	// Unverified subscriber must never receive anything.
	if _, err := s.UpsertSubscription(ctx, "ghost@example.com", []string{"3"}); err != nil {
		t.Fatal(err)
	}

	prev := ingest(t, s, ctx, map[string]string{}, alert("d1", "3"), alert("d2", "90"), alert("d3", "7"))

	f := &fakeSender{}
	n := &Notifier{Store: s, Sender: f, PublicURL: "http://api.test", Dashboard: "http://dash.test"}

	// d1(route 3) -> a and b; d2(route 90) -> b; d3(route 7) -> nobody. = 3
	if sent := n.Run(ctx); sent != 3 {
		t.Fatalf("first pass sent %d, want 3", sent)
	}
	// Second pass (same DB state): nothing new.
	if sent := n.Run(ctx); sent != 0 {
		t.Fatalf("second pass sent %d, want 0 (idempotency)", sent)
	}

	// Notifier "restart": a brand-new instance must also send nothing.
	n2 := &Notifier{Store: s, Sender: f, PublicURL: "http://api.test"}
	if sent := n2.Run(ctx); sent != 0 {
		t.Fatalf("restarted notifier resent %d emails, want 0", sent)
	}

	for _, msg := range f.sent {
		if strings.Contains(msg, "ghost@example.com") {
			t.Error("unverified subscriber received email")
		}
		if !strings.Contains(msg, "/api/unsubscribe?token=") {
			t.Error("email missing unsubscribe link")
		}
	}

	// New alert later: only route-3 subscribers notified, exactly once each.
	ingest(t, s, ctx, prev, alert("d1", "3"), alert("d2", "90"), alert("d3", "7"), alert("d4", "3"))
	if sent := n.Run(ctx); sent != 2 {
		t.Fatalf("after new alert sent %d, want 2", sent)
	}
}

func TestNotifierRetriesFailedSendsWithoutDuplicating(t *testing.T) {
	s, ctx := setup(t)
	verifiedSub(t, s, ctx, "ok@example.com", "3")
	verifiedSub(t, s, ctx, "down@example.com", "3")
	ingest(t, s, ctx, map[string]string{}, alert("d1", "3"))

	// Provider fails for one recipient mid-batch — like a crash/outage.
	f := &fakeSender{failTo: map[string]bool{"down@example.com": true}}
	n := &Notifier{Store: s, Sender: f, PublicURL: "http://api.test"}
	if sent := n.Run(ctx); sent != 1 {
		t.Fatalf("outage pass sent %d, want 1", sent)
	}

	// Provider recovers; retry pass sends ONLY the failed one.
	f.failTo = nil
	if sent := n.Run(ctx); sent != 1 {
		t.Fatalf("retry pass sent %d, want exactly 1", sent)
	}
	if f.count() != 2 {
		t.Fatalf("total sends %d, want 2 (no duplicates for ok@)", f.count())
	}
	if sent := n.Run(ctx); sent != 0 {
		t.Fatal("third pass should send nothing")
	}
}

func TestSubscriberNotFloodedWithBacklog(t *testing.T) {
	s, ctx := setup(t)
	// Disruption exists BEFORE the subscription is verified.
	ingest(t, s, ctx, map[string]string{}, alert("old1", "3"))

	verifiedSub(t, s, ctx, "late@example.com", "3")
	f := &fakeSender{}
	n := &Notifier{Store: s, Sender: f, PublicURL: "http://api.test"}
	if sent := n.Run(ctx); sent != 0 {
		t.Fatalf("new subscriber got %d backlog emails, want 0", sent)
	}
}

func TestResubscribeResetsVerification(t *testing.T) {
	s, ctx := setup(t)
	sub := verifiedSub(t, s, ctx, "x@example.com", "3")

	// Re-subscribing (changing routes) must reset verification + token.
	sub2, err := s.UpsertSubscription(ctx, "x@example.com", []string{"90"})
	if err != nil {
		t.Fatal(err)
	}
	if sub2.VerifiedAt != nil {
		t.Error("resubscribe should reset verified_at")
	}
	if sub2.Token == sub.Token {
		t.Error("resubscribe should rotate the token")
	}

	// Old token is dead for verification.
	if _, err := s.VerifySubscription(ctx, sub.Token); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old token should be invalid, got %v", err)
	}

	// Until re-verified, no emails.
	ingest(t, s, ctx, map[string]string{}, alert("d9", "90"))
	f := &fakeSender{}
	n := &Notifier{Store: s, Sender: f, PublicURL: "http://api.test"}
	if sent := n.Run(ctx); sent != 0 {
		t.Fatalf("unre-verified subscriber got %d emails, want 0", sent)
	}
}

func TestUnsubscribeStopsEmails(t *testing.T) {
	s, ctx := setup(t)
	sub := verifiedSub(t, s, ctx, "bye@example.com", "3")

	email, err := s.Unsubscribe(ctx, sub.Token)
	if err != nil || email != "bye@example.com" {
		t.Fatalf("unsubscribe: %v %q", err, email)
	}
	// Second click: friendly not-found, not an error state.
	if _, err := s.Unsubscribe(ctx, sub.Token); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second unsubscribe should be ErrNotFound, got %v", err)
	}

	ingest(t, s, ctx, map[string]string{}, alert("d1", "3"))
	f := &fakeSender{}
	n := &Notifier{Store: s, Sender: f, PublicURL: "http://api.test"}
	if sent := n.Run(ctx); sent != 0 {
		t.Fatalf("unsubscribed address got %d emails", sent)
	}
}
