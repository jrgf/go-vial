package sqlkit

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"
)

func TestIndependentMigratorsSerializeInDatabase(t *testing.T) {
	for _, dialect := range []Dialect{PostgreSQL, MySQL} {
		t.Run(string(dialect), func(t *testing.T) { testConcurrentMigrators(t, dialect) })
	}
}

func testConcurrentMigrators(t *testing.T, dialect Dialect) {
	db, store := newTestDatabase(t)
	db.SetMaxOpenConns(2)
	store.queryDelay = 50 * time.Millisecond
	source := fstest.MapFS{"001.sql": {Data: []byte("CREATE TABLE once_only")}}
	first, err := NewMigrator(db, source, ".", dialect)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewMigrator(db, source, ".", dialect)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start, results := make(chan struct{}), make(chan error, 2)
	for _, m := range []*Migrator{first, second} {
		go func() { <-start; results <- m.Migrate(ctx) }()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if len(store.statements) != 1 {
		t.Fatalf("migration executed %d times", len(store.statements))
	}
	if len(store.lock) != 0 {
		t.Fatal("database lock leaked")
	}
}

func TestMigrationLockCancellationAndFailure(t *testing.T) {
	for _, dialect := range []Dialect{PostgreSQL, MySQL} {
		t.Run(string(dialect), func(t *testing.T) {
			db, store := newTestDatabase(t)
			m, err := NewMigrator(db, fstest.MapFS{"001.sql": {Data: []byte("FAIL")}}, ".", dialect)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Migrate(context.Background()); err == nil {
				t.Fatal("failed migration accepted")
			}
			if len(store.lock) != 0 {
				t.Fatal("failed migration leaked lock")
			}
			store.lock <- struct{}{}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err := m.Migrate(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lock wait error=%v", err)
			}
			<-store.lock
		})
	}
}
