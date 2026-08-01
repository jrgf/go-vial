package main

import (
	"context"
	"net/http"
	"testing"

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
