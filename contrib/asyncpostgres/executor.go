package asyncpostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jrgf/go-vial"
)

const (
	defaultWorkers       = 8
	defaultPollInterval  = 500 * time.Millisecond
	defaultLeaseDuration = 30 * time.Second
	defaultTaskTimeout   = 5 * time.Minute
	defaultBackoff       = time.Second
	defaultMaxBackoff    = time.Minute
)

type config struct {
	workers       int
	pollInterval  time.Duration
	leaseDuration time.Duration
	taskTimeout   time.Duration
	autoMigrate   bool
	logger        *slog.Logger
}

// Option configures Executor.
type Option func(*config)

// WithWorkers sets the fixed number of PostgreSQL workers.
func WithWorkers(workers int) Option {
	return func(config *config) {
		if workers < 1 {
			panic("asyncpostgres: workers must be greater than zero")
		}
		config.workers = workers
	}
}

// WithPollInterval sets how often idle workers check for deliverable work.
func WithPollInterval(interval time.Duration) Option {
	return func(config *config) {
		if interval <= 0 {
			panic("asyncpostgres: poll interval must be greater than zero")
		}
		config.pollInterval = interval
	}
}

// WithLeaseDuration sets the visibility timeout renewed while a handler runs.
func WithLeaseDuration(duration time.Duration) Option {
	return func(config *config) {
		if duration <= 0 {
			panic("asyncpostgres: lease duration must be greater than zero")
		}
		config.leaseDuration = duration
	}
}

// WithTaskTimeout sets each delivery's execution deadline.
func WithTaskTimeout(timeout time.Duration) Option {
	return func(config *config) {
		if timeout <= 0 {
			panic("asyncpostgres: task timeout must be greater than zero")
		}
		config.taskTimeout = timeout
	}
}

// WithAutoMigrate controls idempotent schema creation during startup.
func WithAutoMigrate(enabled bool) Option {
	return func(config *config) { config.autoMigrate = enabled }
}

