package asyncpostgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
)

var testDriverSequence atomic.Uint64

type testOperation struct {
	id, name, status, leaseOwner, idempotencyKey, idempotencyScope string
	progress, attempt, maxAttempts                                 int64
	createdAt                                                      time.Time
	startedAt, finishedAt, nextAttemptAt                           *time.Time
	result, operationError, metadata, payload                      []byte
	initialBackoffMS, maxBackoffMS                                 int64
}

type testStore struct {
	mu                 sync.Mutex
	schema             bool
	operations         map[string]*testOperation
	idempotent         map[string]string
	pingErr            error
	execErr            error
	queryErr           error
	idempotentQueryErr error
	cancelRowsErr      error
	progressRowsErr    error
	extendRowsErr      error
}

type testDriver struct{ store *testStore }
type testConn struct{ store *testStore }
type pgTestResult struct {
	rows int64
	err  error
}
type testRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func newTestDatabase(t *testing.T) (*sql.DB, *testStore) {
	t.Helper()
	store := &testStore{operations: make(map[string]*testOperation), idempotent: make(map[string]string)}
	name := fmt.Sprintf("vial_asyncpostgres_test_%d", testDriverSequence.Add(1))
	sql.Register(name, &testDriver{store: store})
	database, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	database.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = database.Close() })
	return database, store
}

func (testDriver *testDriver) Open(string) (driver.Conn, error) {
	return &testConn{store: testDriver.store}, nil
}

