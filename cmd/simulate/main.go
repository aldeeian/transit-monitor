// simulate injects a synthetic disruption event into the DB — used to test
// the notification pipeline end-to-end without waiting for Calgary Transit
// to have a real incident on your route.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"transit-monitor/internal/gtfsrt"
	"transit-monitor/internal/store"
)

func main() {
	dsn := flag.String("db", os.Getenv("DATABASE_URL"), "Postgres DSN")
	routes := flag.String("routes", "999", "comma-separated route IDs to disrupt")
	id := flag.String("id", fmt.Sprintf("SIM-%d", time.Now().Unix()), "alert ID")
	effect := flag.String("effect", "significant delays", "effect text")
	flag.Parse()

	ctx := context.Background()
	s, err := store.OpenOrLocal(ctx, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	rts := strings.Split(*routes, ",")
	for i := range rts {
		rts[i] = strings.TrimSpace(rts[i])
	}
	ev := gtfsrt.Event{
		ID:          *id,
		Routes:      rts,
		Header:      fmt.Sprintf("SIMULATED: %s on route %s", *effect, *routes),
		Description: "This is a synthetic disruption injected by cmd/simulate to test the alert pipeline.",
		Effect:      *effect,
		Fingerprint: "sim-" + *id,
	}
	d := gtfsrt.DiffResult{New: []gtfsrt.Event{ev}}
	if err := s.ApplyPoll(ctx, d, nil); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("injected alert %s on route(s) %s — the running ingest service should email subscribers within one poll interval\n", *id, *routes)
}
