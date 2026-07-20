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

	"transit-monitor/internal/gtfsrt"
)

func main() {
	alertsURL := flag.String("alerts-url", gtfsrt.DefaultAlertsURL, "GTFS-RT service alerts feed URL")
	interval := flag.Duration("interval", 30*time.Second, "poll interval")
	once := flag.Bool("once", false, "poll a single time and exit")
	flag.Parse()

	log.SetFlags(log.Ltime)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fetcher := gtfsrt.NewFetcher(*alertsURL)
	prev := map[string]string(nil) // nil = first poll, print full snapshot

	poll := func() {
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		msg, err := fetcher.Fetch(fctx)
		if err != nil {
			log.Printf("ERROR poll failed (will retry next tick): %v", err)
			return
		}
		events := gtfsrt.NormalizeAlerts(msg)
		ts := gtfsrt.FeedTimestamp(msg)

		if prev == nil {
			log.Printf("initial snapshot: %d active alerts (feed timestamp %s)", len(events), ts.Format(time.Kitchen))
			for _, ev := range events {
				printEvent("ACTIVE", ev)
			}
		} else {
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
		}
		prev = gtfsrt.FingerprintMap(events)
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
		case <-ticker.C:
			poll()
		}
	}
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