// WithLogger sets the executor's structured logger.
func WithLogger(logger *slog.Logger) Option {
	return func(config *config) {
		if logger == nil {
			panic("asyncpostgres: logger is nil")
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

type activeOperation struct {
	id     string
	cancel context.CancelFunc
}

// Executor durably runs Vial operations through PostgreSQL.
type Executor struct {
	database   *sql.DB
	config     config
	instanceID string

	mu           sync.Mutex
	state        executorState
	handlers     map[string]vial.AsyncHandler
	handledNames []string
	workerBase   context.Context
	cancelWork   context.CancelFunc
	active       map[string]activeOperation

	workerGroup sync.WaitGroup
	workersDone chan struct{}
	done        chan error
	doneOnce    sync.Once
}

// New creates an executor. Auto-migration is enabled by default.
func New(database *sql.DB, options ...Option) *Executor {
	if database == nil {
		panic("asyncpostgres: database is nil")
	}
	configuration := config{
		workers:       defaultWorkers,
		pollInterval:  defaultPollInterval,
		leaseDuration: defaultLeaseDuration,
		taskTimeout:   defaultTaskTimeout,
		autoMigrate:   true,
		logger:        slog.Default(),
	}
	for _, option := range options {
		if option == nil {
			panic("asyncpostgres: option is nil")
		}
		option(&configuration)
	}
	if configuration.pollInterval > configuration.leaseDuration/3 {
		panic("asyncpostgres: lease duration must be at least three poll intervals")
	}
	return &Executor{
		database:    database,
		config:      configuration,
		instanceID:  newInstanceID(),
		handlers:    make(map[string]vial.AsyncHandler),
		active:      make(map[string]activeOperation),
		workersDone: make(chan struct{}),
		done:        make(chan error, 1),
	}
}

// Handle registers a durable operation handler before startup.
func (executor *Executor) Handle(name string, handler vial.AsyncHandler) {
	name = strings.TrimSpace(name)
	if name == "" || handler == nil {
		panic("asyncpostgres: operation name and handler are required")
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.state != executorCreated {
		panic("asyncpostgres: handlers cannot be registered after startup")
	}
	if _, exists := executor.handlers[name]; exists {
		panic("asyncpostgres: duplicate operation handler " + name)
	}
	executor.handlers[name] = handler
}

// Start verifies the schema and begins lease workers.
func (executor *Executor) Start(contextValue context.Context) error {
	if contextValue == nil {
		contextValue = context.Background()
	}
	if executor.config.autoMigrate {
		if err := executor.EnsureSchema(contextValue); err != nil {
			return err
		}
	} else if err := executor.checkSchema(contextValue); err != nil {
		return err
	}
	executor.mu.Lock()
	if executor.state != executorCreated {
		executor.mu.Unlock()
		return vial.ErrAsyncUnavailable
	}
	executor.workerBase, executor.cancelWork = context.WithCancel(context.WithoutCancel(contextValue))
	executor.handledNames = make([]string, 0, len(executor.handlers))
	for name := range executor.handlers {
		executor.handledNames = append(executor.handledNames, name)
	}
	sort.Strings(executor.handledNames)
	executor.state = executorRunning
	executor.workerGroup.Add(executor.config.workers)
	executor.mu.Unlock()

	for worker := range executor.config.workers {
		go executor.worker(worker)
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

// Done reports when all lease workers have stopped.
func (executor *Executor) Done() <-chan error { return executor.done }

// Shutdown drains running handlers or releases their leases at the deadline.
func (executor *Executor) Shutdown(contextValue context.Context) error {
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
		executor.setStopped()
		return nil
	case <-contextValue.Done():
		executor.cancelActive()
		executor.mu.Lock()
		cancelWork := executor.cancelWork
		executor.mu.Unlock()
		if cancelWork != nil {
			cancelWork()
		}
		releaseContext, cancelRelease := context.WithTimeout(context.Background(), time.Second)
		releaseErr := executor.releaseInstanceLeases(releaseContext)
		cancelRelease()
		executor.setStopped()
		executor.signalDone(nil)
		return errors.Join(fmt.Errorf("asyncpostgres shutdown: %w", contextValue.Err()), releaseErr)
	}
}

func (executor *Executor) worker(worker int) {
	defer executor.workerGroup.Done()
	owner := fmt.Sprintf("%s:%d", executor.instanceID, worker)
	for executor.accepting() {
		delivery, err := executor.lease(executor.workerBase, owner)
		if errors.Is(err, sql.ErrNoRows) {
			executor.waitForPoll()
			continue
		}
		if err != nil {
			executor.config.logger.Error("async lease failed", "error", err)
			executor.waitForPoll()
			continue
		}
		executor.run(owner, delivery)
	}
}

func (executor *Executor) run(owner string, delivery leasedOperation) {
	executor.mu.Lock()
	handler := executor.handlers[delivery.operation.Name]
	executor.mu.Unlock()
	if handler == nil {
		storeContext, cancelStore := executor.storeContext()
		_ = executor.releaseLease(storeContext, delivery, owner)
		cancelStore()
		return
	}
	jobContext, cancel := context.WithTimeout(executor.workerBase, executor.config.taskTimeout)
	activeToken := executor.registerActive(delivery, owner, cancel)
	stopRenewal := make(chan struct{})
	go executor.renewLease(jobContext, cancel, stopRenewal, delivery, owner)
	started := time.Now()
	executor.log(delivery, vial.OperationRunning, slog.LevelInfo, "async operation started", 0, nil)
	result, operationErr := callHandler(handler, jobContext, &postgresJob{
		executor: executor,
		owner:    owner,
		delivery: delivery,
	})
	contextErr := jobContext.Err()
	close(stopRenewal)
	cancel()
	executor.unregisterActive(activeToken)

	if operationErr == nil && contextErr != nil {
		operationErr = contextErr
	}
	if operationErr == nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			operationErr = fmt.Errorf("encode operation result: %w", err)
		} else if err := executor.storeSuccess(delivery, owner, encoded); err == nil {
			executor.log(delivery, vial.OperationSucceeded, slog.LevelInfo, "async operation succeeded", time.Since(started), nil)
			return
		} else if errors.Is(err, vial.ErrOperationFinished) {
			return
		} else {
			operationErr = err
		}
	}
	if errors.Is(contextErr, context.Canceled) && !executor.accepting() {
		storeContext, cancelStore := executor.storeContext()
		_ = executor.releaseLease(storeContext, delivery, owner)
		cancelStore()
		return
	}
	var publicErr *vial.OperationError
	permanent := errors.As(operationErr, &publicErr) && publicErr != nil
	if !permanent && delivery.operation.Attempt < delivery.operation.MaxAttempts {
		backoff := retryBackoff(delivery.operation.Attempt, delivery.initialBackoff, delivery.maxBackoff)
		storeContext, cancelStore := executor.storeContext()
		err := executor.retry(storeContext, delivery, owner, backoff)
		cancelStore()
		if err == nil {
			executor.log(delivery, vial.OperationRetrying, slog.LevelWarn, "async operation retrying", time.Since(started), operationErr)
			return
		} else if errors.Is(err, vial.ErrOperationFinished) {
			return
		}
		executor.config.logger.Error("schedule async retry", "operation_id", delivery.operation.ID, "error", err)
		return
	}
	if publicErr == nil {
		if errors.Is(contextErr, context.DeadlineExceeded) {
			publicErr = &vial.OperationError{Code: "operation_timeout", Message: "The operation timed out"}
		} else {
			publicErr = &vial.OperationError{Code: "operation_failed", Message: "The operation could not be completed"}
		}
	}
	storeContext, cancelStore := executor.storeContext()
	err := executor.fail(storeContext, delivery, owner, publicErr)
	cancelStore()
	if errors.Is(err, vial.ErrOperationFinished) {
		return
	}
	if err != nil && !errors.Is(err, vial.ErrOperationFinished) {
		executor.config.logger.Error("record async failure", "operation_id", delivery.operation.ID, "error", err)
	}
	executor.log(delivery, vial.OperationFailed, slog.LevelError, "async operation failed", time.Since(started), operationErr)
}

func callHandler(handler vial.AsyncHandler, contextValue context.Context, job vial.AsyncJob) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return handler(contextValue, job)
}

func (executor *Executor) renewLease(contextValue context.Context, cancel context.CancelFunc, stop <-chan struct{}, delivery leasedOperation, owner string) {
	ticker := time.NewTicker(executor.config.leaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-contextValue.Done():
			return
		case <-ticker.C:
			ok, err := executor.extendLease(contextValue, delivery, owner)
			if err != nil || !ok {
				cancel()
				return
			}
		}
	}
}

func (executor *Executor) waitForPoll() {
	timer := time.NewTimer(executor.config.pollInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-executor.workerBase.Done():
	}
}

func (executor *Executor) accepting() bool {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.state == executorRunning
}

func (executor *Executor) stopAccepting() {
	executor.mu.Lock()
	if executor.state == executorRunning {
		executor.state = executorStopping
	}
	executor.mu.Unlock()
}

func (executor *Executor) setStopped() {
	executor.mu.Lock()
	executor.state = executorStopped
	executor.mu.Unlock()
}

func (executor *Executor) registerActive(delivery leasedOperation, owner string, cancel context.CancelFunc) string {
	token := fmt.Sprintf("%s\x00%s\x00%d", delivery.operation.ID, owner, delivery.operation.Attempt)
	executor.mu.Lock()
	executor.active[token] = activeOperation{id: delivery.operation.ID, cancel: cancel}
	executor.mu.Unlock()
	return token
}

func (executor *Executor) unregisterActive(token string) {
	executor.mu.Lock()
	delete(executor.active, token)
	executor.mu.Unlock()
}

func (executor *Executor) cancelActive() {
	executor.mu.Lock()
	cancellations := make([]context.CancelFunc, 0, len(executor.active))
	for _, active := range executor.active {
		cancellations = append(cancellations, active.cancel)
	}
	executor.mu.Unlock()
	for _, cancel := range cancellations {
		cancel()
	}
}

func (executor *Executor) signalDone(err error) {
	executor.doneOnce.Do(func() { executor.done <- err })
}

func (executor *Executor) storeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), executor.config.leaseDuration)
}

func (executor *Executor) storeSuccess(delivery leasedOperation, owner string, result []byte) error {
	contextValue, cancel := executor.storeContext()
	defer cancel()
	return executor.succeed(contextValue, delivery, owner, result)
}

func (executor *Executor) log(delivery leasedOperation, status vial.OperationStatus, level slog.Level, message string, duration time.Duration, operationErr error) {
	attributes := []any{
		"operation_id", delivery.operation.ID,
		"operation_name", delivery.operation.Name,
		"attempt", delivery.operation.Attempt,
		"status", status,
		"duration", duration,
	}
	for _, key := range []string{"user_id", "tenant_id", "trace_id"} {
		if value := delivery.operation.Metadata[key]; value != "" {
			attributes = append(attributes, key, value)
		}
	}
	if operationErr != nil {
		attributes = append(attributes, "error", operationErr)
	}
	executor.config.logger.Log(context.Background(), level, message, attributes...)
}

func retryBackoff(attempt int, initial, maximum time.Duration) time.Duration {
	backoff := initial
	for current := 1; current < attempt && backoff < maximum; current++ {
		if backoff > maximum/2 {
			return maximum
		}
		backoff *= 2
	}
	return min(backoff, maximum)
}