func (connection *testConn) Prepare(string) (driver.Stmt, error)      { return nil, driver.ErrSkip }
func (connection *testConn) Close() error                             { return nil }
func (connection *testConn) Begin() (driver.Tx, error)                { return nil, driver.ErrSkip }
func (connection *testConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (connection *testConn) Ping(context.Context) error {
	connection.store.mu.Lock()
	defer connection.store.mu.Unlock()
	return connection.store.pingErr
}

func (connection *testConn) ExecContext(contextValue context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	if err := contextValue.Err(); err != nil {
		return nil, err
	}
	store := connection.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.execErr != nil {
		return nil, store.execErr
	}
	normalized := normalizeSQL(query)
	values := namedValues(arguments)
	switch {
	case strings.Contains(normalized, "CREATE TABLE IF NOT EXISTS vial_async_operations"):
		store.schema = true
		return pgTestResult{}, nil
	case normalized == "SELECT 1 FROM vial_async_operations LIMIT 0":
		if !store.schema {
			return nil, errors.New("schema missing")
		}
		return pgTestResult{}, nil
	case strings.Contains(normalized, "error = '{\"code\":\"operation_delivery_exhausted\""):
		return pgTestResult{}, nil
	case strings.Contains(normalized, "SET status = 'cancelled'"):
		operation := store.operations[valueString(values[0])]
		if operation == nil || terminalStatus(operation.status) {
			return pgTestResult{err: store.cancelRowsErr}, nil
		}
		now := time.Now().UTC()
		operation.status, operation.finishedAt, operation.leaseOwner = "cancelled", &now, ""
		return pgTestResult{rows: 1, err: store.cancelRowsErr}, nil
	case strings.Contains(normalized, "SET progress = $1"):
		operation := store.owned(values[1], values[2], values[3])
		if operation == nil {
			return pgTestResult{err: store.progressRowsErr}, nil
		}
		operation.progress = valueInt64(values[0])
		return pgTestResult{rows: 1, err: store.progressRowsErr}, nil
	case strings.Contains(normalized, "SET lease_expires_at = now()"):
		if store.owned(values[1], values[2], values[3]) == nil {
			return pgTestResult{err: store.extendRowsErr}, nil
		}
		return pgTestResult{rows: 1, err: store.extendRowsErr}, nil
	case strings.Contains(normalized, "SET status = 'succeeded'"):
		operation := store.owned(values[1], values[2], values[3])
		if operation == nil {
			return pgTestResult{}, nil
		}
		now := time.Now().UTC()
		operation.status, operation.progress, operation.result = "succeeded", 100, valueBytes(values[0])
		operation.finishedAt, operation.leaseOwner = &now, ""
		return pgTestResult{rows: 1}, nil
	case strings.Contains(normalized, "SET status = 'failed', error = $1::jsonb"):
		operation := store.owned(values[1], values[2], values[3])
		if operation == nil {
			return pgTestResult{}, nil
		}
		now := time.Now().UTC()
		operation.status, operation.operationError, operation.finishedAt, operation.leaseOwner = "failed", valueBytes(values[0]), &now, ""
		return pgTestResult{rows: 1}, nil
	case strings.Contains(normalized, "SET status = 'retrying', next_attempt_at = now() +"):
		operation := store.owned(values[1], values[2], values[3])
		if operation == nil {
			return pgTestResult{}, nil
		}
		now := time.Now().UTC()
		operation.status, operation.progress, operation.nextAttemptAt, operation.leaseOwner = "retrying", 0, &now, ""
		return pgTestResult{rows: 1}, nil
	case strings.Contains(normalized, "WHERE id = $1 AND lease_owner = $2"):
		operation := store.owned(values[0], values[1], values[2])
		if operation == nil {
			return pgTestResult{}, nil
		}
		now := time.Now().UTC()
		operation.status, operation.nextAttemptAt, operation.leaseOwner = "retrying", &now, ""
		if operation.attempt > 0 {
			operation.attempt--
		}
		return pgTestResult{rows: 1}, nil
	case strings.Contains(normalized, "lease_owner LIKE $1"):
		prefix := strings.TrimSuffix(valueString(values[0]), "%")
		var rows int64
		for _, operation := range store.operations {
			if operation.status == "running" && strings.HasPrefix(operation.leaseOwner, prefix) {
				operation.status, operation.leaseOwner = "retrying", ""
				rows++
			}
		}
		return pgTestResult{rows: rows}, nil
	default:
		return nil, fmt.Errorf("unexpected test ExecContext query: %s", normalized)
	}
}

func (connection *testConn) QueryContext(contextValue context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	if err := contextValue.Err(); err != nil {
		return nil, err
	}
	store := connection.store
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.queryErr != nil {
		return nil, store.queryErr
	}
	normalized := normalizeSQL(query)
	values := namedValues(arguments)
	switch {
	case strings.HasPrefix(normalized, "INSERT INTO vial_async_operations"):
		id, name := valueString(values[0]), valueString(values[1])
		key, scope := valueString(values[8]), valueString(values[9])
		idempotency := scope + "\x00" + name + "\x00" + key
		if key != "" {
			if store.idempotent[idempotency] != "" {
				return operationRows(), nil
			}
		}
		createdAt, _ := values[4].(time.Time)
		operation := &testOperation{
			id: id, name: name, status: "pending", createdAt: createdAt,
			payload: valueBytes(values[2]), metadata: valueBytes(values[3]),
			maxAttempts: valueInt64(values[5]), initialBackoffMS: valueInt64(values[6]), maxBackoffMS: valueInt64(values[7]),
			idempotencyKey: key, idempotencyScope: scope,
		}
		operation.nextAttemptAt = &createdAt
		store.operations[id] = operation
		if key != "" {
			store.idempotent[idempotency] = id
		}
		return operationRows(operation), nil
	case strings.Contains(normalized, "WHERE idempotency_scope = $1"):
		if store.idempotentQueryErr != nil {
			return nil, store.idempotentQueryErr
		}
		id := store.idempotent[valueString(values[0])+"\x00"+valueString(values[1])+"\x00"+valueString(values[2])]
		return operationRows(store.operations[id]), nil
	case strings.HasPrefix(normalized, "SELECT id, name, status") && strings.Contains(normalized, "WHERE id = $1"):
		return operationRows(store.operations[valueString(values[0])]), nil
	case strings.HasPrefix(normalized, "SELECT EXISTS"):
		return &testRows{columns: []string{"exists"}, values: [][]driver.Value{{store.operations[valueString(values[0])] != nil}}}, nil
	case strings.HasPrefix(normalized, "SELECT COUNT(*)"):
		return metricRows(store.operations), nil
	case strings.HasPrefix(normalized, "WITH candidate AS"):
		for _, operation := range store.operations {
			if operation.status != "pending" && operation.status != "retrying" {
				continue
			}
			now := time.Now().UTC()
			operation.status, operation.progress, operation.leaseOwner = "running", 0, valueString(values[0])
			operation.attempt++
			if operation.startedAt == nil {
				operation.startedAt = &now
			}
			operation.nextAttemptAt = nil
			return operationRows(operation), nil
		}
		return operationRows(), nil
	default:
		return nil, fmt.Errorf("unexpected test QueryContext query: %s", normalized)
	}
}

func (store *testStore) owned(idValue, ownerValue, attemptValue any) *testOperation {
	operation := store.operations[valueString(idValue)]
	if operation == nil || operation.status != "running" || operation.leaseOwner != valueString(ownerValue) || operation.attempt != valueInt64(attemptValue) {
		return nil
	}
	return operation
}

func (result pgTestResult) LastInsertId() (int64, error) { return 0, errors.New("unsupported") }
func (result pgTestResult) RowsAffected() (int64, error) { return result.rows, result.err }
func (rows *testRows) Columns() []string                 { return rows.columns }
func (rows *testRows) Close() error                      { return nil }
func (rows *testRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}

func (rows *testRows) Scan(destination ...any) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	values := rows.values[rows.index]
	rows.index++
	for index, target := range destination {
		value := values[index]
		switch pointer := target.(type) {
		case *string:
			*pointer = valueString(value)
		case *int:
			*pointer = int(valueInt64(value))
		case *int64:
			*pointer = valueInt64(value)
		case *time.Time:
			*pointer, _ = value.(time.Time)
		case *sql.NullTime:
			if timestamp, ok := value.(time.Time); ok {
				*pointer = sql.NullTime{Time: timestamp, Valid: true}
			} else {
				*pointer = sql.NullTime{}
			}
		case *[]byte:
			*pointer = valueBytes(value)
		default:
			return fmt.Errorf("unsupported scan target %T", target)
		}
	}
	return nil
}

