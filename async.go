package vial

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// OperationStatus is the current state of an asynchronous operation.
type OperationStatus string

const (
	OperationPending   OperationStatus = "pending"
	OperationRunning   OperationStatus = "running"
	OperationRetrying  OperationStatus = "retrying"
	OperationSucceeded OperationStatus = "succeeded"
	OperationFailed    OperationStatus = "failed"
	OperationCancelled OperationStatus = "cancelled"
)

// Terminal reports whether no further state transition is possible.
func (status OperationStatus) Terminal() bool {
	return status == OperationSucceeded || status == OperationFailed || status == OperationCancelled
}

// OperationError is the safe, client-visible reason an operation failed.
type OperationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (operationError *OperationError) Error() string {
	if operationError == nil {
		return ""
	}
	if operationError.Message != "" {
		return operationError.Message
	}
	return operationError.Code
}

// Operation describes submitted background work.
type Operation struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Status        OperationStatus   `json:"status"`
	Progress      int               `json:"progress"`
	CreatedAt     time.Time         `json:"created_at"`
	StartedAt     *time.Time        `json:"started_at"`
	FinishedAt    *time.Time        `json:"finished_at"`
	Result        any               `json:"result"`
	Error         *OperationError   `json:"error"`
	Metadata      map[string]string `json:"-"`
	Attempt       int               `json:"attempt"`
	MaxAttempts   int               `json:"max_attempts"`
	NextAttemptAt *time.Time        `json:"next_attempt_at"`
}

// RetryPolicy configures durable redelivery after transient worker failures.
// Its zero value permits one attempt and therefore disables retries.
type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// SubmitRequest contains serializable, application-level data for an operation.
// IdempotencyScope must identify the authenticated owner when IdempotencyKey is set.
type SubmitRequest struct {
	Name             string
	Payload          any
	IdempotencyKey   string
	IdempotencyScope string
	Metadata         map[string]string
	Retry            RetryPolicy
}

// AsyncJob is immutable application-level input supplied to an operation handler.
type AsyncJob interface {
	ID() string
	Name() string
	Decode(any) error
	Metadata() map[string]string
	Progress(context.Context, int) error
}

// AsyncHandler processes one operation delivery.
type AsyncHandler func(context.Context, AsyncJob) (any, error)

// AsyncExecutor submits, retrieves, and cancels asynchronous operations.
type AsyncExecutor interface {
	Submit(context.Context, SubmitRequest) (*Operation, error)
	Get(context.Context, string) (*Operation, error)
	Cancel(context.Context, string) error
}

// AsyncWaiter efficiently waits for an operation state change or completion.
type AsyncWaiter interface {
	Wait(context.Context, string) (*Operation, error)
}

// AsyncMetrics is a point-in-time executor metrics snapshot.
type AsyncMetrics struct {
	SubmittedTotal        uint64
	CompletedTotal        uint64
	FailedTotal           uint64
	CancelledTotal        uint64
	RetriedTotal          uint64
	QueueDepth            int64
	Running               int64
	DurationSecondsTotal  float64
	DurationCount         uint64
	QueueWaitSecondsTotal float64
	QueueWaitCount        uint64
}

// AsyncMetricsProvider supplies executor metrics.
type AsyncMetricsProvider interface {
	Metrics(context.Context) (AsyncMetrics, error)
}

// AsyncLifecycleExecutor is an executor managed by the Vial application lifecycle.
// External durable executors may implement only AsyncExecutor.
type AsyncLifecycleExecutor interface {
	AsyncExecutor
	Start(context.Context) error
	Done() <-chan error
	Shutdown(context.Context) error
}

var (
	ErrAsyncUnavailable   = errors.New("async executor is unavailable")
	ErrAsyncQueueFull     = errors.New("async executor queue is full")
	ErrInvalidOperation   = errors.New("invalid async operation")
	ErrOperationNotFound  = errors.New("operation not found")
	ErrOperationFinished  = errors.New("operation is already finished")
	ErrRetriesUnsupported = errors.New("async executor does not support retries")
	ErrInvalidPreference  = errors.New("invalid Prefer header")
)

// Async registers the application's executor. Lifecycle executors are started
// before HTTP serving and drained during application shutdown.
func (app *App) Async(executor AsyncExecutor) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.ensureMutableLocked()
	if executor == nil {
		panic("vial: async executor is nil")
	}
	if app.asyncExecutor != nil {
		panic("vial: async executor is already registered")
	}
	app.asyncExecutor = executor
}

// AsyncExecutor returns the registered executor, or nil when none is configured.
func (app *App) AsyncExecutor() AsyncExecutor {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.asyncExecutor
}

