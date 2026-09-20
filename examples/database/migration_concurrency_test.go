package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jrgf/go-vial/sqlkit"
)

func TestPostgresConcurrentMigrators(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	schema := fmt.Sprintf("vial_migration_test_%d", time.Now().UnixNano())
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	start, results := make(chan struct{}), make(chan error, 2)
	for range 2 {
		pool, err := sql.Open("pgx", u.String())
		if err != nil {
			t.Fatal(err)
		}
		pool.SetMaxOpenConns(1)
		defer func() { _ = pool.Close() }()
		m, err := sqlkit.NewMigrator(pool, fstest.MapFS{"001.sql": {Data: []byte("SELECT pg_sleep(0.1); CREATE TABLE only_once(id int)")}}, ".")
		if err != nil {
			t.Fatal(err)
		}
		go func() { <-start; results <- m.Migrate(ctx) }()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+schema+".vial_schema_migrations").Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}
