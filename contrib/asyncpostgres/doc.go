// Package asyncpostgres provides a durable PostgreSQL-backed Vial async executor.
// It uses row leases and SKIP LOCKED for safe recovery and multi-replica delivery.
// Applications supply a database/sql PostgreSQL driver.
package asyncpostgres
