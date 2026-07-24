// Package notify turns new alert_events rows into subscriber emails. It
// runs on the ingestion poll cadence (no second poller): after each
// successful poll the caller invokes Run, which claims and sends anything
// pending. Claim + send share a transaction, so restarts can't duplicate.
package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"

	"transit-monitor/internal/email"
	"transit-monitor/internal/store"
)

type Notifier struct {
	Store     *store.Store
	Sender    email.Sender
	PublicURL string // base URL of the API, for verify/unsubscribe links
	Dashboard string // base URL of the dashboard, for "view status" links
}

// Run sends every pending notification and reports how many went out. A
// failure on one recipient doesn't block the batch: it stays unclaimed for
// the next pass and is joined into the returned error so the caller decides
// how to surface it.
func (n *Notifier) Run(ctx context.Context) (sent int, err error) {
	pending, err := n.Store.PendingNotifications(ctx, 100)
	if err != nil {
		return 0, fmt.Errorf("list pending: %w", err)
	}
	var errs []error
	for _, p := range pending {
		subject, body := n.alertEmail(p)
		ok, err := n.Store.ClaimAndSend(ctx, p.SubscriptionID, p.EventID, func() error {
			return n.Sender.Send(ctx, p.Email, subject, body)
		})
		if ok {
			sent++
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("send event %d to %s: %w", p.EventID, p.Email, err))
		}
	}
	return sent, errors.Join(errs...)
}

// matchedRoutes intersects the alert's routes with the subscriber's.
func matchedRoutes(alertRoutes, subRoutes []string) []string {
	subSet := map[string]bool{}
	for _, r := range subRoutes {
		subSet[r] = true
	}
	var out []string
	for _, r := range alertRoutes {
		if subSet[r] {
			out = append(out, r)
		}
	}
	return out
}

func (n *Notifier) alertEmail(p store.PendingNotification) (subject, body string) {
	matched := matchedRoutes(p.AlertRoutes, p.SubRoutes)
	if len(matched) == 0 {
		// The DB matched on route overlap, so this shouldn't happen; fall
		// back to the alert's routes rather than panicking mid-batch.
		matched = p.AlertRoutes
	}
	routes := strings.Join(matched, ", ")
	subject = fmt.Sprintf("[Calgary Transit] Route %s: %s", routes, p.Effect)

	var b strings.Builder
	fmt.Fprintf(&b, `<div style="font-family:ui-monospace,Consolas,monospace;max-width:600px">`)
	fmt.Fprintf(&b, `<p><strong>%s</strong> on route %s</p>`,
		html.EscapeString(strings.ToUpper(p.Effect)), html.EscapeString(routes))
	if p.Header != "" {
		fmt.Fprintf(&b, `<p>%s</p>`, html.EscapeString(p.Header))
	}
	if p.Description != "" && p.Description != p.Header {
		fmt.Fprintf(&b, `<pre style="white-space:pre-wrap;font-family:inherit;color:#444">%s</pre>`,
			html.EscapeString(p.Description))
	}
	if n.Dashboard != "" && len(matched) > 0 {
		fmt.Fprintf(&b, `<p><a href="%s/route/%s">Live status for route %s</a></p>`,
			n.Dashboard, html.EscapeString(matched[0]), html.EscapeString(routes))
	}
	fmt.Fprintf(&b, `<hr><p style="color:#888;font-size:12px">You get these because you subscribed to route alerts. `+
		`<a href="%s/api/unsubscribe?token=%s">Unsubscribe</a> (one click, no login).</p></div>`,
		n.PublicURL, p.Token)
	return subject, b.String()
}

// ConfirmationEmail builds the double-opt-in email sent on subscribe.
func ConfirmationEmail(publicURL string, sub store.Subscription) (subject, body string) {
	routes := strings.Join(sub.Routes, ", ")
	subject = fmt.Sprintf("[Calgary Transit] Confirm alerts for route %s", routes)
	body = fmt.Sprintf(`<div style="font-family:ui-monospace,Consolas,monospace;max-width:600px">`+
		`<p>Confirm your subscription to Calgary Transit disruption alerts for route(s): <strong>%s</strong></p>`+
		`<p><a href="%s/api/verify?token=%s">Confirm subscription</a></p>`+
		`<p style="color:#888;font-size:12px">No alerts are sent until you confirm. `+
		`If you didn't request this, ignore this email — the subscription stays inactive.</p></div>`,
		html.EscapeString(routes), publicURL, sub.Token)
	return subject, body
}
