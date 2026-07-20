// dbstats prints a quick operational summary of the database.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"transit-monitor/internal/store"
)

func main() {
	dsn := flag.String("db", os.Getenv("DATABASE_URL"), "Postgres DSN")
	flag.Parse()
	if *dsn == "" {
		*dsn = "postgres://postgres:postgres@127.0.0.1:5433/transit?sslmode=disable"
	}

	s, err := store.Open(context.Background(), *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	st, err := s.Stats(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("active alerts:   %d\nresolved alerts: %d\nevent rows:      %d\n",
		st.ActiveAlerts, st.ResolvedAlerts, st.EventRows)
}
