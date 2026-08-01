package main

import (
	"context"
	"net/http"
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
