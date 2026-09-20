package sqlkit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

const (
	createMigrationsTable = `CREATE TABLE IF NOT EXISTS vial_schema_migrations (
	version varchar(255) PRIMARY KEY,
	checksum char(64) NOT NULL,
	applied_at timestamp NOT NULL
)`
	selectMigrations = `SELECT version, checksum FROM vial_schema_migrations`
)

// InTx runs fn in a transaction. It commits when fn returns nil and rolls back
// on an error or panic.
func InTx(ctx context.Context, database *sql.DB, options *sql.TxOptions, fn func(*sql.Tx) error) (err error) {
	if ctx == nil {
		return errors.New("sqlkit: nil context")
	}
	if database == nil {
		return errors.New("sqlkit: nil database")
	}
	if fn == nil {
		return errors.New("sqlkit: nil transaction function")
	}
	return inTx(ctx, database, options, fn)
}

type transactionStarter interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func inTx(ctx context.Context, database transactionStarter, options *sql.TxOptions, fn func(*sql.Tx) error) (err error) {
	transaction, err := database.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("sqlkit: begin transaction: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = transaction.Rollback()
			panic(recovered)
		}
		if err != nil {
			if rollbackErr := transaction.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("sqlkit: rollback transaction: %w", rollbackErr))
			}
			return
		}
		if commitErr := transaction.Commit(); commitErr != nil {
			err = fmt.Errorf("sqlkit: commit transaction: %w", commitErr)
		}
	}()

	return fn(transaction)
}

// Migrator applies embedded .sql files in filename order.
type Migrator struct {
	database *sql.DB
	source   fs.FS
	dialect  Dialect
}

// NewMigrator creates a forward-only migrator rooted at directory in source.
// Pass "." or an empty directory when source already points at the migrations.
// PostgreSQL is the default dialect. Pass MySQL for its session locking syntax.
// Other engines must use a migration tool supporting their locking semantics.
func NewMigrator(database *sql.DB, source fs.FS, directory string, dialects ...Dialect) (*Migrator, error) {
	if database == nil {
		return nil, errors.New("sqlkit: nil database")
	}
	if source == nil {
		return nil, errors.New("sqlkit: nil migration source")
	}
	dialect := PostgreSQL
	if len(dialects) > 1 {
		return nil, errors.New("sqlkit: expected at most one dialect")
	}
	if len(dialects) == 1 {
		dialect = dialects[0]
	}
	if dialect != PostgreSQL && dialect != MySQL {
		return nil, fmt.Errorf("sqlkit: unsupported migration dialect %q", dialect)
	}
	if directory == "" {
		directory = "."
	}
	if !fs.ValidPath(directory) {
		return nil, fmt.Errorf("sqlkit: invalid migration directory %q", directory)
	}

	root, err := fs.Sub(source, directory)
	if err != nil {
		return nil, fmt.Errorf("sqlkit: open migration directory %q: %w", directory, err)
	}
	return &Migrator{database: database, source: root, dialect: dialect}, nil
}

// Migrate applies unapplied migrations. Changing an applied file returns an
// error. A database session lock serializes migration runs across processes.
// Each file runs in its own transaction; MySQL DDL may commit implicitly.
func (migrator *Migrator) Migrate(ctx context.Context) (err error) {
	if ctx == nil {
		return errors.New("sqlkit: nil context")
	}
	if migrator == nil || migrator.database == nil || migrator.source == nil {
		return errors.New("sqlkit: nil migrator")
	}

	connection, err := migrator.database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("sqlkit: acquire migration connection: %w", err)
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil && !errors.Is(closeErr, sql.ErrConnDone) {
			err = errors.Join(err, fmt.Errorf("sqlkit: close migration connection: %w", closeErr))
		}
	}()
	unlock, err := migrator.lock(ctx, connection)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return migrator.migrate(ctx, connection)
}

func (migrator *Migrator) migrate(ctx context.Context, connection *sql.Conn) error {
	if _, err := connection.ExecContext(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("sqlkit: create migrations table: %w", err)
	}
	applied, err := migrator.applied(ctx, connection)
	if err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrator.source, ".")
	if err != nil {
		return fmt.Errorf("sqlkit: read migrations: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		if !validMigrationName(name) {
			return fmt.Errorf("sqlkit: invalid migration filename %q", name)
		}
		contents, err := fs.ReadFile(migrator.source, name)
		if err != nil {
			return fmt.Errorf("sqlkit: read migration %q: %w", name, err)
		}
		if len(strings.TrimSpace(string(contents))) == 0 {
			return fmt.Errorf("sqlkit: migration %q is empty", name)
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(contents))
		if previous, ok := applied[name]; ok {
			if previous != checksum {
				return fmt.Errorf("sqlkit: migration %q changed after it was applied", name)
			}
			continue
		}

		if err := inTx(ctx, connection, nil, func(transaction *sql.Tx) error {
			// ponytail: send each file as one driver call; use one statement per
			// file when a driver does not accept SQL scripts.
			if _, err := transaction.ExecContext(ctx, string(contents)); err != nil {
				return fmt.Errorf("apply migration %q: %w", name, err)
			}
			query := fmt.Sprintf(
				"INSERT INTO vial_schema_migrations (version, checksum, applied_at) VALUES ('%s', '%s', CURRENT_TIMESTAMP)",
				name,
				checksum,
			)
			if _, err := transaction.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("record migration %q: %w", name, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (migrator *Migrator) applied(ctx context.Context, connection *sql.Conn) (map[string]string, error) {
	rows, err := connection.QueryContext(ctx, selectMigrations)
	if err != nil {
		return nil, fmt.Errorf("sqlkit: list applied migrations: %w", err)
	}
	applied := make(map[string]string)
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("sqlkit: scan applied migration: %w", err)
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("sqlkit: read applied migrations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("sqlkit: close applied migrations: %w", err)
	}
	return applied, nil
}

func validMigrationName(name string) bool {
	if name == ".sql" || !strings.HasSuffix(name, ".sql") {
		return false
	}
	for _, character := range name {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
