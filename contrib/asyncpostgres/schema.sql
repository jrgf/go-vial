CREATE TABLE IF NOT EXISTS vial_async_operations (
    id text PRIMARY KEY,
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'running', 'retrying', 'succeeded', 'failed', 'cancelled')),
    progress smallint NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    payload jsonb NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    result jsonb,
    error jsonb,
    created_at timestamptz NOT NULL,
    started_at timestamptz,
    finished_at timestamptz,
    next_attempt_at timestamptz,
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts integer NOT NULL DEFAULT 1 CHECK (max_attempts >= 1),
    initial_backoff_ms bigint NOT NULL DEFAULT 1000 CHECK (initial_backoff_ms >= 0),
    max_backoff_ms bigint NOT NULL DEFAULT 60000 CHECK (max_backoff_ms >= initial_backoff_ms),
    idempotency_key text,
    idempotency_scope text,
    lease_owner text,
    lease_expires_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS vial_async_operations_idempotency
    ON vial_async_operations (idempotency_scope, name, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS vial_async_operations_delivery
    ON vial_async_operations (next_attempt_at, created_at)
    WHERE status IN ('pending', 'retrying');

CREATE INDEX IF NOT EXISTS vial_async_operations_expired_leases
    ON vial_async_operations (lease_expires_at, created_at)
    WHERE status = 'running';
