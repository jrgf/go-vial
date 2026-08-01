package vial

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
)

type recordingComponent struct {
	events      *[]string
	done        chan error
	startErr    error
	runErr      error
	shutdownErr error
}

func (component *recordingComponent) Start(context.Context) error {
	*component.events = append(*component.events, "component start")
	if component.startErr != nil {
		return component.startErr
	}
	if component.runErr != nil {
		component.done <- component.runErr
	}
	return nil
}

func (component *recordingComponent) Done() <-chan error {
	return component.done
}

func (component *recordingComponent) Shutdown(context.Context) error {
	*component.events = append(*component.events, "component stop")
	if component.runErr == nil {
		component.done <- nil
	}
	return component.shutdownErr
}

func TestLifecycleOrder(t *testing.T) {
	events := make([]string, 0, 6)
	app := New()
	app.OnStart(
		func(context.Context) error {
			events = append(events, "start 1")
			return nil
		},
		func(context.Context) error {
			events = append(events, "start 2")
			return nil
		},
	)
	app.OnStop(
		func(context.Context) error {
			events = append(events, "stop 1")
			return nil
		},
		func(context.Context) error {
			events = append(events, "stop 2")
			return nil
		},
	)

	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	component := &recordingComponent{events: &events, done: make(chan error, 1)}
	if err := app.runLifecycle(contextValue, component); err != nil {
		t.Fatalf("run lifecycle: %v", err)
	}

	want := []string{"start 1", "start 2", "component start", "component stop", "stop 2", "stop 1"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if app.state != applicationStopped {
		t.Fatalf("state = %d, want stopped", app.state)
	}
}

func TestLifecyclePropagatesFailures(t *testing.T) {
	runErr := errors.New("component failed")
	shutdownErr := errors.New("component shutdown failed")
	hookErr := errors.New("shutdown hook failed")
	events := make([]string, 0, 3)
	app := New()
	app.OnStop(func(context.Context) error {
		events = append(events, "hook stop")
		return hookErr
	})
	component := &recordingComponent{
		events:      &events,
		done:        make(chan error, 1),
		runErr:      runErr,
		shutdownErr: shutdownErr,
	}

	err := app.runLifecycle(context.Background(), component)
	for _, want := range []error{runErr, shutdownErr, hookErr} {
		if !errors.Is(err, want) {
			t.Errorf("run lifecycle error = %v, want wrapped %v", err, want)
		}
	}
	wantEvents := []string{"component start", "component stop", "hook stop"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
}

func TestLifecycleStartupFailureSkipsComponents(t *testing.T) {
	wantErr := errors.New("startup failed")
	events := make([]string, 0, 2)
	app := New()
	app.OnStart(func(context.Context) error {
		events = append(events, "hook start")
		return wantErr
	})
	app.OnStop(func(context.Context) error {
		events = append(events, "hook stop")
		return nil
	})
	component := &recordingComponent{events: &events, done: make(chan error, 1)}

	err := app.runLifecycle(context.Background(), component)
	if !errors.Is(err, wantErr) {
		t.Fatalf("run lifecycle error = %v, want wrapped %v", err, wantErr)
	}
	wantEvents := []string{"hook start", "hook stop"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
}

func TestLifecycleHooksAreImmutableAfterBuild(t *testing.T) {
	app := New()
	if err := app.Build(); err != nil {
		t.Fatalf("build: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected lifecycle hook registration to panic after build")
		}
	}()
	app.OnStart(func(context.Context) error { return nil })
}

func TestServePropagatesHTTPFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	err = New().Serve(context.Background(), listener)
	if err == nil || !strings.Contains(err.Error(), "serve HTTP") {
		t.Fatalf("serve error = %v, want HTTP failure", err)
	}
}
