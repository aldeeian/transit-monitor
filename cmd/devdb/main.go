// devdb runs a persistent local Postgres for development with zero system
// install: binaries and data live under .devdb/ (gitignored). Ctrl-C stops
// it cleanly; data survives restarts.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"transit-monitor/internal/store"
)

func main() {
	port := flag.Uint("port", store.DefaultLocalPort, "port to listen on")
	dir := flag.String("dir", ".devdb", "directory for binaries + data")
	flag.Parse()

	abs, err := filepath.Abs(*dir)
	if err != nil {
		log.Fatal(err)
	}

	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(uint32(*port)).
		Database("transit").
		Username("postgres").
		Password("postgres").
		DataPath(filepath.Join(abs, "data")).
		RuntimePath(filepath.Join(abs, "runtime")).
		StartTimeout(120 * time.Second))

	log.Printf("starting postgres on 127.0.0.1:%d (data in %s)...", *port, abs)
	if err := pg.Start(); err != nil {
		log.Fatalf("start: %v", err)
	}
	fmt.Printf("\nready. DSN:\n  %s\n\nctrl-c to stop.\n", store.LocalDSNForPort(*port))

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch

	log.Print("stopping postgres...")
	if err := pg.Stop(); err != nil {
		log.Fatalf("stop: %v", err)
	}
	log.Print("stopped cleanly")
}
