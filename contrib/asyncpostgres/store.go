package asyncpostgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jrgf/go-vial"
)

var randomRead = rand.Read

//go:embed schema.sql
var schemaSQL string

const operationColumns = `
id, name, status, progress, created_at, started_at, finished_at,
result, error, metadata, attempt, max_attempts, next_attempt_at,
payload, initial_backoff_ms, max_backoff_ms`

type leasedOperation struct {
	operation      vial.Operation
	payload        json.RawMessage
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

type rowScanner interface {
	Scan(...any) error
}

type postgresJob struct {
	executor *Executor
	owner    string
	delivery leasedOperation
}

func (job *postgresJob) ID() string   { return job.delivery.operation.ID }
func (job *postgresJob) Name() string { return job.delivery.operation.Name }

func (job *postgresJob) Decode(destination any) error {
	if destination == nil {
		return errors.New("decode asyncpostgres job: destination is nil")
	}
	if err := json.Unmarshal(job.delivery.payload, destination); err != nil {
		return fmt.Errorf("decode asyncpostgres job: %w", err)
	}
	return nil
}

func (job *postgresJob) Metadata() map[string]string {
	return cloneMetadata(job.delivery.operation.Metadata)
}

func (job *postgresJob) Progress(contextValue context.Context, progress int) error {
	return job.executor.progress(contextValue, job.delivery, job.owner, progress)
}

// EnsureSchema creates the durable operation table and indexes idempotently.
func (executor *Executor) EnsureSchema(contextValue context.Context) error {
	if contextValue == nil {
		contextValue = context.Background()
	}
	if _, err := executor.database.ExecContext(contextValue, schemaSQL); err != nil {
		return fmt.Errorf("create asyncpostgres schema: %w", err)
	}
	return nil
}

func (executor *Executor) checkSchema(contextValue context.Context) error {
	if _, err := executor.database.ExecContext(contextValue, `SELECT 1 FROM vial_async_operations LIMIT 0`); err != nil {
		return fmt.Errorf("check asyncpostgres schema: %w", err)
	}
	return nil
}

// Submit persists an operation before it becomes visible to workers.
func (executor *Executor) Submit(contextValue context.Context, request vial.SubmitRequest) (*vial.Operation, error) {
	if contextValue == nil {
		contextValue = context.Background()
	}
	if err := contextValue.Err(); err != nil {
		return nil, err
	}
	if !executor.accepting() {
		return nil, vial.ErrAsyncUnavailable
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", vial.ErrInvalidOperation)
	}
	key := strings.TrimSpace(request.IdempotencyKey)
	scope := strings.TrimSpace(request.IdempotencyScope)
	if key != "" && scope == "" {
		return nil, fmt.Errorf("%w: idempotency scope is required", vial.ErrInvalidOperation)
	}
	payload, err := json.Marshal(request.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: encode payload: %v", vial.ErrInvalidOperation, err)
	}
	metadata, err := json.Marshal(cloneMetadata(request.Metadata))
	if err != nil {
		return nil, fmt.Errorf("%w: encode metadata: %v", vial.ErrInvalidOperation, err)
	}
	maxAttempts, initialBackoff, maxBackoff, err := normalizeRetry(request.Retry)
	if err != nil {
		return nil, err
	}
	id := newOperationID()
	createdAt := time.Now().UTC()
	query := `INSERT INTO vial_async_operations (
id, name, status, progress, payload, metadata, created_at, next_attempt_at,
attempt, max_attempts, initial_backoff_ms, max_backoff_ms, idempotency_key, idempotency_scope
) VALUES ($1, $2, 'pending', 0, $3::jsonb, $4::jsonb, $5, $5, 0, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''))`
	if key != "" {
		query += ` ON CONFLICT (idempotency_scope, name, idempotency_key)
WHERE idempotency_key IS NOT NULL DO NOTHING`
	}
	query += ` RETURNING ` + operationColumns
	delivery, err := scanDelivery(executor.database.QueryRowContext(
		contextValue,
		query,
		id,
		name,
		payload,
		metadata,
		createdAt,
		maxAttempts,
		initialBackoff.Milliseconds(),
		maxBackoff.Milliseconds(),
		key,
		scope,
	))
	if err == nil {
		return &delivery.operation, nil
	}
	if !errors.Is(err, sql.ErrNoRows) || key == "" {
		return nil, unavailableError("submit asyncpostgres operation", err)
	}
	delivery, err = scanDelivery(executor.database.QueryRowContext(
		contextValue,
		`SELECT `+operationColumns+` FROM vial_async_operations
WHERE idempotency_scope = $1 AND name = $2 AND idempotency_key = $3`,
		scope,
		name,
		key,
	))
	if err != nil {
		return nil, unavailableError("get idempotent asyncpostgres operation", err)
	}
	return &delivery.operation, nil
}

// Get returns a durable operation snapshot.
func (executor *Executor) Get(contextValue context.Context, id string) (*vial.Operation, error) {
	if contextValue == nil {
		contextValue = context.Background()
	}
	delivery, err := scanDelivery(executor.database.QueryRowContext(
		contextValue,
		`SELECT `+operationColumns+` FROM vial_async_operations WHERE id = $1`,
		id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, vial.ErrOperationNotFound
	}
	if err != nil {
		return nil, unavailableError("get asyncpostgres operation", err)
	}
	return &delivery.operation, nil
}

// Cancel durably cancels a pending, running, or retrying operation.
func (executor *Executor) Cancel(contextValue context.Context, id string) error {
	if contextValue == nil {
		contextValue = context.Background()
	}
	result, err := executor.database.ExecContext(contextValue, `UPDATE vial_async_operations
SET status = 'cancelled', finished_at = now(), next_attempt_at = NULL,
    lease_owner = NULL, lease_expires_at = NULL
WHERE id = $1 AND status IN ('pending', 'running', 'retrying')`, id)
	if err != nil {
		return unavailableError("cancel asyncpostgres operation", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return unavailableError("count cancelled asyncpostgres operation", err)
	}
	if rows == 0 {
		var exists bool
		if err := executor.database.QueryRowContext(contextValue, `SELECT EXISTS (
SELECT 1 FROM vial_async_operations WHERE id = $1)`, id).Scan(&exists); err != nil {
			return unavailableError("check asyncpostgres operation", err)
		}
		if !exists {
			return vial.ErrOperationNotFound
		}
		return nil
	}
	executor.mu.Lock()
	var cancellations []context.CancelFunc
	for _, active := range executor.active {
		if active.id == id {
			cancellations = append(cancellations, active.cancel)
		}
	}
	executor.mu.Unlock()
	for _, cancel := range cancellations {
		cancel()
	}
	return nil
}

// Wait polls durable state until the operation is terminal or contextValue ends.
func (executor *Executor) Wait(contextValue context.Context, id string) (*vial.Operation, error) {
	if contextValue == nil {
		contextValue = context.Background()
	}
	ticker := time.NewTicker(executor.config.pollInterval)
	defer ticker.Stop()
	for {
		operation, err := executor.Get(contextValue, id)
		if err != nil || operation.Status.Terminal() {
			return operation, err
		}
		select {
		case <-contextValue.Done():
			return nil, contextValue.Err()
		case <-ticker.C:
		}
	}
}

// Metrics returns database-wide durable operation metrics.
func (executor *Executor) Metrics(contextValue context.Context) (vial.AsyncMetrics, error) {
	if contextValue == nil {
		contextValue = context.Background()
	}
	var submitted, completed, failed, cancelled, retried, queued, running int64
	var durationSeconds, queueWaitSeconds float64
	var durationCount, queueWaitCount int64
	err := executor.database.QueryRowContext(contextValue, `SELECT
COUNT(*),
COUNT(*) FILTER (WHERE status = 'succeeded'),
COUNT(*) FILTER (WHERE status = 'failed'),
COUNT(*) FILTER (WHERE status = 'cancelled'),
COALESCE(SUM(GREATEST(attempt - 1, 0)), 0),
COUNT(*) FILTER (WHERE status IN ('pending', 'retrying')),
COUNT(*) FILTER (WHERE status = 'running'),
COALESCE(SUM(EXTRACT(EPOCH FROM (finished_at - started_at))) FILTER (WHERE finished_at IS NOT NULL AND started_at IS NOT NULL), 0)::double precision,
COUNT(*) FILTER (WHERE finished_at IS NOT NULL AND started_at IS NOT NULL),
COALESCE(SUM(EXTRACT(EPOCH FROM (started_at - created_at))) FILTER (WHERE started_at IS NOT NULL), 0)::double precision,
COUNT(*) FILTER (WHERE started_at IS NOT NULL)
FROM vial_async_operations`).Scan(
		&submitted,
		&completed,
		&failed,
		&cancelled,
		&retried,
		&queued,
		&running,
		&durationSeconds,
		&durationCount,
		&queueWaitSeconds,
		&queueWaitCount,
	)
	if err != nil {
		return vial.AsyncMetrics{}, fmt.Errorf("query asyncpostgres metrics: %w", err)
	}
	return vial.AsyncMetrics{
		SubmittedTotal:        uint64(submitted),
		CompletedTotal:        uint64(completed),
		FailedTotal:           uint64(failed),
		CancelledTotal:        uint64(cancelled),
		RetriedTotal:          uint64(retried),
		QueueDepth:            queued,
		Running:               running,
		DurationSecondsTotal:  durationSeconds,
		DurationCount:         uint64(durationCount),
		QueueWaitSecondsTotal: queueWaitSeconds,
		QueueWaitCount:        uint64(queueWaitCount),
	}, nil
}

// Ready reports whether the executor accepts work and PostgreSQL responds.
func (executor *Executor) Ready(contextValue context.Context) error {
	if contextValue == nil {
		contextValue = context.Background()
	}
	if !executor.accepting() {
		return vial.ErrAsyncUnavailable
	}
	if err := executor.database.PingContext(contextValue); err != nil {
		return unavailableError("ping asyncpostgres", err)
	}
	return nil
}

func (executor *Executor) lease(contextValue context.Context, owner string) (leasedOperation, error) {
	if _, err := executor.database.ExecContext(contextValue, `UPDATE vial_async_operations
SET status = 'failed', finished_at = now(), lease_owner = NULL, lease_expires_at = NULL,
    error = '{"code":"operation_delivery_exhausted","message":"The operation could not be completed"}'::jsonb
WHERE status = 'running' AND lease_expires_at <= now() AND attempt >= max_attempts`); err != nil {
		return leasedOperation{}, fmt.Errorf("expire asyncpostgres leases: %w", err)
	}
	executor.mu.Lock()
	names := append([]string(nil), executor.handledNames...)
	executor.mu.Unlock()
	if len(names) == 0 {
		return leasedOperation{}, sql.ErrNoRows
	}
	arguments := []any{owner, executor.config.leaseDuration.Milliseconds()}
	placeholders := make([]string, len(names))
	for index, name := range names {
		arguments = append(arguments, name)
		placeholders[index] = fmt.Sprintf("$%d", index+3)
	}
	query := `WITH candidate AS (
SELECT id FROM vial_async_operations
WHERE name IN (` + strings.Join(placeholders, ",") + `) AND (
    (status IN ('pending', 'retrying') AND next_attempt_at <= now()) OR
    (status = 'running' AND lease_expires_at <= now() AND attempt < max_attempts)
)
ORDER BY COALESCE(next_attempt_at, lease_expires_at), created_at
FOR UPDATE SKIP LOCKED
LIMIT 1
)
UPDATE vial_async_operations AS operation
SET status = 'running', started_at = COALESCE(operation.started_at, now()),
    attempt = operation.attempt + 1, progress = 0, next_attempt_at = NULL,
    lease_owner = $1, lease_expires_at = now() + ($2 * interval '1 millisecond')
FROM candidate WHERE operation.id = candidate.id
RETURNING operation.id, operation.name, operation.status, operation.progress,
operation.created_at, operation.started_at, operation.finished_at,
operation.result, operation.error, operation.metadata, operation.attempt,
operation.max_attempts, operation.next_attempt_at, operation.payload,
operation.initial_backoff_ms, operation.max_backoff_ms`
	return scanDelivery(executor.database.QueryRowContext(contextValue, query, arguments...))
}

func (executor *Executor) progress(contextValue context.Context, delivery leasedOperation, owner string, progress int) error {
	if progress < 0 || progress > 100 {
		return fmt.Errorf("%w: progress must be between 0 and 100", vial.ErrInvalidOperation)
	}
	if contextValue == nil {
		contextValue = context.Background()
	}
	result, err := executor.database.ExecContext(contextValue, `UPDATE vial_async_operations
SET progress = $1 WHERE id = $2 AND lease_owner = $3 AND attempt = $4 AND status = 'running'`,
		progress,
		delivery.operation.ID,
		owner,
		delivery.operation.Attempt,
	)
	if err != nil {
		return fmt.Errorf("update asyncpostgres progress: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return vial.ErrOperationFinished
	}
	return nil
}

func (executor *Executor) extendLease(contextValue context.Context, delivery leasedOperation, owner string) (bool, error) {
	result, err := executor.database.ExecContext(contextValue, `UPDATE vial_async_operations
SET lease_expires_at = now() + ($1 * interval '1 millisecond')
WHERE id = $2 AND lease_owner = $3 AND attempt = $4 AND status = 'running'`,
		executor.config.leaseDuration.Milliseconds(),
		delivery.operation.ID,
		owner,
		delivery.operation.Attempt,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (executor *Executor) succeed(contextValue context.Context, delivery leasedOperation, owner string, resultJSON []byte) error {
	result, err := executor.database.ExecContext(contextValue, `UPDATE vial_async_operations
SET status = 'succeeded', progress = 100, result = $1::jsonb, error = NULL,
    finished_at = now(), lease_owner = NULL, lease_expires_at = NULL
WHERE id = $2 AND lease_owner = $3 AND attempt = $4 AND status = 'running'`,
		resultJSON,
		delivery.operation.ID,
		owner,
		delivery.operation.Attempt,
	)
	return ownedUpdate(result, err)
}

func (executor *Executor) retry(contextValue context.Context, delivery leasedOperation, owner string, backoff time.Duration) error {
	result, err := executor.database.ExecContext(contextValue, `UPDATE vial_async_operations
SET status = 'retrying', next_attempt_at = now() + ($1 * interval '1 millisecond'),
    progress = 0, lease_owner = NULL, lease_expires_at = NULL, error = NULL
WHERE id = $2 AND lease_owner = $3 AND attempt = $4 AND status = 'running'`,
		backoff.Milliseconds(),
		delivery.operation.ID,
		owner,
		delivery.operation.Attempt,
	)
	return ownedUpdate(result, err)
}

func (executor *Executor) fail(contextValue context.Context, delivery leasedOperation, owner string, operationErr *vial.OperationError) error {
	operationErr = &vial.OperationError{Code: operationErr.Code, Message: operationErr.Message}
	if operationErr.Code == "" {
		operationErr.Code = "operation_failed"
	}
	if operationErr.Message == "" {
		operationErr.Message = "The operation could not be completed"
	}
	errorJSON, err := json.Marshal(operationErr)
	if err != nil {
		return err
	}
	result, err := executor.database.ExecContext(contextValue, `UPDATE vial_async_operations
SET status = 'failed', error = $1::jsonb, finished_at = now(),
    lease_owner = NULL, lease_expires_at = NULL
WHERE id = $2 AND lease_owner = $3 AND attempt = $4 AND status = 'running'`,
		errorJSON,
		delivery.operation.ID,
		owner,
		delivery.operation.Attempt,
	)
	return ownedUpdate(result, err)
}

func (executor *Executor) releaseLease(contextValue context.Context, delivery leasedOperation, owner string) error {
	result, err := executor.database.ExecContext(contextValue, `UPDATE vial_async_operations
SET status = 'retrying', next_attempt_at = now(), attempt = GREATEST(attempt - 1, 0),
    lease_owner = NULL, lease_expires_at = NULL
WHERE id = $1 AND lease_owner = $2 AND attempt = $3 AND status = 'running'`,
		delivery.operation.ID,
		owner,
		delivery.operation.Attempt,
	)
	return ownedUpdate(result, err)
}

func (executor *Executor) releaseInstanceLeases(contextValue context.Context) error {
	_, err := executor.database.ExecContext(contextValue, `UPDATE vial_async_operations
SET status = 'retrying', next_attempt_at = now(), attempt = GREATEST(attempt - 1, 0),
    lease_owner = NULL, lease_expires_at = NULL
WHERE status = 'running' AND lease_owner LIKE $1`, executor.instanceID+":%")
	if err != nil {
		return fmt.Errorf("release asyncpostgres leases: %w", err)
	}
	return nil
}

func scanDelivery(scanner rowScanner) (leasedOperation, error) {
	var delivery leasedOperation
	var status string
	var startedAt, finishedAt, nextAttemptAt sql.NullTime
	var resultJSON, errorJSON, metadataJSON, payloadJSON []byte
	var initialBackoffMS, maxBackoffMS int64
	err := scanner.Scan(
		&delivery.operation.ID,
		&delivery.operation.Name,
		&status,
		&delivery.operation.Progress,
		&delivery.operation.CreatedAt,
		&startedAt,
		&finishedAt,
		&resultJSON,
		&errorJSON,
		&metadataJSON,
		&delivery.operation.Attempt,
		&delivery.operation.MaxAttempts,
		&nextAttemptAt,
		&payloadJSON,
		&initialBackoffMS,
		&maxBackoffMS,
	)
	if err != nil {
		return leasedOperation{}, err
	}
	delivery.operation.Status = vial.OperationStatus(status)
	delivery.payload = append(json.RawMessage(nil), payloadJSON...)
	if startedAt.Valid {
		delivery.operation.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		delivery.operation.FinishedAt = &finishedAt.Time
	}
	if nextAttemptAt.Valid {
		delivery.operation.NextAttemptAt = &nextAttemptAt.Time
	}
	if len(resultJSON) > 0 {
		if err := json.Unmarshal(resultJSON, &delivery.operation.Result); err != nil {
			return leasedOperation{}, fmt.Errorf("decode asyncpostgres result: %w", err)
		}
	}
	if len(errorJSON) > 0 {
		if err := json.Unmarshal(errorJSON, &delivery.operation.Error); err != nil {
			return leasedOperation{}, fmt.Errorf("decode asyncpostgres error: %w", err)
		}
	}
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &delivery.operation.Metadata); err != nil {
			return leasedOperation{}, fmt.Errorf("decode asyncpostgres metadata: %w", err)
		}
	}
	delivery.initialBackoff = time.Duration(initialBackoffMS) * time.Millisecond
	delivery.maxBackoff = time.Duration(maxBackoffMS) * time.Millisecond
	return delivery, nil
}

func ownedUpdate(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return vial.ErrOperationFinished
	}
	return nil
}

func normalizeRetry(policy vial.RetryPolicy) (int, time.Duration, time.Duration, error) {
	if policy.MaxAttempts < 0 || policy.InitialBackoff < 0 || policy.MaxBackoff < 0 {
		return 0, 0, 0, fmt.Errorf("%w: retry values cannot be negative", vial.ErrInvalidOperation)
	}
	if policy.MaxAttempts > 1000 {
		return 0, 0, 0, fmt.Errorf("%w: retry attempts cannot exceed 1000", vial.ErrInvalidOperation)
	}
	maxAttempts := policy.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 1
	}
	initialBackoff := policy.InitialBackoff
	if initialBackoff == 0 {
		initialBackoff = defaultBackoff
	}
	maxBackoff := policy.MaxBackoff
	if maxBackoff == 0 {
		maxBackoff = defaultMaxBackoff
	}
	if maxBackoff < initialBackoff {
		return 0, 0, 0, fmt.Errorf("%w: maximum retry backoff is less than initial backoff", vial.ErrInvalidOperation)
	}
	return maxAttempts, initialBackoff, maxBackoff, nil
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func newOperationID() string {
	var random [16]byte
	if _, err := randomRead(random[:]); err != nil {
		panic("asyncpostgres: crypto/rand failed: " + err.Error())
	}
	return "op_" + hex.EncodeToString(random[:])
}

func newInstanceID() string {
	var random [12]byte
	if _, err := randomRead(random[:]); err != nil {
		panic("asyncpostgres: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(random[:])
}

func unavailableError(action string, err error) error {
	return errors.Join(vial.ErrAsyncUnavailable, fmt.Errorf("%s: %w", action, err))
}