// Async returns the application's registered executor.
func (contextValue *Context) Async() AsyncExecutor {
	return contextValue.app.AsyncExecutor()
}

// Accepted writes the conventional 202 response for an asynchronous operation.
func (contextValue *Context) Accepted(operation *Operation) error {
	if operation == nil || operation.ID == "" {
		return errors.New("vial: accepted operation must have an ID")
	}
	return contextValue.AcceptedAt(operation, "/operations/"+url.PathEscape(operation.ID))
}

// AcceptedAt writes a 202 response whose Location points at statusURL.
func (contextValue *Context) AcceptedAt(operation *Operation, statusURL string) error {
	if operation == nil || strings.TrimSpace(statusURL) == "" {
		return errors.New("vial: accepted operation and status URL are required")
	}
	header := contextValue.response.Header()
	header.Set("Location", statusURL)
	header.Set("Retry-After", "2")
	if PrefersRespondAsync(contextValue.Header("Prefer")) {
		header.Set("Preference-Applied", "respond-async")
	}
	return contextValue.JSON(http.StatusAccepted, struct {
		*Operation
		StatusURL string `json:"status_url"`
	}{Operation: operation, StatusURL: statusURL})
}

// AsyncPreference is the RFC 7240 asynchronous preference requested by a client.
type AsyncPreference struct {
	RespondAsync  bool
	Wait          time.Duration
	WaitSpecified bool
}

// ParsePrefer parses respond-async and wait=n while ignoring unknown preferences.
func ParsePrefer(header string) (AsyncPreference, error) {
	var parsed AsyncPreference
	for _, preference := range splitPrefer(header, ',') {
		parts := splitPrefer(preference, ';')
		if len(parts) == 0 {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimSpace(parts[0]), "=")
		switch {
		case strings.EqualFold(strings.TrimSpace(name), "respond-async") && !hasValue:
			parsed.RespondAsync = true
			for _, parameter := range parts[1:] {
				parameterName, parameterValue, parameterHasValue := strings.Cut(strings.TrimSpace(parameter), "=")
				if strings.EqualFold(strings.TrimSpace(parameterName), "wait") {
					if err := setPreferWait(&parsed, parameterValue, parameterHasValue); err != nil {
						return parsed, err
					}
				}
			}
		case strings.EqualFold(strings.TrimSpace(name), "wait"):
			if err := setPreferWait(&parsed, value, hasValue); err != nil {
				return parsed, err
			}
		}
	}
	return parsed, nil
}

