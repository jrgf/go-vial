// Package testkit provides lifecycle-aware HTTP helpers for Vial applications.
package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"testing"

	"github.com/jrgf/go-vial"
)

// Server runs a Vial application for the duration of a test.
type Server struct {
	URL    string
	Client *http.Client

	t         testing.TB
	cancel    context.CancelFunc
	done      <-chan error
	transport *http.Transport
	closeOnce sync.Once
	closeErr  error
}

// Response wraps an HTTP response with test assertions and decoding helpers.
type Response struct {
	*http.Response

	body []byte
	t    testing.TB
}

// File describes a file included in a multipart request.
type File struct {
	Field string
	Name  string
	Body  io.Reader
}

// Fault is Vial's public HTTP error representation.
type Fault struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
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
	transport := &http.Transport{}
	server := &Server{
		URL:       "http://" + listener.Addr().String(),
		Client:    &http.Client{Transport: transport, Jar: jar},
		t:         t,
		cancel:    cancel,
		done:      done,
		transport: transport,
	}
	t.Cleanup(func() {
		if serveErr := server.Close(); serveErr != nil {
			t.Errorf("testkit: stop application: %v", serveErr)
		}
	})
	return server
}

// Close stops the application and waits for its shutdown hooks to finish.
// It is safe to call more than once.
func (server *Server) Close() error {
	server.closeOnce.Do(func() {
		server.transport.CloseIdleConnections()
		server.cancel()
		server.closeErr = <-server.done
	})
	return server.closeErr
}

// RequireRoute returns matching route metadata or fails the test.
func RequireRoute(t testing.TB, app *vial.App, method, path string) vial.Route {
	t.Helper()
	if app == nil {
		t.Fatal("testkit: application is nil")
	}
	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("testkit: inspect routes: %v", err)
	}
	for _, route := range routes {
		if route.Method == method && route.Path == path {
			return route
		}
	}
	t.Fatalf("testkit: route %s %s is not registered; routes=%#v", method, path, routes)
	return vial.Route{}
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

// Multipart sends form fields and files as a multipart request.
func (server *Server) Multipart(method, path string, fields url.Values, files ...File) *Response {
	server.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, values := range fields {
		for _, value := range values {
			if err := writer.WriteField(name, value); err != nil {
				server.t.Fatalf("testkit: write multipart field: %v", err)
			}
		}
	}
	for _, file := range files {
		if file.Body == nil {
			server.t.Fatalf("testkit: multipart file %q has no body", file.Name)
		}
		part, err := writer.CreateFormFile(file.Field, file.Name)
		if err != nil {
			server.t.Fatalf("testkit: create multipart file: %v", err)
		}
		if _, err := io.Copy(part, file.Body); err != nil {
			server.t.Fatalf("testkit: write multipart file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		server.t.Fatalf("testkit: close multipart request: %v", err)
	}
	request := server.NewRequest(method, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
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

// Text returns the response body as text.
func (response *Response) Text() string {
	return string(response.body)
}

// Fault decodes Vial's public HTTP error response.
func (response *Response) Fault() Fault {
	response.t.Helper()
	var payload struct {
		Error Fault `json:"error"`
	}
	response.Decode(&payload)
	return payload.Error
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
