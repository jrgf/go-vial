package vial_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/testkit"
)

func TestStreamingAndRepeatedFlushHTTP1(t *testing.T) {
	app := vial.New()
	firstFlushed := make(chan struct{})
	release := make(chan struct{})
	app.Get("/events", func(context *vial.Context) error {
		context.Response().Header().Set("Content-Type", "text/event-stream")
		if err := context.SetWriteDeadline(time.Time{}); err != nil {
			return err
		}
		_, _ = io.WriteString(context.Response(), "data: one\n\n")
		if err := context.Flush(); err != nil {
			return err
		}
		close(firstFlushed)
		<-release
		_, _ = io.WriteString(context.Response(), "data: two\n\n")
		return context.Flush()
	})

	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	response, err := server.Client().Get(server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "data: one\n" {
		t.Fatalf("first event=%q err=%v", first, err)
	}
	<-firstFlushed
	time.Sleep(10 * time.Millisecond)
	close(release)
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "data: two") || response.ProtoMajor != 1 {
		t.Fatalf("stream remainder=%q proto=%s err=%v", rest, response.Proto, err)
	}
}

func TestGracefulShutdownWaitsForLongLivedRequest(t *testing.T) {
	app := vial.New(vial.WithShutdownTimeout(time.Second))
	started := make(chan struct{})
	release := make(chan struct{})
	app.Get("/long", func(context *vial.Context) error {
		close(started)
		<-release
		return context.Text(http.StatusOK, "complete")
	})
	server := testkit.Start(t, app)
	type result struct {
		response *http.Response
		err      error
	}
	requestDone := make(chan result, 1)
	go func() {
		response, err := server.Client.Get(server.URL + "/long")
		requestDone <- result{response: response, err: err}
	}()
	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- server.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("shutdown returned before active request completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	requestResult := <-requestDone
	if requestResult.err != nil {
		t.Fatal(requestResult.err)
	}
	body, err := io.ReadAll(requestResult.response.Body)
	_ = requestResult.response.Body.Close()
	if err != nil || requestResult.response.StatusCode != http.StatusOK || string(body) != "complete" {
		t.Fatalf("long request: status=%d body=%q err=%v", requestResult.response.StatusCode, body, err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}

func TestStreamingFlushHTTP2(t *testing.T) {
	app := vial.New()
	app.Get("/events", func(context *vial.Context) error {
		_, _ = io.WriteString(context.Response(), "data: h2\n\n")
		return context.Flush()
	})
	server := httptest.NewUnstartedServer(app)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	response, err := server.Client().Get(server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.ProtoMajor != 2 || string(body) != "data: h2\n\n" {
		t.Fatalf("HTTP/2 stream: proto=%s body=%q err=%v", response.Proto, body, err)
	}
}

func TestClientDisconnectCancelsRequest(t *testing.T) {
	app := vial.New()
	started := make(chan struct{})
	canceled := make(chan struct{})
	app.Get("/wait", func(requestContext *vial.Context) error {
		close(started)
		<-requestContext.Request().Context().Done()
		close(canceled)
		return nil
	})
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/wait", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("client request unexpectedly survived cancellation")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("handler did not observe client cancellation")
	}
}

func TestHTTP1HijackingThroughWrappedWriter(t *testing.T) {
	app := vial.New()
	app.Get("/upgrade", func(context *vial.Context) error {
		connection, buffer, err := context.ResponseController().Hijack()
		if err != nil {
			return err
		}
		defer func() { _ = connection.Close() }()
		_, err = buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\nupgraded\n")
		if err == nil {
			err = buffer.Flush()
		}
		return err
	})
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)

	address := strings.TrimPrefix(server.URL, "http://")
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if _, err := fmt.Fprintf(connection, "GET /upgrade HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n", address); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := reader.ReadString('\n')
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols || body != "upgraded\n" {
		t.Fatalf("upgrade response: status=%d body=%q err=%v", response.StatusCode, body, err)
	}
}
