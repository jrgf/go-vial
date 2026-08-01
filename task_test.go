package vial

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildValidatesTasks(t *testing.T) {
	tests := []struct {
		name     string
		register func(*App)
		want     string
	}{
		{
			name: "invalid name",
			register: func(app *App) {
				app.Go(" worker ", func(context.Context) error { return nil })
			},
			want: "invalid task name",
		},
		{
			name: "nil task",
			register: func(app *App) {
				app.Go("worker", nil)
			},
			want: "task \"worker\" is nil",
		},
		{
			name: "duplicate name",
			register: func(app *App) {
				app.Go("worker", func(context.Context) error { return nil })
				app.Go("worker", func(context.Context) error { return nil })
			},
			want: "duplicate task name \"worker\"",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := New()
			test.register(app)
			if err := app.Build(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Build() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestTaskRegistrationIsImmutableAfterBuild(t *testing.T) {
	app := New()
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected task registration to panic after build")
		}
	}()
	app.Go("worker", func(context.Context) error { return nil })
}

func TestTaskLifecycleOrder(t *testing.T) {
	events := make([]string, 0, 4)
	started := make(chan struct{})
	stopped := make(chan struct{})
	app := New()
	app.OnStart(func(context.Context) error {
		events = append(events, "hook start")
		return nil
	})
	app.Go("worker", func(contextValue context.Context) error {
		events = append(events, "task start")
		close(started)
		<-contextValue.Done()
		events = append(events, "task stop")
		close(stopped)
		return contextValue.Err()
	})
	app.OnStop(func(context.Context) error {
		select {
		case <-stopped:
		default:
			return errors.New("task was still running")
		}
		events = append(events, "hook stop")
		return nil
	})

	contextValue, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.runLifecycle(contextValue) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run lifecycle: %v", err)
	}

	want := []string{"hook start", "task start", "task stop", "hook stop"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestCriticalTaskOutcomesStopApplication(t *testing.T) {
	wantErr := errors.New("worker failed")
	tests := []struct {
		name string
		task Task
		want error
		text string
	}{
		{name: "error", task: func(context.Context) error { return wantErr }, want: wantErr, text: "failed"},
		{name: "nil", task: func(context.Context) error { return nil }, text: "exited unexpectedly"},
		{name: "panic", task: func(context.Context) error { panic(wantErr) }, want: wantErr, text: "panicked"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := New()
			app.Go("worker", test.task)
			err := app.runLifecycle(context.Background())
			if test.want != nil && !errors.Is(err, test.want) {
				t.Errorf("run lifecycle error = %v, want wrapped %v", err, test.want)
			}
			if err == nil || !strings.Contains(err.Error(), "worker") || !strings.Contains(err.Error(), test.text) {
				t.Fatalf("run lifecycle error = %v, want named %s outcome", err, test.text)
			}
		})
	}
}

func TestNonCriticalTaskFailuresDoNotStopApplication(t *testing.T) {
	logs := make(chan taskLog, 2)
	app := New(WithLogger(slog.New(taskLogHandler{logs: logs})))
	app.Go("optional-error", func(context.Context) error {
		return errors.New("optional task failed")
	}, NonCritical())
	app.Go("optional-panic", func(context.Context) error {
		panic("optional task panicked")
	}, NonCritical())
	app.Go("blocking", func(contextValue context.Context) error {
		<-contextValue.Done()
		return contextValue.Err()
	})

	contextValue, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.runLifecycle(contextValue) }()

	logged := make(map[string]bool, 2)
	for len(logged) < 2 {
		entry := <-logs
		if entry.message == "background task stopped" {
			logged[entry.task] = true
		}
	}
	for _, name := range []string{"optional-error", "optional-panic"} {
		if !logged[name] {
			t.Errorf("missing log for task %q", name)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("application stopped after non-critical failure: %v", err)
	default:
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run lifecycle: %v", err)
	}
}

func TestCriticalTaskShutdownFailureIsReturned(t *testing.T) {
	wantErr := errors.New("shutdown failed")
	started := make(chan struct{})
	app := New()
	app.Go("worker", func(contextValue context.Context) error {
		close(started)
		<-contextValue.Done()
		return wantErr
	})

	contextValue, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.runLifecycle(contextValue) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("run lifecycle error = %v, want named shutdown failure", err)
	}
}

func TestTaskShutdownTimeoutNamesUnfinishedTask(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	exited := make(chan struct{})
	app := New(WithShutdownTimeout(20 * time.Millisecond))
	app.Go("stuck-worker", func(context.Context) error {
		defer close(exited)
		close(started)
		<-release
		return nil
	})

	contextValue, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.runLifecycle(contextValue) }()
	<-started
	cancel()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "stuck-worker") || !strings.Contains(err.Error(), "shutdown deadline") {
		t.Fatalf("run lifecycle error = %v, want named timeout", err)
	}
	close(release)
	<-exited
}

func TestRoutesValidateWithoutRunningTasks(t *testing.T) {
	var ran atomic.Bool
	app := New()
	app.Go("worker", func(context.Context) error {
		ran.Store(true)
		return nil
	})
	app.Get("/", func(context *Context) error {
		return context.NoContent(204)
	})

	if _, err := app.Routes(); err != nil {
		t.Fatalf("Routes(): %v", err)
	}
	if ran.Load() {
		t.Fatal("route inspection ran background tasks")
	}
}

type taskLog struct {
	message string
	task    string
}

type taskLogHandler struct {
	logs chan<- taskLog
}

func (handler taskLogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (handler taskLogHandler) Handle(_ context.Context, record slog.Record) error {
	entry := taskLog{message: record.Message}
	record.Attrs(func(attribute slog.Attr) bool {
		if attribute.Key == "task" {
			entry.task = attribute.Value.String()
		}
		return true
	})
	handler.logs <- entry
	return nil
}

func (handler taskLogHandler) WithAttrs([]slog.Attr) slog.Handler {
	return handler
}

func (handler taskLogHandler) WithGroup(string) slog.Handler {
	return handler
}
