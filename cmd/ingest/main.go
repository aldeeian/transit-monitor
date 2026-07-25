package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"transit-monitor/internal/api"
	"transit-monitor/internal/email"
	"transit-monitor/internal/gtfsrt"
	"transit-monitor/internal/notify"
	"transit-monitor/internal/store"
)

func main() {
	alertsURL := flag.String("alerts-url", envOr("ALERTS_URL", gtfsrt.DefaultAlertsURL), "GTFS-RT service alerts feed URL")
	interval := flag.Duration("interval", 30*time.Second, "poll interval")
	once := flag.Bool("once", false, "poll a single time and exit")
	dsn := flag.String("db", os.Getenv("DATABASE_URL"), "Postgres DSN (empty = stdout-only, no persistence)")
	listen := flag.String("listen", defaultListen(), "API listen address (empty = no API; requires -db)")
	publicURL := flag.String("public-url", envOr("PUBLIC_URL", "http://127.0.0.1:8080"), "externally reachable API base URL for email links")
	dashURL := flag.String("dashboard-url", envOr("DASHBOARD_URL", "http://127.0.0.1:3000"), "dashboard base URL for email links")
	emailFrom := flag.String("email-from", envOr("EMAIL_FROM", "Transit Monitor <onboarding@resend.dev>"), "From address for outgoing email")
	outbox := flag.String("outbox", ".outbox", "dir for dev-mode emails (used when RESEND_API_KEY is unset)")
	flag.Parse()

	log.SetFlags(log.Ltime)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fetcher := gtfsrt.NewFetcher(*alertsURL)
	prev := map[string]string(nil) // nil = first poll in stdout-only mode: print full snapshot

	var st *store.Store
	if *dsn != "" {
		var err error
		st, err = store.Open(ctx, *dsn)
		if err != nil {
			log.Fatalf("FATAL db: %v", err)
		}
		defer st.Close()
		prev, err = st.ActiveFingerprints(ctx)
		if err != nil {
			log.Fatalf("FATAL load fingerprints: %v", err)
		}
		log.Printf("db connected: %d known active alerts", len(prev))
	}

	// Email: real Resend sender when a key is present, otherwise .html
	// files in the outbox dir — same pipeline either way.
	var sender email.Sender
	if key := os.Getenv("RESEND_API_KEY"); key != "" {
		sender = &email.ResendSender{APIKey: key, From: *emailFrom}
		log.Printf("email: sending via Resend as %q", *emailFrom)
	} else {
		sender = &email.DevSender{Dir: *outbox}
		log.Printf("email: DEV MODE, writing to %s (set RESEND_API_KEY for real sending)", *outbox)
	}

	var notifier *notify.Notifier
	if st != nil {
		notifier = &notify.Notifier{
			Store: st, Sender: sender,
			PublicURL: strings.TrimRight(*publicURL, "/"),
			Dashboard: strings.TrimRight(*dashURL, "/"),
		}
	}

	health := &api.Health{}
	apiErrCh := make(chan error, 1)
	if *listen != "" && st != nil && !*once {
		// Health goes stale after 3 missed polls.
		srv := api.NewServer(st, health, 3**interval)
		srv.EnableSubscriptions(sender, *publicURL)
		go func() { apiErrCh <- srv.Run(ctx, *listen) }()
	}

	poll := func() {
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		msg, err := fetcher.Fetch(fctx)
		if err != nil {
			log.Printf("ERROR poll failed (will retry next tick): %v", err)
			health.RecordFailure()
			return
		}
		events := gtfsrt.NormalizeAlerts(msg)
		ts := gtfsrt.FeedTimestamp(msg)

		if prev == nil {
			log.Printf("initial snapshot: %d active alerts (feed timestamp %s)", len(events), ts.Format(time.Kitchen))
			for _, ev := range events {
				printEvent("ACTIVE", ev)
			}
			prev = gtfsrt.FingerprintMap(events)
			health.RecordSuccess(ts, len(events))
			return
		}

		d := gtfsrt.Diff(prev, events)
		if len(d.New)+len(d.Changed)+len(d.Resolved) == 0 {
			log.Printf("no changes: %d active alerts (feed timestamp %s)", len(events), ts.Format(time.Kitchen))
		}
		for _, ev := range d.New {
			printEvent("NEW", ev)
		}
		for _, ev := range d.Changed {
			printEvent("UPDATED", ev)
		}
		for _, id := range d.Resolved {
			log.Printf("[RESOLVED] alert %s no longer in feed", id)
		}

		if st != nil {
			// Only advance prev after a successful commit, so a failed
			// write is retried (same diff) on the next tick instead of
			// silently dropping events.
			if err := st.ApplyPoll(fctx, d, events); err != nil {
				log.Printf("ERROR db write failed (will retry next tick): %v", err)
				health.RecordFailure()
				return
			}
		}
		prev = gtfsrt.FingerprintMap(events)
		health.RecordSuccess(ts, len(events))

		// Notify on the poll cadence: anything just written (or left over
		// from a previous failed pass) goes out now.
		if notifier != nil {
			n, err := notifier.Run(fctx)
			if n > 0 {
				log.Printf("notify: sent %d email(s)", n)
			}
			if err != nil {
				log.Printf("ERROR notify (will retry next tick): %v", err)
			}
		}
	}

	poll()
	if *once {
		return
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	log.Printf("polling every %s (ctrl-c to stop)", *interval)
	for {
		select {
		case <-ctx.Done():
			log.Printf("shutting down")
			return
		case err := <-apiErrCh:
			// A dead API (port taken, listener closed) must not leave the
			// process running and looking healthy to the platform.
			if err != nil {
				log.Printf("FATAL api server: %v", err)
				st.Close()
				os.Exit(1)
			}
			log.Printf("api server stopped")
			return
		case <-ticker.C:
			poll()
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultListen resolves the API bind address: LISTEN_ADDR wins, then a
// platform-provided PORT (Railway/Render style, bind all interfaces), then
// localhost for development.
func defaultListen() string {
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		return v
	}
	if p := os.Getenv("PORT"); p != "" {
		return "0.0.0.0:" + p
	}
	return "127.0.0.1:8080"
}

func printEvent(tag string, ev gtfsrt.Event) {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s | %s", tag, ev.ID, ev.Effect)
	if ev.Cause != "" && ev.Cause != "unknown cause" {
		fmt.Fprintf(&b, " (%s)", ev.Cause)
	}
	if len(ev.Routes) > 0 {
		fmt.Fprintf(&b, " | routes: %s", strings.Join(ev.Routes, ", "))
	}
	if len(ev.Stops) > 0 {
		fmt.Fprintf(&b, " | %d stop(s)", len(ev.Stops))
	}
	if ev.Header != "" {
		fmt.Fprintf(&b, "\n          %s", ev.Header)
	}
	if ev.Starts != nil {
		fmt.Fprintf(&b, "\n          from %s", ev.Starts.Format("Jan 2 15:04"))
		if ev.Ends != nil {
			fmt.Fprintf(&b, " until %s", ev.Ends.Format("Jan 2 15:04"))
		}
	}
	log.Print(b.String())
}
