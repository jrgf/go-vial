package async

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/jrgf/go-vial"
)

const (
	defaultWorkers     = 8
	defaultQueueSize   = 256
	defaultTaskTimeout = 5 * time.Minute
)

var (
	generateOperationID = newOperationID
	readRandom          = rand.Read
)

// Handler processes one decoded operation payload.
type Handler = vial.AsyncHandler

// Job is the executor-independent operation job contract.
type Job = vial.AsyncJob

type memoryJob struct {
	executor *MemoryExecutor
	id       string
	name     string
	payload  json.RawMessage
	metadata map[string]string
}

// ID returns the operation ID.
func (job memoryJob) ID() string { return job.id }

// Name returns the registered operation name.
func (job memoryJob) Name() string { return job.name }

// Decode unmarshals the copied submission payload into destination.
func (job memoryJob) Decode(destination any) error {
	if destination == nil {
		return fmt.Errorf("decode async job: destination is nil")
	}
	if err := json.Unmarshal(job.payload, destination); err != nil {
		return fmt.Errorf("decode async job: %w", err)
	}
	return nil
}

// Metadata returns a copy of submission metadata.
func (job memoryJob) Metadata() map[string]string {
	return cloneMetadata(job.metadata)
}

// Progress records a percentage from 0 through 100.
func (job memoryJob) Progress(contextValue context.Context, progress int) error {
	return job.executor.reportProgress(contextValue, job.id, progress)
}

type memoryConfig struct {
	workers     int
	queueSize   int
	taskTimeout time.Duration
	logger      *slog.Logger
}

// Option configures MemoryExecutor.
type Option func(*memoryConfig)

// WithWorkers sets the fixed number of operation workers.
func WithWorkers(workers int) Option {
	return func(config *memoryConfig) {
		if workers < 1 {
			panic("async: workers must be greater than zero")
		}
		config.workers = workers
	}
}

// WithQueueSize sets the maximum number of operations waiting for a worker.
func WithQueueSize(size int) Option {
	return func(config *memoryConfig) {
		if size < 1 {
			panic("async: queue size must be greater than zero")
		}
		config.queueSize = size
	}
}

// WithTaskTimeout sets the execution deadline for each operation.
func WithTaskTimeout(timeout time.Duration) Option {
	return func(config *memoryConfig) {
		if timeout <= 0 {
			panic("async: task timeout must be greater than zero")
		}
		config.taskTimeout = timeout
	}
}

// WithLogger sets the executor's structured logger.
func WithLogger(logger *slog.Logger) Option {
	return func(config *memoryConfig) {
		if logger == nil {
			panic("async: logger is nil")
		}
		config.logger = logger
	}
}

type executorState uint8

const (
	executorCreated executorState = iota
	executorRunning
	executorStopping
	executorStopped
)

type operationRecord struct {
	operation vial.Operation
	payload   json.RawMessage
	result    json.RawMessage
	cancel    context.CancelFunc
	done      chan struct{}
	doneOnce  sync.Once
	executing bool
}

// MemoryExecutor runs operations in a fixed worker pool with a bounded queue.
type MemoryExecutor struct {
	config memoryConfig
	queue  chan string

	mu          sync.Mutex
	state       executorState
	handlers    map[string]Handler
	operations  map[string]*operationRecord
	idempotency map[string]string
	workerBase  context.Context
	cancelWork  context.CancelFunc

	workersDone chan struct{}
	done        chan error
	doneOnce    sync.Once
	workerGroup sync.WaitGroup
	metrics     vial.AsyncMetrics
}

// NewMemoryExecutor creates a non-durable executor.
func NewMemoryExecutor(options ...Option) *MemoryExecutor {
	config := memoryConfig{
		workers:     defaultWorkers,
		queueSize:   defaultQueueSize,
		taskTimeout: defaultTaskTimeout,
		logger:      slog.Default(),
	}
	for _, option := range options {
		if option == nil {
			panic("async: option is nil")
		}
		option(&config)
	}
	return &MemoryExecutor{
		config:      config,
		queue:       make(chan string, config.queueSize),
		handlers:    make(map[string]Handler),
		operations:  make(map[string]*operationRecord),
		idempotency: make(map[string]string),
		workersDone: make(chan struct{}),
		done:        make(chan error, 1),
	}
}

