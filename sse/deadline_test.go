package sse

import (
	"bufio"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTP2IdleStreamSurvivesWriteDeadline(t *testing.T) {
	hub, err := NewHub(HubConfig{Heartbeat: 300 * time.Millisecond, WriteTimeout: 75 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	server := httptest.NewUnstartedServer(hub.Handler())
	server.EnableHTTP2 = true
	server.Config.WriteTimeout = 100 * time.Millisecond
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	r, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	if r.ProtoMajor != 2 {
		t.Fatal(r.Proto)
	}
	line, err := bufio.NewReader(r.Body).ReadString('\n')
	if err != nil || line != ": heartbeat\n" {
		t.Fatalf("line=%q err=%v", line, err)
	}
}