func operationRows(operations ...*testOperation) *testRows {
	rows := &testRows{columns: strings.Split("id,name,status,progress,created_at,started_at,finished_at,result,error,metadata,attempt,max_attempts,next_attempt_at,payload,initial_backoff_ms,max_backoff_ms", ",")}
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		rows.values = append(rows.values, []driver.Value{
			operation.id, operation.name, operation.status, operation.progress, operation.createdAt,
			timeValue(operation.startedAt), timeValue(operation.finishedAt), nullableBytes(operation.result), nullableBytes(operation.operationError), nullableBytes(operation.metadata),
			operation.attempt, operation.maxAttempts, timeValue(operation.nextAttemptAt), nullableBytes(operation.payload), operation.initialBackoffMS, operation.maxBackoffMS,
		})
	}
	return rows
}

func metricRows(operations map[string]*testOperation) *testRows {
	var submitted, completed, failed, cancelled, retried, queued, running, durationCount, waitCount int64
	var duration, wait float64
	for _, operation := range operations {
		submitted++
		switch operation.status {
		case "succeeded":
			completed++
		case "failed":
			failed++
		case "cancelled":
			cancelled++
		case "pending", "retrying":
			queued++
		case "running":
			running++
		}
		if operation.attempt > 1 {
			retried += operation.attempt - 1
		}
		if operation.startedAt != nil {
			wait += operation.startedAt.Sub(operation.createdAt).Seconds()
			waitCount++
		}
		if operation.startedAt != nil && operation.finishedAt != nil {
			duration += operation.finishedAt.Sub(*operation.startedAt).Seconds()
			durationCount++
		}
	}
	return &testRows{columns: make([]string, 11), values: [][]driver.Value{{submitted, completed, failed, cancelled, retried, queued, running, duration, durationCount, wait, waitCount}}}
}

func namedValues(arguments []driver.NamedValue) []any {
	values := make([]any, len(arguments))
	for index, argument := range arguments {
		values[index] = argument.Value
	}
	return values
}

func normalizeSQL(query string) string { return strings.Join(strings.Fields(query), " ") }
func valueString(value any) string     { result, _ := value.(string); return result }
func valueBytes(value any) []byte      { result, _ := value.([]byte); return append([]byte(nil), result...) }
func valueInt64(value any) int64 {
	switch result := value.(type) {
	case int:
		return int64(result)
	case int64:
		return result
	default:
		return 0
	}
}
func nullableBytes(value []byte) driver.Value {
	if len(value) == 0 {
		return nil
	}
	return append([]byte(nil), value...)
}
func timeValue(value *time.Time) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}
func terminalStatus(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled"
}

func requirePostgresPanic(t *testing.T, function func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	function()
}

func waitForOperation(t *testing.T, executor *Executor, id string, status vial.OperationStatus) *vial.Operation {
	t.Helper()
	contextValue, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		operation, err := executor.Get(contextValue, id)
		if err != nil {
			t.Fatalf("get operation: %v", err)
		}
		if operation.Status == status {
			return operation
		}
		select {
		case <-contextValue.Done():
			t.Fatalf("operation %s did not reach %s", id, status)
		case <-time.After(time.Millisecond):
		}
	}
}

