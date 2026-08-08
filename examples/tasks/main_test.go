package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrgf/go-vial/testkit"
)

func TestTaskLifecycle(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	app := newApp(func(contextValue context.Context) error {
		close(started)
		<-contextValue.Done()
		close(stopped)
		return contextValue.Err()
	})

	server := testkit.Start(t, app)
	<-started
	response := server.Do(server.NewRequest(http.MethodGet, "/", nil))
	response.RequireStatus(http.StatusOK)
	if response.Text() != "tasks running" {
		t.Fatalf("unexpected response: %q", response.Text())
	}

	if err := server.Close(); err != nil {
		t.Fatalf("close server: %v", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("task did not stop before the application")
	}
}

func TestTasksExampleMain(t *testing.T) {
	t.Setenv("VIAL_ROUTES_OUTPUT", filepath.Join(t.TempDir(), "routes.json"))
	main()
}

func TestHeartbeatStopsWithContext(t *testing.T) {
	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	if err := heartbeat(contextValue); !errors.Is(err, context.Canceled) {
		t.Fatalf("heartbeat error = %v", err)
	}
}

func TestHeartbeatTick(t *testing.T) {
	original := heartbeatInterval
	heartbeatInterval = time.Millisecond
	t.Cleanup(func() { heartbeatInterval = original })
	contextValue, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := heartbeat(contextValue); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("heartbeat error = %v", err)
	}
}
