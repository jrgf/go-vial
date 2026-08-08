package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial/testkit"
)

func TestEventQueue(t *testing.T) {
	received := make(chan Event, 1)
	server := testkit.Start(t, newApp(func(_ context.Context, event Event) error {
		received <- event
		return nil
	}))

	response := server.JSON(http.MethodPost, "/events", Event{Name: "user.created"})
	response.RequireStatus(http.StatusAccepted)

	select {
	case event := <-received:
		if event.Name != "user.created" {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("event was not consumed")
	}
}

func TestEventsExampleMain(t *testing.T) {
	t.Setenv("VIAL_ROUTES_OUTPUT", filepath.Join(t.TempDir(), "routes.json"))
	main()
}

func TestEventQueueErrors(t *testing.T) {
	app := newApp(func(context.Context, Event) error { return nil })
	malformed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader("{"))
	request.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(malformed, request)
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed status = %d", malformed.Code)
	}

	for index := 0; index < 128; index++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(`{"name":"queued"}`))
		request.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted {
			t.Fatalf("enqueue %d status = %d", index, response.Code)
		}
	}
	full := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(`{"name":"full"}`))
	request.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(full, request)
	if full.Code != http.StatusServiceUnavailable {
		t.Fatalf("full status = %d", full.Code)
	}

	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(`{"name":"cancelled"}`)).WithContext(contextValue)
	request.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(cancelled, request)
}

func TestEventHandlerFailureStopsApplication(t *testing.T) {
	want := errors.New("handler failed")
	app := newApp(func(context.Context, Event) error { return want })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	contextValue, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- app.Serve(contextValue, listener) }()
	response, err := http.Post("http://"+listener.Addr().String()+"/events", "application/json", strings.NewReader(`{"name":"fail"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("application did not stop")
	}
}
