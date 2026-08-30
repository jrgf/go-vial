package sqlkit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

var (
	testDriverSequence atomic.Int64
	insertMigration    = regexp.MustCompile(`VALUES \('([^']+)', '([0-9a-f]{64})', CURRENT_TIMESTAMP\)`)
)

func TestSQLKit(t *testing.T) {
	t.Run("transactions", func(t *testing.T) {
		database, store := newTestDatabase(t)
		if err := InTx(context.Background(), database, nil, func(transaction *sql.Tx) error {
			_, err := transaction.Exec("COMMIT ME")
			return err
		}); err != nil {
			t.Fatalf("commit transaction: %v", err)
		}
		wantErr := errors.New("callback failed")
		if err := InTx(context.Background(), database, nil, func(transaction *sql.Tx) error {
			if _, err := transaction.Exec("ROLL BACK ME"); err != nil {
				return err
			}
			return wantErr
		}); !errors.Is(err, wantErr) {
			t.Fatalf("rollback error = %v, want %v", err, wantErr)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("transaction panic was not propagated")
				}
			}()
			_ = InTx(context.Background(), database, nil, func(*sql.Tx) error { panic("boom") })
		}()

		store.mu.Lock()
		defer store.mu.Unlock()
		if got, want := strings.Join(store.statements, ","), "COMMIT ME"; got != want {
			t.Fatalf("committed statements = %q, want %q", got, want)
		}
		if store.commits != 1 || store.rollbacks != 2 {
			t.Fatalf("commits/rollbacks = %d/%d, want 1/2", store.commits, store.rollbacks)
		}
	})

	t.Run("migrations", func(t *testing.T) {
		database, store := newTestDatabase(t)
		source := fstest.MapFS{
			"migrations/002_second.sql": {Data: []byte("CREATE TABLE second")},
			"migrations/001_first.sql":  {Data: []byte("CREATE TABLE first")},
			"migrations/README.md":      {Data: []byte("ignored")},
			"migrations/nested/003.sql": {Data: []byte("ignored")},
		}
		migrator, err := NewMigrator(database, source, "migrations")
		if err != nil {
			t.Fatalf("new migrator: %v", err)
		}
		if err := migrator.Migrate(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		if err := migrator.Migrate(context.Background()); err != nil {
			t.Fatalf("second migrate: %v", err)
		}

		store.mu.Lock()
		got := append([]string(nil), store.statements...)
		store.mu.Unlock()
		want := []string{"CREATE TABLE first", "CREATE TABLE second"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("migration order = %q, want %q", got, want)
		}

		source["migrations/001_first.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE changed")}
		if err := migrator.Migrate(context.Background()); err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
			t.Fatalf("drift error = %v", err)
		}
	})

	t.Run("failed migration", func(t *testing.T) {
		database, store := newTestDatabase(t)
		migrator, err := NewMigrator(database, fstest.MapFS{
			"001_ok.sql":   {Data: []byte("CREATE TABLE ok")},
			"002_fail.sql": {Data: []byte("FAIL")},
		}, ".")
		if err != nil {
			t.Fatalf("new migrator: %v", err)
		}
		if err := migrator.Migrate(context.Background()); err == nil {
			t.Fatal("failed migration returned nil")
		}

		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.applied) != 1 || store.applied["001_ok.sql"] == "" {
			t.Fatalf("applied migrations = %v, want only 001_ok.sql", store.applied)
		}
		if store.rollbacks != 1 {
			t.Fatalf("rollbacks = %d, want 1", store.rollbacks)
		}
	})

	t.Run("validation", func(t *testing.T) {
		//nolint:staticcheck // This test verifies nil-context validation.
		if err := InTx(nil, nil, nil, nil); err == nil {
			t.Fatal("nil transaction inputs returned nil")
		}
		database, _ := newTestDatabase(t)
		migrator, err := NewMigrator(database, fstest.MapFS{
			"bad name.sql": {Data: []byte("SELECT 1")},
		}, ".")
		if err != nil {
			t.Fatalf("new migrator: %v", err)
		}
		if err := migrator.Migrate(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid migration filename") {
			t.Fatalf("invalid filename error = %v", err)
		}
	})
}

type testStore struct {
	mu         sync.Mutex
	applied    map[string]string
	statements []string
	commits    int
	rollbacks  int
}

type testDriver struct{ store *testStore }

func (testDriver *testDriver) Open(string) (driver.Conn, error) {
	return &testConnection{store: testDriver.store}, nil
}

type testConnection struct {
	store       *testStore
	transaction *testTransaction
}

func (connection *testConnection) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (connection *testConnection) Close() error                        { return nil }
func (connection *testConnection) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}
func (connection *testConnection) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	transaction := &testTransaction{connection: connection, applied: make(map[string]string)}
	connection.transaction = transaction
	return transaction, nil
}
func (connection *testConnection) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "FAIL" {
		return nil, errors.New("migration failed")
	}
	if strings.HasPrefix(strings.TrimSpace(query), "CREATE TABLE IF NOT EXISTS vial_schema_migrations") {
		return driver.RowsAffected(0), nil
	}
	if connection.transaction == nil {
		return nil, errors.New("statement outside transaction")
	}
	if match := insertMigration.FindStringSubmatch(query); match != nil {
		connection.transaction.applied[match[1]] = match[2]
	} else {
		connection.transaction.statements = append(connection.transaction.statements, strings.TrimSpace(query))
	}
	return driver.RowsAffected(1), nil
}
func (connection *testConnection) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) != selectMigrations {
		return nil, fmt.Errorf("unexpected query %q", query)
	}
	connection.store.mu.Lock()
	versions := make([]string, 0, len(connection.store.applied))
	for version := range connection.store.applied {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	values := make([][]driver.Value, 0, len(versions))
	for _, version := range versions {
		values = append(values, []driver.Value{version, connection.store.applied[version]})
	}
	connection.store.mu.Unlock()
	return &testRows{values: values}, nil
}

type testTransaction struct {
	connection *testConnection
	statements []string
	applied    map[string]string
}

func (transaction *testTransaction) Commit() error {
	store := transaction.connection.store
	store.mu.Lock()
	store.statements = append(store.statements, transaction.statements...)
	for version, checksum := range transaction.applied {
		store.applied[version] = checksum
	}
	store.commits++
	store.mu.Unlock()
	transaction.connection.transaction = nil
	return nil
}
func (transaction *testTransaction) Rollback() error {
	store := transaction.connection.store
	store.mu.Lock()
	store.rollbacks++
	store.mu.Unlock()
	transaction.connection.transaction = nil
	return nil
}

type testRows struct {
	values [][]driver.Value
	index  int
}

func (rows *testRows) Columns() []string { return []string{"version", "checksum"} }
func (rows *testRows) Close() error      { return nil }
func (rows *testRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}

func newTestDatabase(t *testing.T) (*sql.DB, *testStore) {
	t.Helper()
	store := &testStore{applied: make(map[string]string)}
	name := fmt.Sprintf("vial_sqlkit_test_%d", testDriverSequence.Add(1))
	sql.Register(name, &testDriver{store: store})
	database, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	return database, store
}

var (
	_ driver.Driver         = (*testDriver)(nil)
	_ driver.Conn           = (*testConnection)(nil)
	_ driver.ConnBeginTx    = (*testConnection)(nil)
	_ driver.ExecerContext  = (*testConnection)(nil)
	_ driver.QueryerContext = (*testConnection)(nil)
)
