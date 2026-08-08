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