func newIntegrationExecutor(database *sql.DB, workers int) *Executor {
	return New(database,
		WithWorkers(workers),
		WithPollInterval(2*time.Millisecond),
		WithLeaseDuration(30*time.Millisecond),
		WithTaskTimeout(40*time.Millisecond),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
}

func TestDurableLifecycleRestartWorkersAndIdempotency(t *testing.T) {
	database, _ := newTestDatabase(t)
	executor := newIntegrationExecutor(database, 2)
	parallelStarted := make(chan struct{}, 2)
	releaseParallel := make(chan struct{})
	lateStarted := make(chan struct{})
	releaseLate := make(chan struct{})
	var retryAttempts atomic.Int32
	executor.Handle("success", func(contextValue context.Context, job vial.AsyncJob) (any, error) {
		var payload map[string]string
		if err := job.Decode(&payload); err != nil {
			return nil, err
		}
		if job.ID() == "" || job.Name() != "success" || job.Metadata()["tenant_id"] != "tenant-1" {
			return nil, errors.New("invalid job contract")
		}
		if err := job.Progress(contextValue, 50); err != nil {
			return nil, err
		}
		return payload, nil
	})
	executor.Handle("parallel", func(contextValue context.Context, _ vial.AsyncJob) (any, error) {
		parallelStarted <- struct{}{}
		select {
		case <-releaseParallel:
			return "done", nil
		case <-contextValue.Done():
			return nil, contextValue.Err()
		}
	})
	executor.Handle("retry", func(context.Context, vial.AsyncJob) (any, error) {
		if retryAttempts.Add(1) == 1 {
			return nil, errors.New("temporary")
		}
		return "retried", nil
	})
	executor.Handle("public", func(context.Context, vial.AsyncJob) (any, error) {
		return nil, &vial.OperationError{Code: "permanent", Message: "Permanent failure"}
	})
	executor.Handle("panic", func(context.Context, vial.AsyncJob) (any, error) { panic("worker panic") })
	executor.Handle("encode", func(context.Context, vial.AsyncJob) (any, error) { return make(chan int), nil })
	executor.Handle("timeout-nil", func(contextValue context.Context, _ vial.AsyncJob) (any, error) {
		<-contextValue.Done()
		return nil, nil
	})
	executor.Handle("late-success", func(context.Context, vial.AsyncJob) (any, error) {
		close(lateStarted)
		<-releaseLate
		return "late", nil
	})
	//nolint:staticcheck // Exercise Start's supported nil-context fallback.
	if err := executor.Start(nil); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	//nolint:staticcheck // Exercise Ready's supported nil-context fallback.
	if err := executor.Ready(nil); err != nil {
		t.Fatalf("ready executor: %v", err)
	}

	//nolint:staticcheck // Exercise Submit's supported nil-context fallback.
	operation, err := executor.Submit(nil, vial.SubmitRequest{
		Name: "success", Payload: map[string]string{"report": "sales"},
		IdempotencyKey: "report-1", IdempotencyScope: "tenant-1", Metadata: map[string]string{"tenant_id": "tenant-1"},
	})
	if err != nil {
		t.Fatalf("submit operation: %v", err)
	}
	duplicate, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "success", Payload: "different", IdempotencyKey: "report-1", IdempotencyScope: "tenant-1"})
	if err != nil || duplicate.ID != operation.ID {
		t.Fatalf("duplicate = %#v, %v", duplicate, err)
	}
	completed := waitForOperation(t, executor, operation.ID, vial.OperationSucceeded)
	if completed.Progress != 100 {
		t.Fatalf("completed operation = %#v", completed)
	}
	//nolint:staticcheck // Exercise Wait's supported nil-context fallback.
	if waited, err := executor.Wait(nil, operation.ID); err != nil || waited.Status != vial.OperationSucceeded {
		t.Fatalf("wait = %#v, %v", waited, err)
	}

	parallel := make([]*vial.Operation, 0, 2)
	for range 2 {
		operation, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "parallel"})
		if err != nil {
			t.Fatalf("submit parallel operation: %v", err)
		}
		parallel = append(parallel, operation)
	}
	for range 2 {
		select {
		case <-parallelStarted:
		case <-time.After(time.Second):
			t.Fatal("durable workers did not run concurrently")
		}
	}
	close(releaseParallel)
	for _, operation := range parallel {
		waitForOperation(t, executor, operation.ID, vial.OperationSucceeded)
	}

	retried, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "retry", Retry: vial.RetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}})
	if err != nil {
		t.Fatalf("submit retry: %v", err)
	}
	waitForOperation(t, executor, retried.ID, vial.OperationSucceeded)
	for _, name := range []string{"public", "panic", "encode", "timeout-nil"} {
		failed, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: name})
		if err != nil {
			t.Fatalf("submit %s: %v", name, err)
		}
		waitForOperation(t, executor, failed.ID, vial.OperationFailed)
	}
	late, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "late-success"})
	if err != nil {
		t.Fatalf("submit late success: %v", err)
	}
	<-lateStarted
	if err := executor.Cancel(context.Background(), late.ID); err != nil {
		t.Fatalf("cancel late success: %v", err)
	}
	close(releaseLate)
	waitForOperation(t, executor, late.ID, vial.OperationCancelled)
	//nolint:staticcheck // Exercise Metrics' supported nil-context fallback.
	metrics, err := executor.Metrics(nil)
	if err != nil || metrics.SubmittedTotal != 9 || metrics.CompletedTotal != 4 || metrics.FailedTotal != 4 || metrics.CancelledTotal != 1 || metrics.RetriedTotal == 0 {
		t.Fatalf("metrics = %#v, %v", metrics, err)
	}

	//nolint:staticcheck // Exercise Shutdown's supported nil-context fallback.
	if err := executor.Shutdown(nil); err != nil {
		t.Fatalf("shutdown executor: %v", err)
	}
	if err := <-executor.Done(); err != nil {
		t.Fatalf("executor done: %v", err)
	}
	if err := executor.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}

	restarted := New(database, WithWorkers(1), WithPollInterval(2*time.Millisecond), WithLeaseDuration(30*time.Millisecond), WithAutoMigrate(false))
	restarted.Handle("success", func(context.Context, vial.AsyncJob) (any, error) { return "restarted", nil })
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatalf("restart executor: %v", err)
	}
	//nolint:staticcheck // Exercise Get's supported nil-context fallback.
	if persisted, err := restarted.Get(nil, operation.ID); err != nil || persisted.Status != vial.OperationSucceeded {
		t.Fatalf("persisted operation = %#v, %v", persisted, err)
	}
	if err := restarted.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown restarted executor: %v", err)
	}
}

