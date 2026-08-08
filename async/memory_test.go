package async

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
)

func requirePanic(t *testing.T, function func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	function()
}

func TestOperationIDAndSnapshotEdges(t *testing.T) {
	originalGenerator, originalReader := generateOperationID, readRandom
	t.Cleanup(func() {
		generateOperationID = originalGenerator
		readRandom = originalReader
	})

	want := errors.New("random failed")
	readRandom = func([]byte) (int, error) { return 0, want }
	if _, err := newOperationID(); !errors.Is(err, want) {
		t.Fatalf("newOperationID() error = %v", err)
	}
	readRandom = originalReader

	executor := NewMemoryExecutor(WithWorkers(1), WithQueueSize(2))
	executor.Handle("test", func(context.Context, Job) (any, error) { return "ok", nil })
	startExecutor(t, executor)
	generateOperationID = func() (string, error) { return "", want }
	if _, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "test"}); !errors.Is(err, want) {
		t.Fatalf("initial ID error = %v", err)
	}

	generateOperationID = originalGenerator
	operation, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	generateOperationID = func() (string, error) {
		calls++
		if calls == 1 {
			return operation.ID, nil
		}
		return "", want
	}
	if _, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "test"}); !errors.Is(err, want) {
		t.Fatalf("collision ID error = %v", err)
	}
	if snapshot(nil) != nil {
		t.Fatal("snapshot(nil) is not nil")
	}

	deadline := time.Now().Add(time.Second)
	for {
		current, getErr := executor.Get(context.Background(), operation.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status.Terminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("operation did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	executor.cancelUnfinished()
}

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

func TestMemoryExecutorValidationAndLifecycleEdges(t *testing.T) {
	requirePanic(t, func() { NewMemoryExecutor(WithWorkers(0)) })
	requirePanic(t, func() { NewMemoryExecutor(WithQueueSize(0)) })
	requirePanic(t, func() { NewMemoryExecutor(WithTaskTimeout(0)) })
	requirePanic(t, func() { NewMemoryExecutor(WithLogger(nil)) })
	requirePanic(t, func() { NewMemoryExecutor(nil) })

	executor := NewMemoryExecutor(WithLogger(slog.Default()))
	requirePanic(t, func() { executor.Handle("", nil) })
	executor.Handle("ok", func(context.Context, Job) (any, error) { return "ok", nil })
	requirePanic(t, func() { executor.Handle("ok", func(context.Context, Job) (any, error) { return nil, nil }) })
	if err := executor.Ready(context.Background()); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("ready before start = %v", err)
	}
	if err := executor.Shutdown(context.Background()); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("shutdown before start = %v", err)
	}
	if err := executor.Start(nil); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	if err := executor.Start(context.Background()); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("second start = %v", err)
	}
	requirePanic(t, func() { executor.Handle("late", func(context.Context, Job) (any, error) { return nil, nil }) })

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executor.Submit(cancelled, vial.SubmitRequest{Name: "ok"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled submit = %v", err)
	}
	invalid := []vial.SubmitRequest{
		{},
		{Name: "ok", IdempotencyKey: "key"},
		{Name: "ok", Retry: vial.RetryPolicy{MaxAttempts: -1}},
		{Name: "ok", Payload: make(chan int)},
		{Name: "missing"},
	}
	for _, request := range invalid {
		if _, err := executor.Submit(context.Background(), request); err == nil {
			t.Fatalf("submit %#v unexpectedly succeeded", request)
		}
	}
	if _, err := executor.Get(cancelled, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get = %v", err)
	}
	if _, err := executor.Get(context.Background(), "missing"); !errors.Is(err, vial.ErrOperationNotFound) {
		t.Fatalf("missing get = %v", err)
	}
	if err := executor.Cancel(cancelled, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cancel = %v", err)
	}
	if err := executor.Cancel(context.Background(), "missing"); !errors.Is(err, vial.ErrOperationNotFound) {
		t.Fatalf("missing cancel = %v", err)
	}
	if _, err := executor.Wait(context.Background(), "missing"); !errors.Is(err, vial.ErrOperationNotFound) {
		t.Fatalf("missing wait = %v", err)
	}
	if _, err := executor.Metrics(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled metrics = %v", err)
	}
	if err := executor.Ready(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ready = %v", err)
	}
	if err := executor.Ready(nil); err != nil {
		t.Fatalf("ready executor: %v", err)
	}

	operation, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "ok"})
	if err != nil {
		t.Fatalf("submit valid operation: %v", err)
	}
	completed, err := executor.Wait(nil, operation.ID)
	if err != nil || completed.Status != vial.OperationSucceeded {
		t.Fatalf("wait = %#v, %v", completed, err)
	}
	if completed, err = executor.Wait(context.Background(), operation.ID); err != nil || completed.Status != vial.OperationSucceeded {
		t.Fatalf("terminal wait = %#v, %v", completed, err)
	}
	if err := executor.Cancel(context.Background(), operation.ID); err != nil {
		t.Fatalf("cancel completed operation: %v", err)
	}
	if _, err := executor.Metrics(nil); err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if err := executor.Shutdown(nil); err != nil {
		t.Fatalf("shutdown executor: %v", err)
	}
	if err := executor.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
	if err := <-executor.Done(); err != nil {
		t.Fatalf("done error: %v", err)
	}
	if _, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: "ok"}); !errors.Is(err, vial.ErrAsyncUnavailable) {
		t.Fatalf("submit after shutdown = %v", err)
	}
}

