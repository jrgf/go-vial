// Package asyncpostgres implements Vial's durable PostgreSQL executor. It leases
// rows with SKIP LOCKED for crash recovery and multi-replica delivery.
// Applications choose the database/sql PostgreSQL driver.
package asyncpostgres