func TestDurableCancellationFailuresAndValidation(t *testing.T) {
	database, store := newTestDatabase(t)
	requirePostgresPanic(t, func() { New(nil) })
	requirePostgresPanic(t, func() { New(database, nil) })
	requirePostgresPanic(t, func() { New(database, WithWorkers(0)) })
	requirePostgresPanic(t, func() { New(database, WithPollInterval(0)) })
	requirePostgresPanic(t, func() { New(database, WithLeaseDuration(0)) })
	requirePostgresPanic(t, func() { New(database, WithTaskTimeout(0)) })
	requirePostgresPanic(t, func() { New(database, WithLogger(nil)) })
	requirePostgresPanic(t, func() { New(database, WithPollInterval(time.Second), WithLeaseDuration(time.Second)) })

	executor := newIntegrationExecutor(database, 1)
	requirePostgresPanic(t, func() { executor.Handle("", nil) })
	started := make(chan struct{})
	shutdownStarted := make(chan struct{})
	executor.Handle("block", func(contextValue context.Context, _ vial.AsyncJob) (any, error) {
		close(started)
		<-contextValue.Done()
		return nil, contextValue.Err()
	})
	executor.Handle("shutdown", func(contextValue context.Context, _ vial.AsyncJob) (any, error) {
		close(shutdownStarted)
		<-contextValue.Done()
		return nil, contextValue.Err()
	})
	requirePostgresPanic(t, func() {
		executor.Handle("block", func(context.Context, vial.AsyncJob) (any, error) { return nil, nil })
	})
	if err := executor.Shutdown(context.Background()); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("shutdown before start = %v", err)
	}
	if _, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "block"}); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("submit before start = %v", err)
	}
	if err := executor.Start(context.Background()); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	if err := executor.Start(context.Background()); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("second start = %v", err)
	}
	requirePostgresPanic(t, func() { executor.Handle("late", func(context.Context, vial.AsyncJob) (any, error) { return nil, nil }) })

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executor.Submit(cancelled, vial.SubmitRequest{Name: "block"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled submit = %v", err)
	}
	for _, request := range []vial.SubmitRequest{
		{},
		{Name: "block", IdempotencyKey: "key"},
		{Name: "block", Payload: make(chan int)},
		{Name: "block", Retry: vial.RetryPolicy{MaxAttempts: -1}},
		{Name: "block", Retry: vial.RetryPolicy{MaxAttempts: 1001}},
		{Name: "block", Retry: vial.RetryPolicy{InitialBackoff: 2 * time.Second, MaxBackoff: time.Second}},
	} {
		if _, err := executor.Submit(context.Background(), request); err == nil {
			t.Fatalf("invalid submit %#v succeeded", request)
		}
	}
	if _, err := executor.Get(context.Background(), "missing"); !errors.Is(err, vial.ErrOperationNotFound) {
		t.Fatalf("missing get = %v", err)
	}
	if err := executor.Cancel(context.Background(), "missing"); !errors.Is(err, vial.ErrOperationNotFound) {
		t.Fatalf("missing cancel = %v", err)
	}

	operation, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "block"})
	if err != nil {
		t.Fatalf("submit block: %v", err)
	}
	<-started
	//nolint:staticcheck // Exercise Cancel's supported nil-context fallback.
	if err := executor.Cancel(nil, operation.ID); err != nil {
		t.Fatalf("cancel operation: %v", err)
	}
	waitForOperation(t, executor, operation.ID, vial.OperationCancelled)
	if err := executor.Cancel(context.Background(), operation.ID); err != nil {
		t.Fatalf("cancel terminal operation: %v", err)
	}
	waitContext, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if _, err := executor.Wait(waitContext, "missing"); !errors.Is(err, vial.ErrOperationNotFound) && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait = %v", err)
	}

	store.mu.Lock()
	store.pingErr = errors.New("ping failed")
	store.mu.Unlock()
	if err := executor.Ready(context.Background()); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("failed readiness = %v", err)
	}
	store.mu.Lock()
	store.pingErr = nil
	store.mu.Unlock()
	if _, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "shutdown"}); err != nil {
		t.Fatalf("submit shutdown operation: %v", err)
	}
	<-shutdownStarted

	shutdownContext, cancelShutdown := context.WithCancel(context.Background())
	cancelShutdown()
	if err := executor.Shutdown(shutdownContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("forced shutdown = %v", err)
	}
	if err := executor.Ready(context.Background()); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("ready after shutdown = %v", err)
	}
}

