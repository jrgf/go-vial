package vial

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type testAsyncExecutor struct {
	operation *Operation
	started   chan struct{}
	stopped   chan struct{}
	done      chan error
	cancelled bool
	once      sync.Once
}

type edgeAsyncExecutor struct {
	operation *Operation
	getErr    error
	cancelErr error
	waitErr   error
	metrics   AsyncMetrics
	metricErr error
}

func (executor *edgeAsyncExecutor) Submit(context.Context, SubmitRequest) (*Operation, error) {
	return executor.operation, nil
}

func (executor *edgeAsyncExecutor) Get(context.Context, string) (*Operation, error) {
	return executor.operation, executor.getErr
}

func (executor *edgeAsyncExecutor) Cancel(context.Context, string) error { return executor.cancelErr }

func (executor *edgeAsyncExecutor) Wait(context.Context, string) (*Operation, error) {
	return executor.operation, executor.waitErr
}

func (executor *edgeAsyncExecutor) Metrics(context.Context) (AsyncMetrics, error) {
	return executor.metrics, executor.metricErr
}

func requireAsyncPanic(t *testing.T, function func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	function()
}

func (executor *testAsyncExecutor) Submit(context.Context, SubmitRequest) (*Operation, error) {
	return executor.operation, nil
}

func (executor *testAsyncExecutor) Get(context.Context, string) (*Operation, error) {
	if executor.operation == nil {
		return nil, ErrOperationNotFound
	}
	return executor.operation, nil
}

func (executor *testAsyncExecutor) Cancel(context.Context, string) error {
	executor.cancelled = true
	return nil
}

func (executor *testAsyncExecutor) Start(context.Context) error {
	close(executor.started)
	return nil
}

func (executor *testAsyncExecutor) Done() <-chan error { return executor.done }

func (executor *testAsyncExecutor) Shutdown(context.Context) error {
	executor.once.Do(func() {
		close(executor.stopped)
		executor.done <- nil
	})
	return nil
}

func TestAsyncExecutorUsesApplicationLifecycle(t *testing.T) {
	executor := &testAsyncExecutor{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
		done:    make(chan error, 1),
	}
	app := New()
	app.Async(executor)
	if app.AsyncExecutor() != executor {
		t.Fatal("registered async executor was not returned")
	}

	contextValue, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- app.runLifecycle(contextValue) }()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("async executor did not start")
	}
	cancel()
	select {
	case <-executor.stopped:
	case <-time.After(time.Second):
		t.Fatal("async executor did not stop")
	}
	if err := <-result; err != nil {
		t.Fatalf("run lifecycle: %v", err)
	}
}

