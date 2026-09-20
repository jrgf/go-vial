package vial

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type unwrapWriter struct{ http.ResponseWriter }

func (w unwrapWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestResponseFinalizationAndUnwrappedFlush(t *testing.T) {
	for _, flush := range []bool{false, true} {
		app := New()
		app.UseHTTP(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(unwrapWriter{w}, r)
			})
		})
		calls := 0
		app.Get("/", func(c *Context) error {
			if err := c.BeforeCommit(func(h http.Header) { calls++; h.Set("X-Hook", "yes") }); err != nil {
				return err
			}
			if flush {
				if _, ok := c.Response().(http.Flusher); !ok {
					t.Error("Unwrap hid Flusher")
				}
				if err := c.Flush(); err != nil {
					return err
				}
				if !c.Committed() {
					t.Error("flush bypassed tracking")
				}
			}
			return nil
		})
		r := httptest.NewRecorder()
		app.ServeHTTP(r, httptest.NewRequest("GET", "/", nil))
		if calls != 1 || r.Result().Header.Get("X-Hook") != "yes" {
			t.Errorf("flush=%v calls=%d headers=%v", flush, calls, r.Result().Header)
		}
	}
}

func TestInformationalResponseDoesNotCommit(t *testing.T) {
	app := New()
	app.Get("/", func(c *Context) error {
		if err := c.BeforeCommit(func(h http.Header) { h.Set("X-Final", "yes") }); err != nil {
			return err
		}
		c.Response().WriteHeader(http.StatusEarlyHints)
		if c.Committed() {
			t.Error("103 committed final response")
		}
		return c.JSON(http.StatusNotFound, "missing")
	})
	server := httptest.NewServer(app)
	defer server.Close()
	r, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 404 || string(body) != "\"missing\"\n" || r.Header.Get("X-Final") != "yes" {
		t.Fatalf("status=%d headers=%v body=%q", r.StatusCode, r.Header, body)
	}
}