func TestMemoryExecutorWaitCancellationAndJobContract(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	executor := NewMemoryExecutor(WithWorkers(1), WithQueueSize(1))
	executor.Handle("job", func(contextValue context.Context, job Job) (any, error) {
		close(started)
		if job.ID() == "" || job.Name() != "job" || job.Metadata()["user_id"] != "user-1" {
			return nil, errors.New("invalid job contract")
		}
		if err := job.Decode(nil); err == nil {
			return nil, errors.New("nil decode succeeded")
		}
		var number int
		if err := job.Decode(&number); err == nil {
			return nil, errors.New("invalid decode succeeded")
		}
		if err := job.Progress(contextValue, -1); !errors.Is(err, vial.ErrInvalidOperation) {
			return nil, err
		}
		if err := job.Progress(contextValue, 101); !errors.Is(err, vial.ErrInvalidOperation) {
			return nil, err
		}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := job.Progress(cancelled, 10); !errors.Is(err, context.Canceled) {
			return nil, err
		}
		if err := job.Progress(contextValue, 50); err != nil {
			return nil, err
		}
		<-release
		return "done", nil
	})
	startExecutor(t, executor)
	operation, err := executor.Submit(context.Background(), vial.SubmitRequest{
		Name:     "job",
		Payload:  map[string]string{"value": "text"},
		Metadata: map[string]string{"user_id": "user-1", "tenant_id": "tenant-1", "trace_id": "trace-1"},
	})
	if err != nil {
		t.Fatalf("submit operation: %v", err)
	}
	<-started
	waitContext, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if _, err := executor.Wait(waitContext, operation.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait = %v", err)
	}
	close(release)
	completed := waitForStatus(t, executor, operation.ID, vial.OperationSucceeded)
	if completed.Progress != 100 {
		t.Fatalf("completed progress = %d", completed.Progress)
	}
	if err := executor.reportProgress(context.Background(), "missing", 10); !errors.Is(err, vial.ErrOperationNotFound) {
		t.Fatalf("missing progress = %v", err)
	}
	if err := executor.reportProgress(context.Background(), operation.ID, 10); !errors.Is(err, vial.ErrOperationFinished) {
		t.Fatalf("finished progress = %v", err)
	}
}

func TestMemoryExecutorFailureRepresentations(t *testing.T) {
	executor := NewMemoryExecutor(WithWorkers(1), WithQueueSize(4), WithTaskTimeout(10*time.Millisecond))
	executor.Handle("encode", func(context.Context, Job) (any, error) { return make(chan int), nil })
	executor.Handle("public", func(context.Context, Job) (any, error) { return nil, &vial.OperationError{} })
	executor.Handle("late-success", func(contextValue context.Context, _ Job) (any, error) {
		<-contextValue.Done()
		return "late", nil
	})
	startExecutor(t, executor)

	for _, test := range []struct {
		name string
		code string
	}{
		{name: "encode", code: "operation_failed"},
		{name: "public", code: "operation_failed"},
		{name: "late-success", code: "operation_timeout"},
	} {
		operation, err := executor.Submit(context.Background(), vial.SubmitRequest{Name: test.name})
		if err != nil {
			t.Fatalf("submit %s: %v", test.name, err)
		}
		operation = waitForStatus(t, executor, operation.ID, vial.OperationFailed)
		if operation.Error == nil || operation.Error.Code != test.code || operation.Error.Message == "" {
			t.Fatalf("%s error = %#v", test.name, operation.Error)
		}
		executor.run(operation.ID)
	}
	executor.run("missing")
	executor.finish("missing", nil, nil, nil)
}
