package store

import (
	"context"
	"errors"
	"testing"

	"transit-monitor/internal/testpg"
)

func openStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, testpg.Start(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s, ctx
}

func TestOpenBadDSN(t *testing.T) {
	if _, err := Open(context.Background(), "not-a-dsn"); err == nil {
		t.Fatal("want error for an unparseable DSN")
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	s, ctx := openStore(t)

	sub, err := s.UpsertSubscription(ctx, "rider@example.com", []string{"3", "90"})
	if err != nil {
		t.Fatal(err)
	}
	if sub.ID == 0 || len(sub.Token) != 64 {
		t.Fatalf("sub = %+v, want an id and a 32-byte hex token", sub)
	}
	if sub.VerifiedAt != nil {
		t.Error("new subscriptions must start unverified (double opt-in)")
	}

	verified, err := s.VerifySubscription(ctx, sub.Token)
	if err != nil {
		t.Fatal(err)
	}
	if verified.VerifiedAt == nil || verified.ID != sub.ID {
		t.Fatalf("verified = %+v", verified)
	}

	// Verifying twice keeps the original timestamp.
	again, err := s.VerifySubscription(ctx, sub.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !again.VerifiedAt.Equal(*verified.VerifiedAt) {
		t.Errorf("re-verify changed timestamp: %v -> %v", verified.VerifiedAt, again.VerifiedAt)
	}

	email, err := s.Unsubscribe(ctx, sub.Token)
	if err != nil {
		t.Fatal(err)
	}
	if email != "rider@example.com" {
		t.Errorf("Unsubscribe returned %q", email)
	}
	if _, err := s.Unsubscribe(ctx, sub.Token); !errors.Is(err, ErrNotFound) {
		t.Errorf("second unsubscribe err = %v, want ErrNotFound", err)
	}
}

func TestUpsertSubscriptionReissuesTokenAndResetsVerification(t *testing.T) {
	s, ctx := openStore(t)

	first, err := s.UpsertSubscription(ctx, "rider@example.com", []string{"3"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifySubscription(ctx, first.Token); err != nil {
		t.Fatal(err)
	}

	second, err := s.UpsertSubscription(ctx, "rider@example.com", []string{"3", "10"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Errorf("upsert created a second row: %d != %d", second.ID, first.ID)
	}
	if second.Token == first.Token {
		t.Error("route change must issue a fresh token")
	}
	if second.VerifiedAt != nil {
		t.Error("route change must reset verification")
	}
	if _, err := s.VerifySubscription(ctx, first.Token); !errors.Is(err, ErrNotFound) {
		t.Errorf("old token err = %v, want ErrNotFound", err)
	}
}

func TestUpsertSubscriptionNilRoutes(t *testing.T) {
	s, ctx := openStore(t)
	sub, err := s.UpsertSubscription(ctx, "empty@example.com", nil)
	if err != nil {
		t.Fatalf("nil routes must be coalesced to an empty array, got %v", err)
	}
	if len(sub.Routes) != 0 {
		t.Errorf("routes = %v, want empty", sub.Routes)
	}
}

func TestVerifyUnknownToken(t *testing.T) {
	s, ctx := openStore(t)
	if _, err := s.VerifySubscription(ctx, "deadbeef"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestNewTokensAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		tok, err := newToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 64 {
			t.Fatalf("token %q has length %d, want 64 hex chars", tok, len(tok))
		}
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
}
