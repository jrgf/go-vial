package sqlkit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

// Dialect selects database-level migration locking. All replicas must use it.
type Dialect string

const (
	PostgreSQL Dialect = "postgres"
	MySQL      Dialect = "mysql"
)

// Use the same connection for the lock, reads, DDL and migration records.
// Losing that session also prevents the runner from continuing without its lock.
func (migrator *Migrator) lock(ctx context.Context, connection *sql.Conn) (func() error, error) {
	if migrator.dialect == MySQL {
		for {
			var acquired sql.NullInt64
			err := connection.QueryRowContext(ctx, "SELECT GET_LOCK('vial_schema_migrations', 1)").Scan(&acquired)
			if err != nil || !acquired.Valid {
				discardConnection(connection)
				if err == nil {
					err = fmt.Errorf("GET_LOCK returned NULL")
				}
				return nil, fmt.Errorf("sqlkit: acquire migration lock: %w", err)
			}
			if acquired.Int64 == 1 {
				break
			}
		}
	} else if _, err := connection.ExecContext(ctx, "SELECT pg_advisory_lock(1986617708, 1)"); err != nil {
		discardConnection(connection)
		return nil, fmt.Errorf("sqlkit: acquire PostgreSQL migration lock: %w", err)
	}
	return func() error {
		// Release after cancellation too; never put a possibly locked session back
		// into the pool when release fails.
		releaseContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		query := "SELECT pg_advisory_unlock(1986617708, 1)"
		if migrator.dialect == MySQL {
			query = "SELECT RELEASE_LOCK('vial_schema_migrations')"
		}
		if _, err := connection.ExecContext(releaseContext, query); err != nil {
			discardConnection(connection)
			return fmt.Errorf("sqlkit: release migration lock: %w", err)
		}
		return nil
	}, nil
}

func discardConnection(connection *sql.Conn) {
	_ = connection.Raw(func(any) error { return driver.ErrBadConn })
}
