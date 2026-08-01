// Package testkit provides lifecycle-aware HTTP helpers for Vial applications.
package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"testing"

	"github.com/jrgf/go-vial"
)

// Server runs a Vial application for the duration of a test.
type Server struct {
	URL    string
	Client *http.Client

	t testing.TB
}

// Response wraps an HTTP response with test assertions and decoding helpers.
type Response struct {
	*http.Response

	body []byte
	t    testing.TB
}

// Start builds and runs app on an ephemeral localhost port. The application and
// its HTTP client are stopped automatically when the test completes.
func Start(t testing.TB, app *vial.App) *Server {
	t.Helper()
	if app == nil {
		t.Fatal("testkit: application is nil")
	}
	if err := app.Build(); err != nil {
		t.Fatalf("testkit: build application: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testkit: listen: %v", err)
	}
	ready := make(chan struct{})
	tracked := &readyListener{Listener: listener, ready: ready}
	contextValue, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Serve(contextValue, tracked)
	}()

	select {
	case <-ready:
	case serveErr := <-done:
		cancel()
		t.Fatalf("testkit: start application: %v", serveErr)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		cancel()
		<-done
		t.Fatalf("testkit: create cookie jar: %v", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	server := &Server{
		URL:    "http://" + listener.Addr().String(),
		Client: &http.Client{Transport: transport, Jar: jar},
		t:      t,
	}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		cancel()
		if serveErr := <-done; serveErr != nil {
			t.Errorf("testkit: stop application: %v", serveErr)
		}
	})
	return server
}

// NewRequest creates a request for a path on the test server.
func (server *Server) NewRequest(method, path string, body io.Reader) *http.Request {
	server.t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, server.URL+path, body)
	if err != nil {
		server.t.Fatalf("testkit: create request: %v", err)
	}
	return request
}

// Do sends a request, reads and closes its response body, and returns the response.
func (server *Server) Do(request *http.Request) *Response {
	server.t.Helper()
	response, err := server.Client.Do(request)
	if err != nil {
		server.t.Fatalf("testkit: send request: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		server.t.Fatalf("testkit: read response: %v", err)
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return &Response{Response: response, body: body, t: server.t}
}

// JSON sends a JSON request.
func (server *Server) JSON(method, path string, value any) *Response {
	server.t.Helper()
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		server.t.Fatalf("testkit: encode JSON request: %v", err)
	}
	request := server.NewRequest(method, path, &body)
	request.Header.Set("Content-Type", "application/json")
	return server.Do(request)
}

// RequireStatus fails the test when the response status differs from status.
func (response *Response) RequireStatus(status int) {
	response.t.Helper()
	if response.StatusCode != status {
		response.t.Fatalf(
			"testkit: status = %d, want %d; body=%q",
			response.StatusCode,
			status,
			response.body,
		)
	}
}

// Decode decodes the JSON response body into destination.
func (response *Response) Decode(destination any) {
	response.t.Helper()
	if err := json.Unmarshal(response.body, destination); err != nil {
		response.t.Fatalf("testkit: decode JSON response: %v", err)
	}
}

type readyListener struct {
	net.Listener
	ready chan struct{}
	once  sync.Once
}

func (listener *readyListener) Accept() (net.Conn, error) {
	listener.once.Do(func() { close(listener.ready) })
	return listener.Listener.Accept()
}