// Handle registers an operation handler before the executor starts.
func (executor *MemoryExecutor) Handle(name string, handler Handler) {
	name = strings.TrimSpace(name)
	if name == "" || handler == nil {
		panic("async: operation name and handler are required")
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.state != executorCreated {
		panic("async: handlers cannot be registered after startup")
	}
	if _, exists := executor.handlers[name]; exists {
		panic("async: duplicate operation handler " + name)
	}
	executor.handlers[name] = handler
}

// Start begins workers using an application-owned context independent of any request.
func (executor *MemoryExecutor) Start(contextValue context.Context) error {
	if contextValue == nil {
		contextValue = context.Background()
	}
	executor.mu.Lock()
	if executor.state != executorCreated {
		executor.mu.Unlock()
		return vial.ErrAsyncUnavailable
	}
	executor.workerBase, executor.cancelWork = context.WithCancel(context.WithoutCancel(contextValue))
	executor.state = executorRunning
	executor.workerGroup.Add(executor.config.workers)
	executor.mu.Unlock()

	for range executor.config.workers {
		go executor.worker()
	}
	go func() {
		executor.workerGroup.Wait()
		executor.mu.Lock()
		cancelWork := executor.cancelWork
		executor.mu.Unlock()
		if cancelWork != nil {
			cancelWork()
		}
		close(executor.workersDone)
		executor.signalDone(nil)
	}()
	go func() {
		select {
		case <-contextValue.Done():
			executor.stopAccepting()
		case <-executor.workersDone:
		}
	}()
	return nil
}

// Done reports when executor workers stop.
func (executor *MemoryExecutor) Done() <-chan error { return executor.done }

// Shutdown stops submissions, drains accepted work, then cancels unfinished
// operations if contextValue expires.
func (executor *MemoryExecutor) Shutdown(contextValue context.Context) error {
	if contextValue == nil {
		contextValue = context.Background()
	}
	executor.mu.Lock()
	state := executor.state
	executor.mu.Unlock()
	if state == executorCreated {
		return vial.ErrAsyncUnavailable
	}
	if state == executorStopped {
		return nil
	}
	executor.stopAccepting()
	select {
	case <-executor.workersDone:
		executor.mu.Lock()
		executor.state = executorStopped
		executor.mu.Unlock()
		return nil
	case <-contextValue.Done():
		executor.cancelUnfinished()
		executor.mu.Lock()
		executor.state = executorStopped
		cancelWork := executor.cancelWork
		executor.mu.Unlock()
		if cancelWork != nil {
			cancelWork()
		}
		executor.signalDone(nil)
		return fmt.Errorf("async executor shutdown: %w", contextValue.Err())
	}
}

// Submit copies and queues an operation, or returns vial.ErrAsyncQueueFull.
func (executor *MemoryExecutor) Submit(contextValue context.Context, request vial.SubmitRequest) (*vial.Operation, error) {
	if contextValue != nil {
		if err := contextValue.Err(); err != nil {
			return nil, err
		}
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
	if request.Retry.MaxAttempts < 0 || request.Retry.InitialBackoff < 0 || request.Retry.MaxBackoff < 0 {
		return nil, fmt.Errorf("%w: retry values cannot be negative", vial.ErrInvalidOperation)
	}
	if request.Retry.MaxAttempts > 1 || request.Retry.InitialBackoff != 0 || request.Retry.MaxBackoff != 0 {
		return nil, vial.ErrRetriesUnsupported
	}
	payload, err := json.Marshal(request.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: encode payload: %v", vial.ErrInvalidOperation, err)
	}
	id, err := generateOperationID()
	if err != nil {
		return nil, fmt.Errorf("create operation ID: %w", err)
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.state != executorRunning {
		return nil, vial.ErrAsyncUnavailable
	}
	if _, exists := executor.handlers[name]; !exists {
		return nil, fmt.Errorf("%w: no handler for %q", vial.ErrInvalidOperation, name)
	}
	idempotencyKey := ""
	if key != "" {
		idempotencyKey = scope + "\x00" + name + "\x00" + key
		if existingID, exists := executor.idempotency[idempotencyKey]; exists {
			return snapshot(executor.operations[existingID]), nil
		}
	}
	for executor.operations[id] != nil {
		id, err = generateOperationID()
		if err != nil {
			return nil, fmt.Errorf("create operation ID: %w", err)
		}
	}
	record := &operationRecord{
		operation: vial.Operation{
			ID:          id,
			Name:        name,
			Status:      vial.OperationPending,
			CreatedAt:   time.Now().UTC(),
			Metadata:    cloneMetadata(request.Metadata),
			MaxAttempts: 1,
		},
		payload: append(json.RawMessage(nil), payload...),
		done:    make(chan struct{}),
	}
	executor.operations[id] = record
	if idempotencyKey != "" {
		executor.idempotency[idempotencyKey] = id
	}
	select {
	case executor.queue <- id:
		executor.metrics.SubmittedTotal++
		return snapshot(record), nil
	default:
		delete(executor.operations, id)
		delete(executor.idempotency, idempotencyKey)
		return nil, vial.ErrAsyncQueueFull
	}
}

// Wait blocks until an operation is terminal or contextValue ends.
func (executor *MemoryExecutor) Wait(contextValue context.Context, id string) (*vial.Operation, error) {
	if contextValue == nil {
		contextValue = context.Background()
	}
	executor.mu.Lock()
	record := executor.operations[id]
	if record == nil {
		executor.mu.Unlock()
		return nil, vial.ErrOperationNotFound
	}
	if record.operation.Status.Terminal() {
		operation := snapshot(record)
		executor.mu.Unlock()
		return operation, nil
	}
	done := record.done
	executor.mu.Unlock()
	select {
	case <-done:
		executor.mu.Lock()
		operation := snapshot(executor.operations[id])
		executor.mu.Unlock()
		return operation, nil
	case <-contextValue.Done():
		return nil, contextValue.Err()
	}
}

// Metrics returns an in-process executor metrics snapshot.
func (executor *MemoryExecutor) Metrics(contextValue context.Context) (vial.AsyncMetrics, error) {
	if contextValue != nil {
		if err := contextValue.Err(); err != nil {
			return vial.AsyncMetrics{}, err
		}
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	metrics := executor.metrics
	metrics.QueueDepth = int64(len(executor.queue))
	return metrics, nil
}

// Ready reports whether the executor accepts new operations.
func (executor *MemoryExecutor) Ready(contextValue context.Context) error {
	if contextValue != nil {
		if err := contextValue.Err(); err != nil {
			return err
		}
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.state != executorRunning {
		return vial.ErrAsyncUnavailable
	}
	return nil
}

// Get returns a snapshot of an operation.
func (executor *MemoryExecutor) Get(contextValue context.Context, id string) (*vial.Operation, error) {
	if contextValue != nil {
		if err := contextValue.Err(); err != nil {
			return nil, err
		}
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	record := executor.operations[id]
	if record == nil {
		return nil, vial.ErrOperationNotFound
	}
	return snapshot(record), nil
}

// Cancel idempotently cancels a pending or running operation.
func (executor *MemoryExecutor) Cancel(contextValue context.Context, id string) error {
	if contextValue != nil {
		if err := contextValue.Err(); err != nil {
			return err
		}
	}
	executor.mu.Lock()
	record := executor.operations[id]
	if record == nil {
		executor.mu.Unlock()
		return vial.ErrOperationNotFound
	}
	if record.operation.Status.Terminal() {
		executor.mu.Unlock()
		return nil
	}
	now := time.Now().UTC()
	record.operation.Status = vial.OperationCancelled
	record.operation.FinishedAt = &now
	executor.metrics.CancelledTotal++
	record.doneOnce.Do(func() { close(record.done) })
	operation := record.operation
	cancel := record.cancel
	executor.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	executor.log(operation, slog.LevelInfo, "async operation cancelled", 0, nil)
	return nil
}

func (executor *MemoryExecutor) worker() {
	defer executor.workerGroup.Done()
	for id := range executor.queue {
		executor.run(id)
	}
}

func (executor *MemoryExecutor) run(id string) {
	executor.mu.Lock()
	record := executor.operations[id]
	if record == nil || record.operation.Status != vial.OperationPending {
		executor.mu.Unlock()
		return
	}
	handler := executor.handlers[record.operation.Name]
	jobContext, cancel := context.WithTimeout(executor.workerBase, executor.config.taskTimeout)
	record.cancel = cancel
	now := time.Now().UTC()
	record.operation.Status = vial.OperationRunning
	record.operation.StartedAt = &now
	record.operation.Attempt = 1
	record.executing = true
	executor.metrics.Running++
	executor.metrics.QueueWaitSecondsTotal += now.Sub(record.operation.CreatedAt).Seconds()
	executor.metrics.QueueWaitCount++
	job := memoryJob{
		executor: executor,
		id:       id,
		name:     record.operation.Name,
		payload:  append(json.RawMessage(nil), record.payload...),
		metadata: cloneMetadata(record.operation.Metadata),
	}
	startedOperation := record.operation
	executor.mu.Unlock()
	executor.log(startedOperation, slog.LevelInfo, "async operation started", 0, nil)

	result, err := callHandler(handler, jobContext, job)
	contextErr := jobContext.Err()
	cancel()
	if err == nil && contextErr != nil {
		err = contextErr
	}
	var encoded json.RawMessage
	if err == nil {
		encoded, err = json.Marshal(result)
		if err != nil {
			err = fmt.Errorf("encode operation result: %w", err)
		}
	}
	executor.finish(id, encoded, err, contextErr)
}

func callHandler(handler Handler, contextValue context.Context, job vial.AsyncJob) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return handler(contextValue, job)
}

func (executor *MemoryExecutor) finish(id string, result json.RawMessage, operationErr, contextErr error) {
	executor.mu.Lock()
	record := executor.operations[id]
	if record == nil {
		executor.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	duration := time.Duration(0)
	if record.executing {
		record.executing = false
		executor.metrics.Running--
		if record.operation.StartedAt != nil {
			duration = now.Sub(*record.operation.StartedAt)
			executor.metrics.DurationSecondsTotal += duration.Seconds()
			executor.metrics.DurationCount++
		}
	}
	if record.operation.Status.Terminal() {
		executor.mu.Unlock()
		return
	}
	record.operation.FinishedAt = &now
	if operationErr == nil {
		record.operation.Status = vial.OperationSucceeded
		record.operation.Progress = 100
		record.result = append(json.RawMessage(nil), result...)
		executor.metrics.CompletedTotal++
		record.doneOnce.Do(func() { close(record.done) })
		operation := record.operation
		executor.mu.Unlock()
		executor.log(operation, slog.LevelInfo, "async operation succeeded", duration, nil)
		return
	}
	record.operation.Status = vial.OperationFailed
	var publicErr *vial.OperationError
	switch {
	case errors.Is(contextErr, context.DeadlineExceeded):
		publicErr = &vial.OperationError{Code: "operation_timeout", Message: "The operation timed out"}
	case errors.As(operationErr, &publicErr) && publicErr != nil:
		publicErr = &vial.OperationError{Code: publicErr.Code, Message: publicErr.Message}
		if publicErr.Code == "" {
			publicErr.Code = "operation_failed"
		}
		if publicErr.Message == "" {
			publicErr.Message = "The operation could not be completed"
		}
	default:
		publicErr = &vial.OperationError{Code: "operation_failed", Message: "The operation could not be completed"}
	}
	record.operation.Error = publicErr
	executor.metrics.FailedTotal++
	record.doneOnce.Do(func() { close(record.done) })
	operation := record.operation
	executor.mu.Unlock()
	executor.log(operation, slog.LevelError, "async operation failed", duration, operationErr)
}

func (executor *MemoryExecutor) reportProgress(contextValue context.Context, id string, progress int) error {
	if progress < 0 || progress > 100 {
		return fmt.Errorf("%w: progress must be between 0 and 100", vial.ErrInvalidOperation)
	}
	if contextValue != nil {
		if err := contextValue.Err(); err != nil {
			return err
		}
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	record := executor.operations[id]
	if record == nil {
		return vial.ErrOperationNotFound
	}
	if record.operation.Status != vial.OperationRunning {
		return vial.ErrOperationFinished
	}
	record.operation.Progress = progress
	return nil
}

func (executor *MemoryExecutor) stopAccepting() {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.state == executorRunning {
		executor.state = executorStopping
		close(executor.queue)
	}
}

func (executor *MemoryExecutor) cancelUnfinished() {
	now := time.Now().UTC()
	var cancellations []context.CancelFunc
	executor.mu.Lock()
	for _, record := range executor.operations {
		if record.operation.Status.Terminal() {
			continue
		}
		record.operation.Status = vial.OperationCancelled
		record.operation.FinishedAt = &now
		executor.metrics.CancelledTotal++
		record.doneOnce.Do(func() { close(record.done) })
		if record.cancel != nil {
			cancellations = append(cancellations, record.cancel)
		}
	}
	executor.mu.Unlock()
	for _, cancel := range cancellations {
		cancel()
	}
}

func (executor *MemoryExecutor) signalDone(err error) {
	executor.doneOnce.Do(func() { executor.done <- err })
}

func (executor *MemoryExecutor) log(operation vial.Operation, level slog.Level, message string, duration time.Duration, operationErr error) {
	attributes := []any{
		"operation_id", operation.ID,
		"operation_name", operation.Name,
		"attempt", operation.Attempt,
		"status", operation.Status,
		"duration", duration,
	}
	for _, key := range []string{"user_id", "tenant_id", "trace_id"} {
		if value := operation.Metadata[key]; value != "" {
			attributes = append(attributes, key, value)
		}
	}
	if operationErr != nil {
		attributes = append(attributes, "error", operationErr)
	}
	executor.config.logger.Log(context.Background(), level, message, attributes...)
}

func snapshot(record *operationRecord) *vial.Operation {
	if record == nil {
		return nil
	}
	operation := record.operation
	operation.Metadata = cloneMetadata(operation.Metadata)
	operation.StartedAt = cloneTime(operation.StartedAt)
	operation.FinishedAt = cloneTime(operation.FinishedAt)
	operation.NextAttemptAt = cloneTime(operation.NextAttemptAt)
	if operation.Error != nil {
		operation.Error = &vial.OperationError{Code: operation.Error.Code, Message: operation.Error.Message}
	}
	if len(record.result) > 0 {
		_ = json.Unmarshal(record.result, &operation.Result)
	}
	return &operation
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	return maps.Clone(metadata)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func newOperationID() (string, error) {
	var random [16]byte
	if _, err := readRandom(random[:]); err != nil {
		return "", err
	}
	return "op_" + hex.EncodeToString(random[:]), nil
}