func TestDurableExecutorAndStoreEdgeBranches(t *testing.T) {
	database, store := newTestDatabase(t)
	failure := errors.New("database failed")

	store.execErr = failure
	if err := New(database).Start(context.Background()); err == nil {
		t.Fatal("start with migration failure succeeded")
	}
	store.execErr = nil
	if err := New(database).EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	store.execErr = failure
	if err := New(database, WithAutoMigrate(false)).Start(context.Background()); err == nil {
		t.Fatal("start with schema check failure succeeded")
	}
	store.execErr = nil

	lifecycleContext, cancelLifecycle := context.WithCancel(context.Background())
	lifecycle := New(database, WithWorkers(1), WithPollInterval(time.Millisecond), WithLeaseDuration(30*time.Millisecond))
	if err := lifecycle.Start(lifecycleContext); err != nil {
		t.Fatalf("start lifecycle executor: %v", err)
	}
	cancelLifecycle()
	deadline := time.Now().Add(time.Second)
	for lifecycle.accepting() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if lifecycle.accepting() {
		t.Fatal("lifecycle cancellation did not stop submissions")
	}
	if err := lifecycle.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown lifecycle executor: %v", err)
	}

	worker := New(database, WithWorkers(1), WithPollInterval(time.Millisecond), WithLeaseDuration(30*time.Millisecond))
	worker.workerBase = context.Background()
	worker.state = executorRunning
	worker.workerGroup.Add(1)
	store.execErr = failure
	go worker.worker(0)
	time.Sleep(5 * time.Millisecond)
	worker.stopAccepting()
	worker.workerGroup.Wait()
	store.execErr = nil

	direct := New(database, WithPollInterval(time.Millisecond), WithLeaseDuration(30*time.Millisecond), WithTaskTimeout(20*time.Millisecond))
	direct.Handle("success", func(context.Context, vial.AsyncJob) (any, error) { return "ok", nil })
	direct.Handle("retry", func(context.Context, vial.AsyncJob) (any, error) { return nil, errors.New("temporary") })
	direct.workerBase = context.Background()
	direct.state = executorRunning
	missingSuccess := leasedOperation{operation: vial.Operation{ID: "missing-success", Name: "success", Status: vial.OperationRunning, Attempt: 1, MaxAttempts: 1}}
	direct.run("owner", missingSuccess)
	missingRetry := leasedOperation{
		operation:      vial.Operation{ID: "missing-retry", Name: "retry", Status: vial.OperationRunning, Attempt: 1, MaxAttempts: 2},
		initialBackoff: time.Millisecond,
		maxBackoff:     time.Millisecond,
	}
	direct.run("owner", missingRetry)
	store.execErr = failure
	direct.run("owner", missingSuccess)
	direct.run("owner", missingRetry)
	store.execErr = nil

	now := time.Now().UTC()
	store.operations["unhandled"] = &testOperation{id: "unhandled", name: "unhandled", status: "running", leaseOwner: "owner", attempt: 1, maxAttempts: 1, createdAt: now}
	direct.run("owner", leasedOperation{operation: vial.Operation{ID: "unhandled", Name: "unhandled", Status: vial.OperationRunning, Attempt: 1, MaxAttempts: 1}})

	renewContext, renewCancel := context.WithCancel(context.Background())
	renewStopped := make(chan struct{})
	renewDone := make(chan struct{})
	go func() {
		direct.renewLease(renewContext, renewCancel, renewStopped, missingSuccess, "owner")
		close(renewDone)
	}()
	select {
	case <-renewContext.Done():
	case <-time.After(time.Second):
		t.Fatal("lost lease did not cancel its job")
	}
	<-renewDone

	empty := New(database)
	if _, err := empty.lease(context.Background(), "owner"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("lease without handlers = %v", err)
	}

	submitter := New(database, WithPollInterval(100*time.Millisecond), WithLeaseDuration(time.Second))
	submitter.state = executorRunning
	store.queryErr = failure
	if _, err := submitter.Submit(context.Background(), vial.SubmitRequest{Name: "job"}); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("failed submit = %v", err)
	}
	store.queryErr = nil
	first, err := submitter.Submit(context.Background(), vial.SubmitRequest{Name: "job", IdempotencyKey: "key", IdempotencyScope: "scope"})
	if err != nil {
		t.Fatalf("submit idempotent operation: %v", err)
	}
	store.idempotentQueryErr = failure
	if _, err := submitter.Submit(context.Background(), vial.SubmitRequest{Name: "job", IdempotencyKey: "key", IdempotencyScope: "scope"}); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("failed idempotent lookup = %v", err)
	}
	store.idempotentQueryErr = nil

	store.execErr = failure
	if err := submitter.Cancel(context.Background(), first.ID); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("failed cancel = %v", err)
	}
	store.execErr = nil
	store.cancelRowsErr = failure
	if err := submitter.Cancel(context.Background(), first.ID); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("failed cancel row count = %v", err)
	}
	store.cancelRowsErr = nil
	store.queryErr = failure
	if err := submitter.Cancel(context.Background(), "missing"); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("failed existence check = %v", err)
	}
	store.queryErr = nil

	waiting, err := submitter.Submit(context.Background(), vial.SubmitRequest{Name: "job"})
	if err != nil {
		t.Fatalf("submit waiting operation: %v", err)
	}
	waitContext, cancelWait := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancelWait()
	}()
	if _, err := submitter.Wait(waitContext, waiting.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait = %v", err)
	}

	owned := leasedOperation{operation: vial.Operation{ID: "owned", Status: vial.OperationRunning, Attempt: 1}}
	store.operations["owned"] = &testOperation{id: "owned", status: "running", leaseOwner: "owner", attempt: 1, createdAt: now}
	//nolint:staticcheck // Exercise progress reporting's supported nil-context fallback.
	if err := submitter.progress(nil, owned, "owner", 10); err != nil {
		t.Fatalf("progress with background context: %v", err)
	}
	store.execErr = failure
	if err := submitter.progress(context.Background(), owned, "owner", 20); err == nil {
		t.Fatal("progress database failure succeeded")
	}
	store.execErr = nil
	store.progressRowsErr = failure
	if err := submitter.progress(context.Background(), owned, "owner", 20); !errors.Is(err, failure) {
		t.Fatalf("progress row count error = %v", err)
	}
	store.progressRowsErr = nil
	store.execErr = failure
	if _, err := submitter.extendLease(context.Background(), owned, "owner"); !errors.Is(err, failure) {
		t.Fatalf("extend lease error = %v", err)
	}
	store.execErr = nil
	store.extendRowsErr = failure
	if _, err := submitter.extendLease(context.Background(), owned, "owner"); !errors.Is(err, failure) {
		t.Fatalf("extend lease row error = %v", err)
	}
	store.extendRowsErr = nil
	store.execErr = failure
	if err := submitter.releaseInstanceLeases(context.Background()); err == nil {
		t.Fatal("release leases database failure succeeded")
	}
	store.execErr = nil
}

