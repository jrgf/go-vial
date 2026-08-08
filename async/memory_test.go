package async

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
)

func startExecutor(t *testing.T, executor *MemoryExecutor) context.CancelFunc {
	t.Helper()
	contextValue, cancel := context.WithCancel(context.Background())
	if err := executor.Start(contextValue); err != nil {
		cancel()
		t.Fatalf("start executor: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		if err := executor.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown executor: %v", err)
		}
	})
	return cancel
}

func waitForStatus(t *testing.T, executor *MemoryExecutor, id string, status vial.OperationStatus) *vial.Operation {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		operation, err := executor.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("get operation: %v", err)
		}
		if operation.Status == status {
			return operation
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("operation %s did not reach %s", id, status)
	return nil
}

func TestMemoryExecutorDuplicateSubmissions(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	started := make(chan struct{})
	proceed := make(chan struct{})
	executor := NewMemoryExecutor(WithWorkers(1), WithQueueSize(2))
	executor.Handle("reports.generate", func(contextValue context.Context, job Job) (any, error) {
		close(started)
		<-proceed
		var request payload
		if err := job.Decode(&request); err != nil {
			return nil, err
		}
		if err := job.Progress(contextValue, 60); err != nil {
			return nil, err
		}
		return map[string]string{"name": request.Name}, nil
	})
	startExecutor(t, executor)

	requestContext, cancelRequest := context.WithCancel(context.Background())
	input := &payload{Name: "original"}
	metadata := map[string]string{"owner": "rafa"}
	operation, err := executor.Submit(requestContext, vial.SubmitRequest{
		Name:             "reports.generate",
		Payload:          input,
		IdempotencyKey:   "report-1",
		IdempotencyScope: "rafa",
		Metadata:         metadata,
	})
	if err != nil {
		t.Fatalf("submit operation: %v", err)
	}
	<-started
	cancelRequest()
	input.Name = "mutated"
	metadata["owner"] = "other"
	duplicate, err := executor.Submit(context.Background(), vial.SubmitRequest{
		Name:             "reports.generate",
		Payload:          payload{Name: "different"},
		IdempotencyKey:   "report-1",
		IdempotencyScope: "rafa",
	})
	if err != nil || duplicate.ID != operation.ID {
		t.Fatalf("duplicate = %#v, error %v", duplicate, err)
	}
	close(proceed)
	completed := waitForStatus(t, executor, operation.ID, vial.OperationSucceeded)
	result, ok := completed.Result.(map[string]any)
	if !ok || result["name"] != "original" {
		t.Fatalf("result = %#v", completed.Result)
	}
	if completed.Metadata["owner"] != "rafa" || completed.Progress != 100 {
		t.Fatalf("completed operation = %#v", completed)
	}
	metrics, err := executor.Metrics(context.Background())
	if err != nil || metrics.SubmittedTotal != 1 || metrics.CompletedTotal != 1 || metrics.QueueWaitCount != 1 {
		t.Fatalf("metrics = %#v, error %v", metrics, err)
	}
}

func TestMemoryExecutorUsesMultipleWorkers(t *testing.T) {
	const workers = 3
	started := make(chan struct{}, workers)
	release := make(chan struct{})
	executor := NewMemoryExecutor(WithWorkers(workers), WithQueueSize(workers))
	executor.Handle("parallel", func(contextValue context.Context, _ Job) (any, error) {
		started <- struct{}{}
		select {
		case <-release:
			return "done", nil
		case <-contextValue.Done():
			return nil, contextValue.Err()
		}
	})
	startExecutor(t, executor)

	operations := make([]*vial.Operation, 0, workers)
	for range workers {
		operation, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "parallel"})
		if err != nil {
			t.Fatalf("submit operation: %v", err)
		}
		operations = append(operations, operation)
	}
	for range workers {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("operations did not run concurrently")
		}
	}
	close(release)
	for _, operation := range operations {
		waitForStatus(t, executor, operation.ID, vial.OperationSucceeded)
	}
}

