package testkit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

type panicTB struct{ testing.TB }

func (panicTB) Helper()                                {}
func (panicTB) Fatal(arguments ...any)                 { panic(arguments) }
func (panicTB) Fatalf(format string, arguments ...any) { panic("fatal") }

func expectFatal(t *testing.T, function func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected test helper failure")
		}
	}()
	function()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type errorBody struct{}

func (errorBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (errorBody) Close() error             { return errors.New("close failed") }

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("copy failed") }

func TestCoverageStartFailures(t *testing.T) {
	tb := panicTB{TB: t}
	expectFatal(t, func() { Start(tb, vial.New(), nil) })
	expectFatal(t, func() { Start(tb, vial.New(), WithTimeout(-1)) })
	expectFatal(t, func() { Start(tb, nil) })

	invalid := vial.New()
	invalid.Handle(" ", "/", func(*vial.Context) error { return nil })
	expectFatal(t, func() { Start(tb, invalid) })

	startup := vial.New()
	startup.OnStart(func(context.Context) error { return errors.New("start failed") })
	expectFatal(t, func() { Start(tb, startup) })
}

func TestCoverageRequestFailures(t *testing.T) {
	tb := panicTB{TB: t}
	server := &Server{t: tb, URL: "http://example.com"}
	expectFatal(t, func() { server.NewRequest(http.MethodGet, "/\n", nil) })

	server.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("send failed")
	})}
	expectFatal(t, func() {
		server.Do(mustRequest(t, http.MethodGet, "http://example.com", nil))
	})

	server.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: errorBody{}}, nil
	})}
	expectFatal(t, func() {
		server.Do(mustRequest(t, http.MethodGet, "http://example.com", nil))
	})

	expectFatal(t, func() { server.JSON(http.MethodPost, "/", make(chan int)) })
	expectFatal(t, func() {
		server.Multipart(http.MethodPost, "/", nil, File{Name: "missing", Body: nil})
	})
	expectFatal(t, func() {
		server.Multipart(http.MethodPost, "/", nil, File{Field: "file", Name: "name", Body: errorReader{}})
	})
	expectFatal(t, func() {
		server.Multipart(http.MethodPost, "/", url.Values{"bad\nfield": {"value"}})
	})
	expectFatal(t, func() {
		server.Multipart(http.MethodPost, "/", nil, File{Field: "bad\nfield", Name: "name", Body: strings.NewReader("body")})
	})
}

func TestCoverageAssertionFailures(t *testing.T) {
	tb := panicTB{TB: t}
	expectFatal(t, func() { RequireRoute(tb, nil, http.MethodGet, "/") })

	invalid := vial.New()
	invalid.Handle(" ", "/", func(*vial.Context) error { return nil })
	expectFatal(t, func() { RequireRoute(tb, invalid, http.MethodGet, "/") })
	expectFatal(t, func() { RequireRoute(tb, vial.New(), http.MethodGet, "/missing") })

	response := &Response{Response: &http.Response{StatusCode: http.StatusOK}, body: []byte("invalid"), t: tb}
	expectFatal(t, func() { response.RequireStatus(http.StatusCreated) })
	expectFatal(t, func() { response.Decode(new(map[string]any)) })
}

func mustRequest(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
