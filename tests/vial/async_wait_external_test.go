package vial_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/async"
)

func TestAwaitAndMetricsHandlers(t *testing.T) {
	executor := async.NewMemoryExecutor(async.WithWorkers(1), async.WithQueueSize(2))
	executor.Handle("fast", func(context.Context, vial.AsyncJob) (any, error) {
		return map[string]string{"state": "ready"}, nil
	})
	executor.Handle("slow", func(contextValue context.Context, _ vial.AsyncJob) (any, error) {
		select {
		case <-time.After(100 * time.Millisecond):
			return "late", nil
		case <-contextValue.Done():
			return nil, contextValue.Err()
		}
	})
	lifecycleContext, cancelLifecycle := context.WithCancel(context.Background())
	if err := executor.Start(lifecycleContext); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	t.Cleanup(func() {
		cancelLifecycle()
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := executor.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown executor: %v", err)
		}
	})

	app := vial.New()
	app.Async(executor)
	app.Post("/fast", awaitHandler("fast", 100*time.Millisecond))
	app.Post("/slow", awaitHandler("slow", 5*time.Millisecond))
	app.Get("/metrics", vial.AsyncMetricsHandler(executor))

	request := httptest.NewRequest(http.MethodPost, "/fast", nil)
	request.Header.Set("Prefer", "respond-async, wait=1")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ready") || response.Header().Get("Preference-Applied") != "respond-async" {
		t.Fatalf("fast response = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/slow", nil)
	request.Header.Set("Prefer", "respond-async, wait=1")
	response = httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") == "" {
		t.Fatalf("slow response = %d, location %q", response.Code, response.Header().Get("Location"))
	}

	response = httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "vial_async_submitted_total 2") || !strings.Contains(body, "# EOF") {
		t.Fatalf("metrics response = %d %s", response.Code, body)
	}
}

func awaitHandler(name string, maximum time.Duration) vial.Handler {
	return func(contextValue *vial.Context) error {
		operation, err := contextValue.Async().Submit(contextValue.Request().Context(), vial.SubmitRequest{Name: name})
		if err != nil {
			return err
		}
		operation, completed, err := contextValue.Await(operation, maximum)
		if err != nil {
			return err
		}
		if completed && operation.Status == vial.OperationSucceeded {
			return contextValue.JSON(http.StatusOK, operation.Result)
		}
		if completed && operation.Error != nil {
			return operation.Error
		}
		return contextValue.Accepted(operation)
	}
}
