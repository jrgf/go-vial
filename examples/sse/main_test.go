package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventStream(t *testing.T) {
	server := httptest.NewServer(newApp(time.Millisecond))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/events")
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

	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	var event Event
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Time.IsZero() {
		t.Fatal("event time is zero")
	}
}
