// sqlq runs one SQL statement against the DB and prints rows tab-separated.
// Dev/ops helper (the embedded Postgres runtime ships no psql client).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn := flag.String("db", os.Getenv("DATABASE_URL"), "Postgres DSN")
	flag.Parse()
	if *dsn == "" {
		*dsn = "postgres://postgres:postgres@127.0.0.1:5433/transit?sslmode=disable"
	}
	query := strings.Join(flag.Args(), " ")
	if query == "" {
		log.Fatal("usage: sqlq [-db dsn] <sql>")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, query)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			log.Fatal(err)
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		fmt.Println(strings.Join(parts, "\t"))
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
}