func TestStoreAndScannerErrorPaths(t *testing.T) {
	database, store := newTestDatabase(t)
	executor := New(database)
	//nolint:staticcheck // Exercise EnsureSchema's supported nil-context fallback.
	if err := executor.EnsureSchema(nil); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	store.mu.Lock()
	store.queryErr = errors.New("query failed")
	store.mu.Unlock()
	if _, err := executor.Get(context.Background(), "id"); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("failed get = %v", err)
	}
	if _, err := executor.Metrics(context.Background()); err == nil {
		t.Fatal("failed metrics succeeded")
	}
	store.mu.Lock()
	store.queryErr = nil
	store.execErr = errors.New("exec failed")
	store.mu.Unlock()
	if err := executor.EnsureSchema(context.Background()); err == nil {
		t.Fatal("failed schema creation succeeded")
	}
	if err := executor.checkSchema(context.Background()); err == nil {
		t.Fatal("failed schema check succeeded")
	}
	store.mu.Lock()
	store.execErr = nil
	store.mu.Unlock()

	badJSON := func(column int) *testRows {
		operation := &testOperation{id: "id", name: "name", status: "succeeded", createdAt: time.Now(), metadata: []byte(`{}`), payload: []byte(`{}`), maxAttempts: 1}
		rows := operationRows(operation)
		rows.values[0][column] = []byte(`{`)
		return rows
	}
	for _, column := range []int{7, 8, 9} {
		if _, err := scanDelivery(badJSON(column)); err == nil {
			t.Fatalf("bad JSON column %d decoded", column)
		}
	}
	if _, err := scanDelivery(operationRows()); !errors.Is(err, io.EOF) {
		t.Fatalf("empty scan = %v", err)
	}

	delivery := leasedOperation{operation: vial.Operation{ID: "missing", Attempt: 1}}
	//nolint:staticcheck // Exercise progress validation before context use.
	if err := executor.progress(nil, delivery, "owner", -1); !errors.Is(err, vial.ErrInvalidOperation) {
		t.Fatalf("invalid progress = %v", err)
	}
	if err := executor.progress(context.Background(), delivery, "owner", 50); !errors.Is(err, vial.ErrOperationFinished) {
		t.Fatalf("unowned progress = %v", err)
	}
	if ok, err := executor.extendLease(context.Background(), delivery, "owner"); err != nil || ok {
		t.Fatalf("unowned extension = %v, %v", ok, err)
	}
	if err := executor.succeed(context.Background(), delivery, "owner", []byte(`{}`)); !errors.Is(err, vial.ErrOperationFinished) {
		t.Fatalf("unowned success = %v", err)
	}
	if err := executor.retry(context.Background(), delivery, "owner", time.Second); !errors.Is(err, vial.ErrOperationFinished) {
		t.Fatalf("unowned retry = %v", err)
	}
	if err := executor.fail(context.Background(), delivery, "owner", &vial.OperationError{}); !errors.Is(err, vial.ErrOperationFinished) {
		t.Fatalf("unowned failure = %v", err)
	}
	if err := executor.releaseLease(context.Background(), delivery, "owner"); !errors.Is(err, vial.ErrOperationFinished) {
		t.Fatalf("unowned release = %v", err)
	}
	if err := executor.releaseInstanceLeases(context.Background()); err != nil {
		t.Fatalf("release instance leases: %v", err)
	}

	job := postgresJob{delivery: leasedOperation{
		operation: vial.Operation{ID: "id", Name: "name", Metadata: map[string]string{"key": "value"}},
		payload:   []byte(`{"text":"value"}`),
	}}
	if job.ID() != "id" || job.Name() != "name" || job.Metadata()["key"] != "value" {
		t.Fatalf("job contract failed: %#v", job)
	}
	if err := job.Decode(nil); err == nil {
		t.Fatal("nil decode succeeded")
	}
	var number int
	if err := job.Decode(&number); err == nil {
		t.Fatal("invalid decode succeeded")
	}

	resultError := errors.New("rows failed")
	if err := ownedUpdate(pgTestResult{err: resultError}, nil); !errors.Is(err, resultError) {
		t.Fatalf("rows error = %v", err)
	}
	if err := ownedUpdate(nil, resultError); !errors.Is(err, resultError) {
		t.Fatalf("update error = %v", err)
	}
	if !errors.Is(unavailableError("test", resultError), vial.ErrAsyncUnavailable) {
		t.Fatal("unavailable error was not classified")
	}
	if newOperationID() == "" || newInstanceID() == "" {
		t.Fatal("generated empty identifier")
	}
	if cloneMetadata(nil) != nil {
		t.Fatal("nil metadata clone was not nil")
	}
	metadata := map[string]string{"key": "value"}
	if clone := cloneMetadata(metadata); clone["key"] != "value" {
		t.Fatalf("metadata clone = %#v", clone)
	}
	encoded, _ := json.Marshal(&vial.OperationError{Code: "failed", Message: "safe"})
	if len(encoded) == 0 {
		t.Fatal("operation error did not encode")
	}
}
