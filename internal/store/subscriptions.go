package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

// Subscription as stored; Token drives both verify and unsubscribe links.
type Subscription struct {
	ID         int64
	Email      string
	Routes     []string
	Token      string
	VerifiedAt *time.Time
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// UpsertSubscription creates or replaces the subscription for an email.
// Any change (including route edits) resets verification and issues a new
// token, so every active subscription has clicked a confirm link for its
// current route set (double opt-in).
func (s *Store) UpsertSubscription(ctx context.Context, email string, routes []string) (Subscription, error) {
	token, err := newToken()
	if err != nil {
		return Subscription{}, err
	}
	if routes == nil {
		routes = []string{}
	}
	var sub Subscription
	err = s.pool.QueryRow(ctx, `
		INSERT INTO subscriptions (email, routes, token)
		VALUES ($1, $2, $3)
		ON CONFLICT (email) DO UPDATE SET
			routes = EXCLUDED.routes,
			token = EXCLUDED.token,
			verified_at = NULL,
			created_at = now()
		RETURNING id, email, routes, token, verified_at`,
		email, routes, token).
		Scan(&sub.ID, &sub.Email, &sub.Routes, &sub.Token, &sub.VerifiedAt)
	return sub, err
}

// VerifySubscription activates the subscription for a token. Returns the
// subscription; ErrNotFound for an unknown token. Verifying twice is a
// no-op (stays verified, original timestamp kept).
func (s *Store) VerifySubscription(ctx context.Context, token string) (Subscription, error) {
	var sub Subscription
	err := s.pool.QueryRow(ctx, `
		UPDATE subscriptions
		SET verified_at = COALESCE(verified_at, now())
		WHERE token = $1
		RETURNING id, email, routes, token, verified_at`,
		token).
		Scan(&sub.ID, &sub.Email, &sub.Routes, &sub.Token, &sub.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, ErrNotFound
	}
	return sub, err
}

// Unsubscribe deletes the subscription for a token (one-click, no login).
func (s *Store) Unsubscribe(ctx context.Context, token string) (string, error) {
	var email string
	err := s.pool.QueryRow(ctx,
		`DELETE FROM subscriptions WHERE token = $1 RETURNING email`, token).
		Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return email, err
}

// PendingNotification is one email that should be sent: a new-disruption
// event matched to a verified subscription that hasn't been notified yet.
type PendingNotification struct {
	EventID        int64
	SubscriptionID int64
	Email          string
	Token          string
	AlertID        string
	Header         string
	Effect         string
	Description    string
	AlertRoutes    []string
	SubRoutes      []string
}

// PendingNotifications matches unclaimed 'new' events against verified
// subscriptions by route overlap. Events that predate a subscription's
// verification are excluded, so a fresh subscriber isn't flooded with the
// existing backlog.
func (s *Store) PendingNotifications(ctx context.Context, limit int) ([]PendingNotification, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, s.id, s.email, s.token,
		       a.id, a.header, a.effect, a.description, a.routes, s.routes
		FROM alert_events e
		JOIN alerts a ON a.id = e.alert_id
		JOIN subscriptions s ON s.verified_at IS NOT NULL
		                    AND a.routes && s.routes
		WHERE e.kind = 'new'
		  AND e.occurred_at >= s.verified_at
		  AND NOT EXISTS (
		      SELECT 1 FROM notifications_sent ns
		      WHERE ns.subscription_id = s.id AND ns.event_id = e.id)
		ORDER BY e.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PendingNotification{}
	for rows.Next() {
		var p PendingNotification
		if err := rows.Scan(&p.EventID, &p.SubscriptionID, &p.Email, &p.Token,
			&p.AlertID, &p.Header, &p.Effect, &p.Description,
			&p.AlertRoutes, &p.SubRoutes); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClaimAndSend claims the (subscription, event) pair and calls send inside
// one transaction: if send fails the claim rolls back and is retried on a
// later pass; if the claim already exists (restart raced us) nothing is
// sent. Returns whether an email went out.
func (s *Store) ClaimAndSend(ctx context.Context, subscriptionID, eventID int64, send func() error) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO notifications_sent (subscription_id, event_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, subscriptionID, eventID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already sent
	}
	if err := send(); err != nil {
		return false, err // rollback claim; retried next pass
	}
	return true, tx.Commit(ctx)
}
