package sse_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial/sse"
)

type writerFunc func([]byte) (int, error)

func (write writerFunc) Write(data []byte) (int, error) { return write(data) }

func TestEventEncoding(t *testing.T) {
	var output bytes.Buffer
	event, err := sse.JSON("note", map[string]string{"body": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	event.ID = "42"
	event.Retry = 1500 * time.Millisecond
	if err := sse.WriteEvent(&output, event); err != nil {
		t.Fatal(err)
	}
	want := "id: 42\nevent: note\nretry: 1500\ndata: {\"body\":\"hello\"}\n\n"
	if output.String() != want {
		t.Fatalf("event = %q, want %q", output.String(), want)
	}

	if err := sse.WriteEvent(io.Discard, sse.Event{ID: "bad\nid"}); err == nil {
		t.Fatal("expected invalid event ID error")
	}
	output.Reset()
	if err := sse.WriteComment(&output, "one\r\ntwo"); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != ": one\n: two\n\n" {
		t.Fatalf("comment = %q", got)
	}
}

func TestEventEncodingRejectsInvalidInputAndWriteFailures(t *testing.T) {
	writeError := errors.New("write failed")
	cases := []struct {
		name  string
		write func() error
	}{
		{"nil event writer", func() error { return sse.WriteEvent(nil, sse.Event{}) }},
		{"nil comment writer", func() error { return sse.WriteComment(nil, "") }},
		{"event name newline", func() error { return sse.WriteEvent(io.Discard, sse.Event{Name: "bad\nname"}) }},
		{"negative retry", func() error { return sse.WriteEvent(io.Discard, sse.Event{Retry: -time.Second}) }},
		{"JSON marshal", func() error { _, err := sse.JSON("event", make(chan int)); return err }},
		{"JSON event name", func() error { _, err := sse.JSON("bad\nname", struct{}{}); return err }},
		{"short write", func() error {
			return sse.WriteEvent(writerFunc(func(data []byte) (int, error) { return len(data) - 1, nil }), sse.Event{})
		}},
		{"event write error", func() error {
			return sse.WriteEvent(writerFunc(func([]byte) (int, error) { return 0, writeError }), sse.Event{})
		}},
		{"comment write error", func() error {
			return sse.WriteComment(writerFunc(func([]byte) (int, error) { return 0, writeError }), "heartbeat")
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.write(); err == nil {
				t.Fatal("expected error")
			}
		})
	}

	var output bytes.Buffer
	if err := sse.WriteEvent(&output, sse.Event{Retry: time.Nanosecond, Data: []byte("one\rtwo")}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "retry: 1\ndata: one\ndata: two\n\n" {
		t.Fatalf("event = %q", got)
	}
	output.Reset()
	if err := sse.WriteComment(&output, ""); err != nil || output.String() != ":\n\n" {
		t.Fatalf("empty comment = %q, err=%v", output.String(), err)
	}
}

func TestHubBoundsSlowConsumers(t *testing.T) {
	hub, err := sse.NewHub(sse.HubConfig{SubscriberBuffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	events := hub.Subscribe(t.Context())
	if delivered, err := hub.Publish(sse.Event{Data: []byte("one")}); err != nil || delivered != 1 {
		t.Fatalf("first publish delivered=%d err=%v", delivered, err)
	}
	if delivered, err := hub.Publish(sse.Event{Data: []byte("two")}); err != nil || delivered != 0 {
		t.Fatalf("second publish delivered=%d err=%v", delivered, err)
	}
	if event := <-events; string(event.Data) != "one" {
		t.Fatalf("buffered event = %q", event.Data)
	}
	if _, open := <-events; open {
		t.Fatal("slow subscriber remained open")
	}

	copyHub, err := sse.NewHub(sse.HubConfig{SubscriberBuffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	first := copyHub.Subscribe(t.Context())
	second := copyHub.Subscribe(t.Context())
	data := []byte("safe")
	if _, err := copyHub.Publish(sse.Event{Data: data}); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	firstEvent := <-first
	firstEvent.Data[1] = 'X'
	if got := string((<-second).Data); got != "safe" {
		t.Fatalf("subscriber data = %q", got)
	}

	contextValue, cancel := context.WithCancel(context.Background())
	dropHub, err := sse.NewHub(sse.HubConfig{SubscriberBuffer: 1, SlowConsumer: sse.DropEvent})
	if err != nil {
		t.Fatal(err)
	}
	dropped := dropHub.Subscribe(contextValue)
	_, _ = dropHub.Publish(sse.Event{Data: []byte("kept")})
	_, _ = dropHub.Publish(sse.Event{Data: []byte("dropped")})
	if event := <-dropped; string(event.Data) != "kept" {
		t.Fatalf("drop policy event = %q", event.Data)
	}
	cancel()
	select {
	case _, open := <-dropped:
		if open {
			t.Fatal("canceled subscriber remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber cancellation timed out")
	}
}

func TestHubValidatesConfiguration(t *testing.T) {
	for _, config := range []sse.HubConfig{
		{SubscriberBuffer: -1},
		{Heartbeat: -1},
		{WriteTimeout: -1},
		{SlowConsumer: 99},
	} {
		if _, err := sse.NewHub(config); err == nil {
			t.Fatalf("expected configuration error for %#v", config)
		}
	}
}

func TestHubCloseAndImmediateCancellation(t *testing.T) {
	hub, err := sse.NewHub(sse.HubConfig{})
	if err != nil {
		t.Fatal(err)
	}
	//nolint:staticcheck // Exercise Subscribe's supported nil-context fallback.
	nilContextEvents := hub.Subscribe(nil)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, open := <-hub.Subscribe(canceled); open {
		t.Fatal("canceled subscription remained open")
	}
	hub.Close()
	hub.Close()
	if _, open := <-nilContextEvents; open {
		t.Fatal("subscription remained open after close")
	}
	if _, open := <-hub.Subscribe(t.Context()); open {
		t.Fatal("subscription created after close remained open")
	}
	if delivered, err := hub.Publish(sse.Event{Data: []byte("ignored")}); err != nil || delivered != 0 {
		t.Fatalf("publish after close delivered=%d err=%v", delivered, err)
	}
}

func TestHubHandlerEventsAndHeartbeats(t *testing.T) {
	hub, err := sse.NewHub(sse.HubConfig{Heartbeat: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(hub.Handler())
	t.Cleanup(server.Close)

	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream response status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != ": heartbeat" {
		t.Fatalf("heartbeat line=%q err=%v", line, err)
	}
	if _, err := hub.Publish(sse.Event{Data: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data:") {
			break
		}
	}
	if strings.TrimSpace(line) != "data: hello" {
		t.Fatalf("event line = %q", line)
	}
}
