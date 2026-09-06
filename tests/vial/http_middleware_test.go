package vial_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/jrgf/go-vial"
)

type httpMiddlewareContextKey struct{}

func TestHTTPMiddlewareWrapsCompleteApplication(t *testing.T) {
	var order []string
	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			order = append(order, "outer:before")
			writer.Header().Set("X-HTTP-Middleware", "wrapped")
			request = request.WithContext(context.WithValue(request.Context(), httpMiddlewareContextKey{}, "value"))
			next.ServeHTTP(writer, request)
			order = append(order, "outer:after")
		})
	}
	inner := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			order = append(order, "inner:before")
			next.ServeHTTP(writer, request)
			order = append(order, "inner:after")
		})
	}

	app := vial.New()
	app.UseHTTP(outer, inner)
	app.Get("/", func(contextValue *vial.Context) error {
		order = append(order, "handler")
		if got := contextValue.Request().Context().Value(httpMiddlewareContextKey{}); got != "value" {
			t.Fatalf("request context value = %v", got)
		}
		return vial.BadRequest("bad", "bad request")
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-HTTP-Middleware") != "wrapped" {
		t.Fatalf("response: status=%d headers=%v", response.Code, response.Header())
	}
	want := []string{"outer:before", "inner:before", "handler", "inner:after", "outer:after"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestHTTPMiddlewareMayShortCircuit(t *testing.T) {
	app := vial.New()
	app.UseHTTP(func(http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusTeapot)
		})
	})
	app.Get("/", func(*vial.Context) error {
		t.Fatal("handler was called")
		return nil
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHTTPMiddlewareRejectsNilWrappedHandler(t *testing.T) {
	app := vial.New()
	app.UseHTTP(func(http.Handler) http.Handler { return nil })
	if err := app.Build(); err == nil {
		t.Fatal("expected build error")
	}
}

func TestRequestContextReplacementPreservesVialState(t *testing.T) {
	key := vial.NewValueKey[string]("identity")
	app := vial.New()
	var original *vial.Context
	finished := false
	app.Use(func(next vial.Handler) vial.Handler {
		return func(c *vial.Context) error {
			original = c
			key.Set(c, "Ada")
			if err := c.AfterResponse(func() { finished = true }); err != nil {
				return err
			}
			replacement, cancel := context.WithCancel(context.WithValue(context.Background(), httpMiddlewareContextKey{}, "replacement"))
			cancel()
			*c.Request() = *c.Request().WithContext(replacement)
			c.Request().URL.Path = "/raw/42"
			return next(c)
		}
	})
	app.HandleHTTP("GET /raw/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := vial.ContextFromRequest(r)
		if !ok || c != original || c.Param("id") != "42" || c.Route().Path != "/raw/{id}" {
			t.Errorf("lost Vial context or rewritten route: context=%p original=%p", c, original)
		}
		if value, ok := key.FromRequest(r); !ok || value != "Ada" {
			t.Errorf("lost request identity: %q/%v", value, ok)
		}
		if r.Context().Err() != context.Canceled || r.Context().Value(httpMiddlewareContextKey{}) != "replacement" {
			t.Error("lost replacement context cancellation or values")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/before", nil))
	if response.Code != http.StatusNoContent || !finished {
		t.Fatalf("status=%d after-response hook=%v", response.Code, finished)
	}
}

func TestConcurrentFirstRequestValueWrites(t *testing.T) {
	app := vial.New()
	app.Get("/", func(c *vial.Context) error {
		var workers sync.WaitGroup
		for index := range 32 {
			workers.Go(func() {
				key := strconv.Itoa(index)
				c.Set(key, index)
				if value, ok := c.Get(key); !ok || value != index {
					t.Errorf("request value %q = %v/%v", key, value, ok)
				}
			})
		}
		workers.Wait()
		return c.NoContent(http.StatusNoContent)
	})
	for range 2 {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d", response.Code)
		}
	}
}