func TestAsyncHTTPHelpers(t *testing.T) {
	operation := &Operation{
		ID:       "op_test",
		Name:     "reports.generate",
		Status:   OperationRunning,
		Metadata: map[string]string{"owner": "rafa"},
	}
	executor := &testAsyncExecutor{operation: operation}
	authorize := func(contextValue *Context, operation *Operation) error {
		if contextValue.Header("X-Owner") != operation.Metadata["owner"] {
			return Forbidden("operation_forbidden", "Operation access denied")
		}
		return nil
	}
	app := New()
	app.Post("/reports", func(contextValue *Context) error {
		return contextValue.Accepted(operation)
	})
	app.Get("/operations/{id}", OperationStatusHandler(executor, authorize))
	app.Delete("/operations/{id}", OperationCancelHandler(executor, authorize))

	request := httptest.NewRequest(http.MethodPost, "/reports", nil)
	request.Header.Set("Prefer", `handling=strict, RESPOND-ASYNC; wait=2`)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") != "/operations/op_test" {
		t.Fatalf("accepted response = %d, location %q", response.Code, response.Header().Get("Location"))
	}
	if response.Header().Get("Preference-Applied") != "respond-async" {
		t.Fatalf("Preference-Applied = %q", response.Header().Get("Preference-Applied"))
	}
	var accepted struct {
		StatusURL string `json:"status_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil || accepted.StatusURL != "/operations/op_test" {
		t.Fatalf("accepted body = %#v, error %v", accepted, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/operations/op_test", nil)
	response = httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthorized poll status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/operations/op_test", nil)
	request.Header.Set("X-Owner", "rafa")
	response = httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Retry-After") != "2" {
		t.Fatalf("poll response = %d, retry-after %q", response.Code, response.Header().Get("Retry-After"))
	}

	request = httptest.NewRequest(http.MethodDelete, "/operations/op_test", nil)
	request.Header.Set("X-Owner", "rafa")
	response = httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !executor.cancelled {
		t.Fatalf("cancel response = %d, cancelled %v", response.Code, executor.cancelled)
	}
}

func TestPrefersRespondAsync(t *testing.T) {
	tests := map[string]bool{
		"respond-async":                  true,
		"handling=strict, RESPOND-ASYNC": true,
		`example="x, respond-async"`:     false,
		"respond-asyncish":               false,
		";":                              false,
		"":                               false,
	}
	for header, want := range tests {
		if got := PrefersRespondAsync(header); got != want {
			t.Errorf("PrefersRespondAsync(%q) = %v, want %v", header, got, want)
		}
	}
	preference, err := ParsePrefer(`respond-async; wait="2"`)
	if err != nil || !preference.RespondAsync || !preference.WaitSpecified || preference.Wait != 2*time.Second {
		t.Fatalf("parsed preference = %#v, error %v", preference, err)
	}
	if _, err := ParsePrefer("respond-async, wait=-1"); !errors.Is(err, ErrInvalidPreference) {
		t.Fatalf("invalid wait error = %v", err)
	}
	if preference, err := ParsePrefer("unknown; wait=invalid"); err != nil || preference.WaitSpecified {
		t.Fatalf("unknown preference = %#v, error %v", preference, err)
	}
}

func TestAsyncErrorsHaveSafeHTTPResponses(t *testing.T) {
	app := New()
	app.Post("/", func(*Context) error { return ErrAsyncQueueFull })
	app.Get("/failed", func(*Context) error {
		return &OperationError{Code: "report_failed", Message: "The report failed"}
	})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "5" {
		t.Fatalf("queue-full response = %d, retry-after %q", response.Code, response.Header().Get("Retry-After"))
	}
	response = httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/failed", nil))
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"report_failed"`) {
		t.Fatalf("operation error response = %d %s", response.Code, response.Body.String())
	}
}

func TestAsyncContractsAndInvalidConfiguration(t *testing.T) {
	var nilError *OperationError
	if nilError.Error() != "" {
		t.Fatalf("nil operation error = %q", nilError.Error())
	}
	if got := (&OperationError{Code: "failed"}).Error(); got != "failed" {
		t.Fatalf("code-only operation error = %q", got)
	}

	requireAsyncPanic(t, func() { New().Async(nil) })
	app := New()
	executor := &testAsyncExecutor{}
	app.Async(executor)
	requireAsyncPanic(t, func() { app.Async(executor) })
	requireAsyncPanic(t, func() { OperationStatusHandler(nil, func(*Context, *Operation) error { return nil }) })
	requireAsyncPanic(t, func() { OperationStatusHandler(executor, nil) })
	requireAsyncPanic(t, func() { OperationCancelHandler(nil, func(*Context, *Operation) error { return nil }) })
	requireAsyncPanic(t, func() { OperationCancelHandler(executor, nil) })
	requireAsyncPanic(t, func() { AsyncMetricsHandler(nil) })
}

func TestAsyncInvalidResponsesAndPreferences(t *testing.T) {
	for _, header := range []string{"wait", "respond-async; wait", `wait="2`, `wait=not-a-number`} {
		if _, err := ParsePrefer(header); !errors.Is(err, ErrInvalidPreference) {
			t.Errorf("ParsePrefer(%q) error = %v", header, err)
		}
	}
	preference, err := ParsePrefer(`handling="a\\,b", respond-async; wait=0`)
	if err != nil || !preference.RespondAsync || !preference.WaitSpecified || preference.Wait != 0 {
		t.Fatalf("escaped preference = %#v, %v", preference, err)
	}

	app := New()
	app.Get("/accepted-nil", func(contextValue *Context) error { return contextValue.Accepted(nil) })
	app.Get("/accepted-empty", func(contextValue *Context) error { return contextValue.Accepted(&Operation{}) })
	app.Get("/accepted-at-nil", func(contextValue *Context) error { return contextValue.AcceptedAt(nil, "/status") })
	app.Get("/accepted-at-empty", func(contextValue *Context) error { return contextValue.AcceptedAt(&Operation{ID: "op"}, " ") })
	for _, path := range []string{"/accepted-nil", "/accepted-empty", "/accepted-at-nil", "/accepted-at-empty"} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusInternalServerError {
			t.Errorf("%s status = %d", path, response.Code)
		}
	}
}

func TestAwaitEdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		executor   AsyncExecutor
		operation  *Operation
		prefer     string
		maximum    time.Duration
		wantStatus int
	}{
		{name: "nil operation", executor: &edgeAsyncExecutor{}, prefer: "respond-async, wait=1", maximum: time.Second, wantStatus: http.StatusInternalServerError},
		{name: "terminal", executor: &edgeAsyncExecutor{}, operation: &Operation{ID: "op", Status: OperationSucceeded}, prefer: "respond-async, wait=1", maximum: time.Second, wantStatus: http.StatusNoContent},
		{name: "invalid preference", executor: &edgeAsyncExecutor{}, operation: &Operation{ID: "op", Status: OperationPending}, prefer: "wait=bad", maximum: time.Second, wantStatus: http.StatusBadRequest},
		{name: "no wait", executor: &edgeAsyncExecutor{}, operation: &Operation{ID: "op", Status: OperationPending}, maximum: time.Second, wantStatus: http.StatusAccepted},
		{name: "zero maximum", executor: &edgeAsyncExecutor{}, operation: &Operation{ID: "op", Status: OperationPending}, prefer: "respond-async, wait=1", wantStatus: http.StatusAccepted},
		{name: "wait error", executor: &edgeAsyncExecutor{waitErr: errors.New("wait failed")}, operation: &Operation{ID: "op", Status: OperationPending}, prefer: "respond-async, wait=1", maximum: time.Second, wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := New()
			app.Async(test.executor)
			app.Get("/", func(contextValue *Context) error {
				operation, completed, err := contextValue.Await(test.operation, test.maximum)
				if err != nil {
					return err
				}
				if completed {
					return contextValue.NoContent(http.StatusNoContent)
				}
				return contextValue.Accepted(operation)
			})
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Prefer", test.prefer)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}

	app := New()
	app.Async(&testAsyncExecutor{})
	app.Get("/", func(contextValue *Context) error {
		operation := &Operation{ID: "op", Status: OperationPending}
		_, completed, err := contextValue.Await(operation, time.Second)
		if err != nil || completed {
			t.Fatalf("non-waiter await = %v, %v", completed, err)
		}
		return contextValue.Accepted(operation)
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Prefer", "respond-async, wait=1")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("non-waiter status = %d", response.Code)
	}
}

func TestAsyncHandlerErrorPaths(t *testing.T) {
	operation := &Operation{ID: "op", Status: OperationRunning}
	getFailure := &edgeAsyncExecutor{getErr: ErrOperationNotFound}
	cancelFailure := &edgeAsyncExecutor{operation: operation, cancelErr: ErrOperationFinished}
	metricFailure := &edgeAsyncExecutor{metricErr: errors.New("metrics failed")}
	authorizeFailure := &edgeAsyncExecutor{operation: operation}

	app := New()
	app.Get("/status/{id}", OperationStatusHandler(getFailure, func(*Context, *Operation) error { return nil }))
	app.Delete("/get/{id}", OperationCancelHandler(getFailure, func(*Context, *Operation) error { return nil }))
	app.Delete("/authorize/{id}", OperationCancelHandler(authorizeFailure, func(*Context, *Operation) error {
		return Forbidden("forbidden", "Forbidden")
	}))
	app.Delete("/cancel/{id}", OperationCancelHandler(cancelFailure, func(*Context, *Operation) error { return nil }))
	app.Get("/metrics", AsyncMetricsHandler(metricFailure))
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/status/op", http.StatusNotFound},
		{http.MethodDelete, "/get/op", http.StatusNotFound},
		{http.MethodDelete, "/authorize/op", http.StatusForbidden},
		{http.MethodDelete, "/cancel/op", http.StatusConflict},
		{http.MethodGet, "/metrics", http.StatusInternalServerError},
	} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status {
			t.Errorf("%s %s status = %d, want %d", test.method, test.path, response.Code, test.status)
		}
	}

	provider := &edgeAsyncExecutor{}
	handler := AsyncMetricsHandler(provider)
	var committedErr error
	committed := New()
	committed.Get("/", func(contextValue *Context) error {
		if err := contextValue.NoContent(http.StatusNoContent); err != nil {
			return err
		}
		committedErr = handler(contextValue)
		return nil
	})
	response := httptest.NewRecorder()
	committed.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if committedErr == nil {
		t.Fatal("metrics handler accepted a committed response")
	}
}