func setPreferWait(preference *AsyncPreference, value string, hasValue bool) error {
	if !hasValue {
		return fmt.Errorf("%w: wait requires seconds", ErrInvalidPreference)
	}
	seconds, err := parseWaitSeconds(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	preference.Wait = time.Duration(seconds) * time.Second
	preference.WaitSpecified = true
	return nil
}

// PrefersRespondAsync reports whether Prefer requests RFC 7240 respond-async.
func PrefersRespondAsync(header string) bool {
	preference, _ := ParsePrefer(header)
	return preference.RespondAsync
}

// Await waits up to the client preference and server maximum. completed is true
// only when the returned operation reached a terminal state.
func (contextValue *Context) Await(operation *Operation, maximum time.Duration) (current *Operation, completed bool, err error) {
	if operation == nil {
		return nil, false, errors.New("vial: operation is required")
	}
	if operation.Status.Terminal() {
		return operation, true, nil
	}
	preference, err := ParsePrefer(contextValue.Header("Prefer"))
	if err != nil {
		return nil, false, err
	}
	if !preference.RespondAsync || !preference.WaitSpecified || preference.Wait <= 0 || maximum <= 0 {
		return operation, false, nil
	}
	waiter, ok := contextValue.Async().(AsyncWaiter)
	if !ok {
		return operation, false, nil
	}
	contextValue.response.Header().Set("Preference-Applied", "respond-async")
	duration := min(preference.Wait, maximum)
	waitContext, cancel := context.WithTimeout(contextValue.Request().Context(), duration)
	defer cancel()
	current, err = waiter.Wait(waitContext, operation.ID)
	if err == nil {
		return current, current.Status.Terminal(), nil
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return nil, false, err
	}
	current, err = contextValue.Async().Get(contextValue.Request().Context(), operation.ID)
	return current, false, err
}

// AsyncMetricsHandler exposes dependency-free Prometheus/OpenMetrics metrics.
func AsyncMetricsHandler(provider AsyncMetricsProvider) Handler {
	if provider == nil {
		panic("vial: async metrics provider is required")
	}
	return func(contextValue *Context) error {
		if contextValue.Committed() {
			return errors.New("vial: response already committed")
		}
		metrics, err := provider.Metrics(contextValue.Request().Context())
		if err != nil {
			return err
		}
		var body strings.Builder
		writeAsyncMetric := func(name, metricType, value string) {
			fmt.Fprintf(&body, "# TYPE %s %s\n%s %s\n", name, metricType, name, value)
		}
		writeAsyncMetric("vial_async_submitted_total", "counter", strconv.FormatUint(metrics.SubmittedTotal, 10))
		writeAsyncMetric("vial_async_completed_total", "counter", strconv.FormatUint(metrics.CompletedTotal, 10))
		writeAsyncMetric("vial_async_failed_total", "counter", strconv.FormatUint(metrics.FailedTotal, 10))
		writeAsyncMetric("vial_async_cancelled_total", "counter", strconv.FormatUint(metrics.CancelledTotal, 10))
		writeAsyncMetric("vial_async_retried_total", "counter", strconv.FormatUint(metrics.RetriedTotal, 10))
		writeAsyncMetric("vial_async_queue_depth", "gauge", strconv.FormatInt(metrics.QueueDepth, 10))
		writeAsyncMetric("vial_async_running", "gauge", strconv.FormatInt(metrics.Running, 10))
		writeAsyncMetric("vial_async_duration_seconds_sum", "counter", strconv.FormatFloat(metrics.DurationSecondsTotal, 'f', -1, 64))
		writeAsyncMetric("vial_async_duration_seconds_count", "counter", strconv.FormatUint(metrics.DurationCount, 10))
		writeAsyncMetric("vial_async_queue_wait_seconds_sum", "counter", strconv.FormatFloat(metrics.QueueWaitSecondsTotal, 'f', -1, 64))
		writeAsyncMetric("vial_async_queue_wait_seconds_count", "counter", strconv.FormatUint(metrics.QueueWaitCount, 10))
		body.WriteString("# EOF\n")
		contextValue.response.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0; charset=utf-8")
		contextValue.response.WriteHeader(http.StatusOK)
		_, err = contextValue.response.Write([]byte(body.String()))
		return err
	}
}

func splitPrefer(value string, separator byte) []string {
	start, quoted, escaped := 0, false, false
	var parts []string
	for index := 0; index <= len(value); index++ {
		atEnd := index == len(value)
		if !atEnd {
			switch value[index] {
			case '\\':
				escaped = quoted && !escaped
				continue
			case '"':
				if !escaped {
					quoted = !quoted
				}
			}
			escaped = false
		}
		if !atEnd && (quoted || value[index] != separator) {
			continue
		}
		if part := strings.TrimSpace(value[start:index]); part != "" {
			parts = append(parts, part)
		}
		start = index + 1
	}
	return parts
}

func parseWaitSeconds(value string) (uint64, error) {
	if strings.HasPrefix(value, "\"") {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return 0, fmt.Errorf("%w: malformed wait value", ErrInvalidPreference)
		}
		value = unquoted
	}
	seconds, err := strconv.ParseUint(value, 10, 63)
	if err != nil || seconds > uint64((1<<63-1)/int64(time.Second)) {
		return 0, fmt.Errorf("%w: wait must be non-negative seconds", ErrInvalidPreference)
	}
	return seconds, nil
}

// OperationAuthorizer applies application ownership rules before status or
// cancellation is exposed. Helpers require one to avoid globally readable IDs.
type OperationAuthorizer func(*Context, *Operation) error

// OperationStatusHandler returns a polling handler for a route containing {id}.
func OperationStatusHandler(executor AsyncExecutor, authorize OperationAuthorizer) Handler {
	if executor == nil || authorize == nil {
		panic("vial: operation status executor and authorizer are required")
	}
	return func(contextValue *Context) error {
		operation, err := executor.Get(contextValue.Request().Context(), contextValue.Param("id"))
		if err != nil {
			return err
		}
		if err := authorize(contextValue, operation); err != nil {
			return err
		}
		if !operation.Status.Terminal() {
			contextValue.response.Header().Set("Retry-After", "2")
		}
		return contextValue.JSON(http.StatusOK, operation)
	}
}

// OperationCancelHandler returns a cancellation handler for a route containing {id}.
func OperationCancelHandler(executor AsyncExecutor, authorize OperationAuthorizer) Handler {
	if executor == nil || authorize == nil {
		panic("vial: operation cancellation executor and authorizer are required")
	}
	return func(contextValue *Context) error {
		operation, err := executor.Get(contextValue.Request().Context(), contextValue.Param("id"))
		if err != nil {
			return err
		}
		if err := authorize(contextValue, operation); err != nil {
			return err
		}
		if err := executor.Cancel(contextValue.Request().Context(), operation.ID); err != nil {
			return err
		}
		return contextValue.NoContent(http.StatusNoContent)
	}
}