func TestMemoryExecutorProcessInterruptionAndRestart(t *testing.T) {
	started := make(chan struct{})
	first := NewMemoryExecutor(WithWorkers(1), WithQueueSize(1))
	first.Handle("block", func(contextValue context.Context, _ Job) (any, error) {
		close(started)
		<-contextValue.Done()
		return nil, contextValue.Err()
	})
	lifecycleContext, interrupt := context.WithCancel(context.Background())
	if err := first.Start(lifecycleContext); err != nil {
		t.Fatalf("start first executor: %v", err)
	}
	operation, err := first.Submit(context.Background(), vial.SubmitRequest{Name: "block"})
	if err != nil {
		t.Fatalf("submit operation: %v", err)
	}
	<-started
	interrupt()
	shutdownContext, cancelShutdown := context.WithCancel(context.Background())
	cancelShutdown()
	if err := first.Shutdown(shutdownContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted shutdown error = %v", err)
	}
	select {
	case <-first.workersDone:
	case <-time.After(time.Second):
		t.Fatal("interrupted worker did not stop")
	}

	restarted := NewMemoryExecutor(WithWorkers(1), WithQueueSize(1))
	restarted.Handle("block", func(context.Context, Job) (any, error) { return "restarted", nil })
	startExecutor(t, restarted)
	if _, err := restarted.Get(context.Background(), operation.ID); !errors.Is(err, vial.ErrOperationNotFound) {
		t.Fatalf("operation survived in-memory restart: %v", err)
	}
	newOperation, err := restarted.Submit(context.Background(), vial.SubmitRequest{Name: "block"})
	if err != nil {
		t.Fatalf("submit after restart: %v", err)
	}
	waitForStatus(t, restarted, newOperation.ID, vial.OperationSucceeded)
}

func TestMemoryExecutorBackpressureAndCancellation(t *testing.T) {
	started := make(chan struct{})
	executor := NewMemoryExecutor(WithWorkers(1), WithQueueSize(1))
	executor.Handle("block", func(contextValue context.Context, _ Job) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-contextValue.Done()
		return nil, contextValue.Err()
	})
	startExecutor(t, executor)
	if _, err := executor.Submit(context.Background(), vial.SubmitRequest{
		Name:  "block",
		Retry: vial.RetryPolicy{MaxAttempts: 2},
	}); !errors.Is(err, vial.ErrRetriesUnsupported) {
		t.Fatalf("retry submit error = %v", err)
	}
	first, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "block"})
	if err != nil {
		t.Fatalf("submit first operation: %v", err)
	}
	<-started
	second, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "block"})
	if err != nil {
		t.Fatalf("submit second operation: %v", err)
	}
	if _, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "block"}); !errors.Is(err, vial.ErrAsyncQueueFull) {
		t.Fatalf("third submit error = %v", err)
	}
	if err := executor.Cancel(context.Background(), second.ID); err != nil {
		t.Fatalf("cancel pending operation: %v", err)
	}
	if err := executor.Cancel(context.Background(), first.ID); err != nil {
		t.Fatalf("cancel running operation: %v", err)
	}
	waitForStatus(t, executor, first.ID, vial.OperationCancelled)
	waitForStatus(t, executor, second.ID, vial.OperationCancelled)
}

func TestMemoryExecutorTimeoutAndPanicAreSafe(t *testing.T) {
	executor := NewMemoryExecutor(WithWorkers(1), WithQueueSize(2), WithTaskTimeout(10*time.Millisecond))
	executor.Handle("timeout", func(contextValue context.Context, _ Job) (any, error) {
		<-contextValue.Done()
		return nil, contextValue.Err()
	})
	executor.Handle("panic", func(context.Context, Job) (any, error) {
		panic("internal detail")
	})
	startExecutor(t, executor)
	timedOut, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "timeout"})
	if err != nil {
		t.Fatalf("submit timeout operation: %v", err)
	}
	timedOut = waitForStatus(t, executor, timedOut.ID, vial.OperationFailed)
	if timedOut.Error == nil || timedOut.Error.Code != "operation_timeout" {
		t.Fatalf("timeout error = %#v", timedOut.Error)
	}
	panicked, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "panic"})
	if err != nil {
		t.Fatalf("submit panic operation: %v", err)
	}
	panicked = waitForStatus(t, executor, panicked.ID, vial.OperationFailed)
	if panicked.Error == nil || panicked.Error.Code != "operation_failed" || panicked.Error.Message == "internal detail" {
		t.Fatalf("panic error = %#v", panicked.Error)
	}
}
