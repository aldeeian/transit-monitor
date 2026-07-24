package store

import (
	"context"
	"fmt"
)

// DefaultLocalPort is the port cmd/devdb serves its embedded Postgres on and
// the port LocalDSN points at.
const DefaultLocalPort = 5433

// LocalDSN is the default Postgres DSN for local development: the embedded
// database cmd/devdb starts. The dev CLIs fall back to it when neither -db
// nor $DATABASE_URL is set.
var LocalDSN = LocalDSNForPort(DefaultLocalPort)

// LocalDSNForPort builds the local-development DSN for a given Postgres port.
func LocalDSNForPort(port uint) string {
	return fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/transit?sslmode=disable", port)
}

// DSNOrLocal returns dsn unchanged, or LocalDSN when dsn is empty.
func DSNOrLocal(dsn string) string {
	if dsn == "" {
		return LocalDSN
	}
	return dsn
}

// OpenOrLocal opens the store at dsn, falling back to the local-development
// database when dsn is empty.
func OpenOrLocal(ctx context.Context, dsn string) (*Store, error) {
	return Open(ctx, DSNOrLocal(dsn))
}
