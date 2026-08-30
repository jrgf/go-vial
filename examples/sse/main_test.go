package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial/testkit"
)

func TestEventStream(t *testing.T) {
	app, err := newApp(time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	server := testkit.Start(t, app)

	response, err := server.Client.Get(server.URL + "/events")
	if err != nil {
		t.Fatalf("get event stream: %v", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("content type = %q", contentType)
	}
	if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "no-cache" {
		t.Fatalf("cache control = %q", cacheControl)
	}

	reader := bufio.NewReader(response.Body)
	var line string
	for !strings.HasPrefix(line, "data:") {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read event: %v", err)
		}
	}
	var event Event
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Time.IsZero() {
		t.Fatal("event time is zero")
	}
}
