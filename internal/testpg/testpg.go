// Package testpg boots a throwaway embedded Postgres for integration tests.
package testpg

import (
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// Start returns a DSN for a fresh Postgres that lives for the duration of
// the test. First-ever run downloads binaries (~50MB); later runs are fast.
// Skipped under -short.
func Start(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded-postgres integration test in -short mode")
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	dir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(uint32(port)).
		Database("transit_test").
		Username("postgres").
		Password("postgres").
		DataPath(filepath.Join(dir, "data")).
		RuntimePath(filepath.Join(dir, "runtime")).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	t.Cleanup(func() {
		if err := pg.Stop(); err != nil {
			t.Errorf("stop embedded postgres: %v", err)
		}
	})
	return fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/transit_test?sslmode=disable", port)
}
